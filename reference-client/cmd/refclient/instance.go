package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

// instance is one running participant: an endpoint, a database of its own, a
// connection to the stream, and the person at the screens.
//
// One process is one participant on purpose. Two of them exchanging data is two
// processes with two databases and two applications — anything less shares a
// stream and an identifier namespace, and would demonstrate something the
// protocol does not do.
type instance struct {
	cfg   config
	store *store.Store
	log   *eventLog
	inbox *inbox

	endpointID uuid.UUID
	client     *agmasync.Client
	endpoint   *agmasync.Endpoint
	applier    *psync.Applier
	receiver   *psync.Receiver
	ids        *localIDs

	// loads carries the news that an initial load may be owed. Buffered and
	// coalescing: what matters is that a load runs after the last signal, not
	// that one runs per signal.
	loads chan struct{}

	mu       sync.Mutex
	state    string
	caughtUp bool
	blocked  []psync.BlockedObject
	lastErr  string

	// routed is the routing the last signal carried, and nil where the signal
	// carried none — at startup, where there is no frame behind it.
	//
	// It is carried in memory rather than read back because the frame that
	// changes it signals from inside the transaction it is written in: a reader
	// racing that transaction sees the routing as it was before the change, and
	// concluding "routed to nothing, so nothing is owed" is exactly the wrong
	// answer to a frame that has just widened it.
	routed []agmasync.EntityType

	// declared is what this endpoint last told agrirouter it can exchange. It is
	// held here because nothing reads it back: the declaration travels on
	// PutEndpoint and agrirouter answers with the endpoint, not with the list.
	declared []agmasync.EntityType

	// routingUnknown is the disagreement worth showing: agrirouter says this
	// endpoint owes a load, and this participant has not been told what it is
	// routed to. It means a ROUTE_CHANGED went missing, which is recoverable
	// only by asking for the stream from the beginning.
	routingUnknown bool

	// replayNext has the receive loop reconnect from the beginning rather than
	// from the stored position, and cancelStream ends the connection it is
	// currently parked on so that it does so now rather than whenever the
	// connection happens to drop.
	replayNext   bool
	cancelStream context.CancelFunc
}

func newInstance(ctx context.Context, cfg config) (*instance, error) {
	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return nil, err
	}

	log := newEventLog()
	endpointID, created, err := onboardPatiently(
		ctx, cfg, cfg.httpClient(ctx), log, cfg.masterdata)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if created {
		log.say("onboard", "endpoint created: "+endpointID.String())
	} else {
		log.say("onboard", "endpoint already existed: "+endpointID.String())
	}

	client, err := agmasync.NewClient(cfg.baseURL, cfg.options(ctx)...)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	in := &instance{
		cfg: cfg, store: db, log: log,
		inbox:      newInbox(ctx, cfg.decisionTimeout, log),
		endpointID: endpointID,
		client:     client,
		loads:      make(chan struct{}, 1),
	}
	in.declared = cfg.masterdata
	in.endpoint = client.For(endpointID, cfg.externalID(),
		cfg.applicationID, cfg.tenantID, cfg.softwareVersionID, endpointType)

	if in.ids, err = newLocalIDs(db, cfg.instance, cfg.tenantID.String()); err != nil {
		_ = db.Close()
		return nil, err
	}

	in.applier = &psync.Applier{
		Store: db,
		// This sample serves one tenancy per process, so the platform's own key
		// for it can be agrirouter's. A product holding several would have its
		// own, and an applier per tenant over one store.
		Tenant:   cfg.tenantID.String(),
		Endpoint: in.endpoint,
		IDs:      in.ids,
	}
	in.receiver = &psync.Receiver{
		Client:      client,
		Store:       db,
		Tenants:     map[uuid.UUID]*psync.Applier{cfg.tenantID: in.applier},
		OnApplied:   in.onApplied,
		OnCaughtUp:  in.onCaughtUp,
		OnSelection: in.onRouteChanged,
	}
	return in, nil
}

func (in *instance) Close() error { return in.store.Close() }

// run connects, stays connected, and drives a load whenever one is owed.
func (in *instance) run(ctx context.Context) {
	go in.loadLoop(ctx)

	// A load may already have been owed while this participant was down, and
	// nothing will arrive on the stream to say so: the routing that started it
	// was stated below the position this connection resumes from. Which is what
	// the stored routing is for, and why this signal carries none.
	in.wantLoad(nil)

	in.receive(ctx)
}

// receive keeps the live stream open, reconnecting from the last durably
// applied position.
//
// The stream ending is not a signal and not an error: it is a dropped
// connection. [psync.Receiver.Run] returns rather than reconnecting so that
// this decides how long to wait, which is the one thing a sample can do that a
// library should not.
func (in *instance) receive(ctx context.Context) {
	const first, longest = time.Second, 30 * time.Second
	wait := first

	for ctx.Err() == nil {
		// Each connection gets a context of its own so that a replay can end the
		// one in flight. Without that, asking for the stream from the beginning
		// would take effect whenever the current connection happened to drop,
		// which on a healthy stream is never.
		attempt, cancel := context.WithCancel(ctx)

		in.mu.Lock()
		fromStart := in.replayNext
		in.replayNext = false
		in.cancelStream = cancel
		in.mu.Unlock()

		var res psync.ReceiveResult
		var err error
		if fromStart {
			in.log.say("stream", "reconnecting from the beginning")
			res, err = in.receiver.RunFromStart(attempt)
		} else {
			res, err = in.receiver.Run(attempt)
		}
		cancel()

		if ctx.Err() != nil {
			return
		}

		// A connection this program ended on purpose is not a failure, and the
		// next one must not wait out a backoff meant for a broken stream.
		deliberate := attempt.Err() != nil
		switch {
		case deliberate:
			wait = first
		case err != nil:
			in.fail(fmt.Sprintf("stream failed: %v", err))
		default:
			in.log.say("stream", "stream ended, reconnecting")
		}

		in.setCaughtUp(false)
		if res.CaughtUp {
			// It got far enough to be current at least once, so whatever went
			// wrong is not the kind of failure that repeats immediately.
			wait = first
		}
		if deliberate {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait *= 2; wait > longest {
			wait = longest
		}
	}
}

// replayFromStart has the receive loop take the stream from the beginning.
//
// It is the only recovery there is for routing that went missing, and it is
// deliberately a person's decision rather than something this program does on a
// hunch: it costs a redelivery of everything the application has ever been
// sent.
func (in *instance) replayFromStart() {
	in.mu.Lock()
	in.replayNext = true
	cancel := in.cancelStream
	in.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// loadLoop runs one initial load at a time, whenever one may be owed.
//
// Serialised deliberately. A load holds a database transaction open while it
// waits for a person, and two of them would be two such transactions with the
// second unable to read what the first is asking about.
func (in *instance) loadLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-in.loads:
			in.runLoad(ctx)
		}
	}
}

// wantLoad says a load may be owed, carrying the routing to run it against
// where the caller has it and nil where it is to be read back from the store.
func (in *instance) wantLoad(types []agmasync.EntityType) {
	if types != nil {
		in.mu.Lock()
		in.routed = types
		in.mu.Unlock()
	}
	select {
	case in.loads <- struct{}{}:
	default:
	}
}

// runLoad drives whatever the endpoint's initial load still owes.
//
// The routed types are whatever the last signal carried, falling back to the
// store for a load with no frame behind it. The endpoint's own declaration is
// not a substitute for either: it is a superset of what the user actually asked
// for, and loading against it offers agrirouter back types nobody wanted.
func (in *instance) runLoad(ctx context.Context) {
	in.mu.Lock()
	types := in.routed
	in.mu.Unlock()

	if types == nil {
		var err error
		if types, err = in.routedTypes(); err != nil {
			in.fail(fmt.Sprintf("reading routing: %v", err))
			return
		}
	}

	if len(types) == 0 {
		// Routed to nothing as far as this participant knows — but that is its
		// own belief, and the frame that would have corrected it is exactly the
		// thing that may have gone missing. agrirouter is asked instead: an
		// endpoint routed to nothing has no initial-load state at all, so a state
		// coming back here is a load owed against routing nobody told us about.
		owed, err := in.owesLoad(ctx)
		if err != nil {
			in.fail(fmt.Sprintf("reading initial-load state: %v", err))
			return
		}

		in.mu.Lock()
		in.routingUnknown = owed
		in.mu.Unlock()

		if owed {
			in.log.say("load",
				"a load is owed but the routing is unknown; replay the stream")
		}
		in.setState("")
		return
	}

	in.mu.Lock()
	in.routingUnknown = false
	in.mu.Unlock()

	loader := &psync.Loader{
		Applier:    in.applier,
		Reconciler: in.inbox,
		Types:      types,
	}
	res, err := loader.Run(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		in.fail(fmt.Sprintf("initial load: %v", err))
		return
	}

	in.mu.Lock()
	in.state = string(res.State)
	in.blocked = res.Blocked
	in.mu.Unlock()

	in.log.say("load", fmt.Sprintf(
		"initial load %s: %d received, %d created, %d matched, %d offered back, %d blocked",
		orNone(string(res.State)), res.Received, res.Created, res.Matched,
		res.Sent, len(res.Blocked)))
	for _, rejected := range res.Rejected {
		in.log.say("load", fmt.Sprintf("agrirouter refused a binding: %s %s",
			rejected.LocalId, rejected.Reason))
	}
	if res.UserAttentionErr != nil {
		in.log.say("load", fmt.Sprintf(
			"could not tell agrirouter a person is needed: %v", res.UserAttentionErr))
	}
}

// owesLoad asks agrirouter whether this endpoint has an initial load to do.
//
// It is the one question about routing that can be asked rather than waited
// for. The answer is coarse — something or nothing, never which types — but
// that is enough to tell a participant routed to nothing from one that has
// simply not been told, and those two look identical from in here.
func (in *instance) owesLoad(ctx context.Context) (bool, error) {
	status, err := in.endpoint.InitialLoadStatus(ctx)
	switch {
	case errors.Is(err, agmasync.ErrNotFound):
		// No initial-load state at all, which is what an endpoint routed to
		// nothing has. Nothing is owed and nothing went missing.
		return false, nil
	case err != nil:
		return false, err
	}
	return status.State != agmasync.StateCompleted, nil
}

// routedTypes reads what the user has routed this endpoint to exchange.
func (in *instance) routedTypes() ([]agmasync.EntityType, error) {
	var out []agmasync.EntityType
	err := in.store.ReadTx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		var err error
		out, err = tx.Route(in.endpointID)
		return err
	})
	return out, err
}

// onRouteChanged persists the routing the frame states, in the transaction that
// records the frame's position.
//
// What it must not do is act on it here. This runs inside the receive loop's
// transaction, and taking the initial-load stream from in here would hold that
// transaction open across the whole load.
func (in *instance) onRouteChanged(tx *store.Tx, sel oapi.RouteChangedEventData) error {
	types := agmasync.SelectedTypes(sel)
	if err := tx.SetRoute(sel.EndpointId, types); err != nil {
		return err
	}

	if sel.EndpointId != in.endpointID {
		// Another of this application's endpoints. Recorded, because the frame is
		// not restated once this position is taken, but not ours to load for.
		return nil
	}

	names := make([]string, 0, len(types))
	for _, typ := range types {
		names = append(names, string(typ))
	}
	in.log.say("route", "routed to exchange: "+orNone(strings.Join(names, ", ")))

	// Widening starts an initial load and narrowing does not, but this does not
	// try to tell which: the endpoint's state is what says whether anything is
	// owed, and the loader reads it.
	in.wantLoad(types)
	return nil
}

func (in *instance) onApplied(ev agmasync.Event, out psync.Outcome) {
	what := "applied"
	switch {
	case out.Created:
		what = "created"
	case out.Matched:
		what = "matched"
	case out.Superseded:
		what = "ignored as older than what we hold"
	case out.Ignored:
		what = "ignored"
	}
	in.log.say("receive", fmt.Sprintf("%s %s %s (revision %s)",
		ev.Envelope.Type, out.LocalID, what, revisionOf(ev.Envelope)))
}

func (in *instance) onCaughtUp() {
	in.setCaughtUp(true)
	in.log.say("stream", "caught up")
}

// send writes one of the platform's records and offers it to agrirouter.
//
// The two halves are not one step. The record is the platform's whether or not
// the send succeeds, and a platform that only stored what it managed to send
// would be a strange platform.
func (in *instance) send(
	ctx context.Context, typ agmasync.EntityType, localID string, attributes []byte,
) (string, error) {
	if localID == "" {
		localID = in.ids.New(typ)
	}

	record, err := recordFrom(typ, localID, attributes)
	if err != nil {
		return "", err
	}
	if err := in.store.Tx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		return tx.UpsertRecord(record, localID)
	}); err != nil {
		return "", err
	}

	out, err := in.applier.Send(ctx, typ, localID)
	if err != nil {
		return localID, err
	}
	in.log.say("send", fmt.Sprintf("%s %s sent", typ, out.LocalID))
	return localID, nil
}

// declare restates what this participant's software can exchange.
//
// It is PutEndpoint again, the same call onboarding makes, because the
// declaration is part of the endpoint rather than a resource of its own — so
// stating it is stating the whole endpoint, and what is not named is withdrawn.
//
// Withdrawing is the part with consequences. A type dropped here is dropped
// from the user's routing too: they cannot have selected what the participant
// no longer says it can exchange, and agrirouter narrows their selection to
// match. Widening is the harmless direction — it enables nothing on its own,
// and what the endpoint exchanges is still the user's choice.
func (in *instance) declare(ctx context.Context, types []agmasync.EntityType) error {
	if _, _, err := onboard(ctx, in.cfg, in.cfg.httpClient(ctx), types); err != nil {
		return err
	}

	// What went out is the closure, not the ticks: a declaration naming fields
	// names the farms they hang off. Reading it back from the same helper keeps
	// the screen honest about what agrirouter was actually told.
	declared := agmasync.DependencyClosure(types)
	in.mu.Lock()
	in.declared = declared
	in.mu.Unlock()

	names := make([]string, 0, len(declared))
	for _, typ := range declared {
		names = append(names, string(typ))
	}
	in.log.say("declare", "declared: "+orNone(strings.Join(names, ", ")))
	return nil
}

// declaredTypes is what the screens show as ticked.
func (in *instance) declaredTypes() []agmasync.EntityType {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.declared
}

// create stores a record the platform's user just filled in, and offers it to
// agrirouter where the user has routed its type.
//
// The routing is the only thing that decides whether anything leaves. A farm
// management system creates the objects its user creates whatever agrirouter
// knows about; exchanging them is a separate decision, made elsewhere, and a
// platform that refused to create what it cannot send would be letting
// agrirouter run its data model.
//
// An unrouted record is stored with no binding, which is exactly the state an
// initial load offers back later: routing the type afterwards sends it then.
func (in *instance) create(
	ctx context.Context, typ agmasync.EntityType, attributes []byte,
) (string, bool, error) {
	routed, err := in.routedTypes()
	if err != nil {
		return "", false, err
	}
	if slices.Contains(routed, typ) {
		localID, err := in.send(ctx, typ, "", attributes)
		return localID, true, err
	}

	localID := in.ids.New(typ)
	record, err := recordFrom(typ, localID, attributes)
	if err != nil {
		return "", false, err
	}
	if err := in.store.Tx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		return tx.UpsertRecord(record, localID)
	}); err != nil {
		return "", false, err
	}
	in.log.say("create", fmt.Sprintf(
		"%s %s created; %s is not routed, nothing sent", typ, localID, typ))
	return localID, false, nil
}

func (in *instance) deactivate(
	ctx context.Context, typ agmasync.EntityType, localID string,
) error {
	if err := in.store.Tx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		return tx.SetArchived(typ, localID, true)
	}); err != nil {
		return err
	}
	if _, err := in.applier.Deactivate(ctx, typ, localID); err != nil {
		return err
	}
	in.log.say("send", fmt.Sprintf("%s %s deactivated", typ, localID))
	return nil
}

// deleteRecord drops a record this platform holds, telling agrirouter first
// where there is anything to tell.
//
// Deleting a record is the platform's own act and the protocol models nothing
// of it: agrirouter never removes a canonical object, and deactivation — which
// does travel — says the entity is inactive in the world, which is a different
// claim and not one to make on the platform's behalf just because its user
// tidied up.
//
// What matters is the mapping. A local deletion that leaves one standing leaves
// agrirouter resolving this platform's identifier to a record that is gone, and
// the object's next change arriving under an identifier that resolves to
// nothing. So the three cases differ by whether a mapping exists and whether
// deactivation is available instead:
//
//   - Nothing bound — never sent, or already unbound. Delete and tell nobody:
//     there is no mapping to leave behind.
//   - Bound, type not routed. Unbind first, which is exactly the declaration
//     the specification provides for a record its user deleted locally, and
//     then delete. Deactivation is not an option here — the endpoint cannot
//     send about a type it is not routed to — so refusing would leave the
//     record with nothing that can be done to it at all.
//   - Bound, type routed. Refused. Deactivation is available and is what a
//     platform means when an entity is gone in the world; unbinding instead
//     would silently keep the object alive for every other participant while
//     this one stopped hearing about it.
func (in *instance) deleteRecord(
	ctx context.Context, typ agmasync.EntityType, localID string,
) error {
	var bound bool
	if err := in.store.ReadTx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		row, err := tx.SyncRow(typ, localID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil
			}
			return err
		}
		bound = row.Bound()
		return nil
	}); err != nil {
		return err
	}

	told := false
	if bound {
		routed, err := in.routedTypes()
		if err != nil {
			return err
		}
		if slices.Contains(routed, typ) {
			return fmt.Errorf(
				"%s %q is bound and %s is routed: deactivate it instead",
				typ, localID, typ)
		}
		// agrirouter first. A platform that dropped the record locally but
		// failed to say so would leave the pair standing with nothing behind it.
		if err := in.applier.Unbind(ctx, typ, localID); err != nil {
			return fmt.Errorf("unbinding before deleting: %w", err)
		}
		told = true
	}

	if err := in.store.Tx(in.cfg.tenantID.String(), func(tx *store.Tx) error {
		return tx.DeleteRecord(typ, localID)
	}); err != nil {
		return err
	}

	what := fmt.Sprintf("%s %s deleted", typ, localID)
	if told {
		what = fmt.Sprintf("%s %s unbound and deleted", typ, localID)
	}
	in.log.say("delete", what)
	return nil
}

// request asks for one canonical object back, which arrives on the live stream.
//
// It is how a blocked object returns: the canonical set is delivered once, so
// an object nobody could decide during the load is not sent again on its own.
func (in *instance) request(
	ctx context.Context, typ agmasync.EntityType, agrirouterID uuid.UUID,
) error {
	if err := in.endpoint.Request(ctx, typ, agrirouterID); err != nil {
		return err
	}
	in.log.say("request", fmt.Sprintf("asked for %s %s", typ, agrirouterID))

	in.mu.Lock()
	kept := in.blocked[:0]
	for _, b := range in.blocked {
		if b.AgrirouterID != agrirouterID {
			kept = append(kept, b)
		}
	}
	in.blocked = kept
	in.mu.Unlock()

	// The object arrives on the live stream and is applied there, so what is left
	// for the load is the confirmation and the push it stopped short of.
	in.wantLoad(nil)
	return nil
}

// recordFrom turns the attributes a person typed into one of the platform's
// records.
//
// It goes through the wire shape rather than straight into columns, because
// that is where the split between what this platform models and what it merely
// relays is decided — and getting an attribute this platform has no column for
// is the ordinary case, not an error.
func recordFrom(typ agmasync.EntityType, localID string, attributes []byte) (store.Record, error) {
	var fields map[string]json.RawMessage
	if len(attributes) > 0 {
		if err := json.Unmarshal(attributes, &fields); err != nil {
			return store.Record{}, fmt.Errorf("the attributes are not a JSON object: %w", err)
		}
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["type"], _ = json.Marshal(typ)
	fields["local_id"], _ = json.Marshal(localID)

	raw, err := json.Marshal(fields)
	if err != nil {
		return store.Record{}, err
	}
	var entity oapi.Entity
	if err := entity.UnmarshalJSON(raw); err != nil {
		return store.Record{}, fmt.Errorf("reading the entity: %w", err)
	}
	return store.FromEntity(typ, entity)
}

func (in *instance) setState(state string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.state = state
}

func (in *instance) setCaughtUp(v bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.caughtUp = v
}

func (in *instance) fail(text string) {
	in.mu.Lock()
	in.lastErr = text
	in.mu.Unlock()
	in.log.say("error", text)
}

func revisionOf(env agmasync.Envelope) string {
	if env.Revision == nil {
		return "none"
	}
	return strconv.Itoa(*env.Revision)
}

func orNone(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}

// localIDs mints this platform's own identifiers the way a system with an
// auto-increment key does: sequentially per type, and not of the platform's
// choosing in any meaningful sense. That is the reason binding exists at all.
//
// The counter is seeded from what the database already holds, so a restarted
// instance does not hand out an identifier it used before it went down. It is
// held in memory rather than read per call because New is called from inside the
// transaction the object is being applied in.
type localIDs struct {
	prefix string

	mu sync.Mutex
	n  map[agmasync.EntityType]int
}

func newLocalIDs(db *store.Store, prefix, tenant string) (*localIDs, error) {
	ids := &localIDs{prefix: prefix, n: map[agmasync.EntityType]int{}}
	err := db.ReadTx(tenant, func(tx *store.Tx) error {
		for _, typ := range agmasync.EntityTypes {
			localIDs, err := tx.LocalIDs(typ)
			if err != nil {
				return err
			}
			for _, id := range localIDs {
				if n, ok := ids.suffix(typ, id); ok && n > ids.n[typ] {
					ids.n[typ] = n
				}
			}
		}
		return nil
	})
	return ids, err
}

// suffix reads back the counter out of an identifier this platform minted, and
// reports false for one it did not — a record named by hand, or by another
// participant whose identifier arrived with the object.
func (l *localIDs) suffix(typ agmasync.EntityType, id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, fmt.Sprintf("%s-%s-", l.prefix, typ))
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

func (l *localIDs) New(typ agmasync.EntityType) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n[typ]++
	return fmt.Sprintf("%s-%s-%d", l.prefix, typ, l.n[typ])
}
