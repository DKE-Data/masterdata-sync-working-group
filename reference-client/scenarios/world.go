package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter"
	"github.com/google/uuid"
)

// World is one tenant on one agrirouter, with the participants a scenario
// onboards into it.
//
// Everything it sets up goes over HTTP, including the parts that stand in for
// agrirouter's own screens, so a scenario runs unchanged against the in-process
// test router and against the containerised one.
type World struct {
	BaseURL string
	Say     *Narrator

	// Tenant is the farming business every participant in this scenario is
	// onboarded into. Objects belong to it, not to the participant that
	// contributed them.
	Tenant uuid.UUID

	// suffix keeps one scenario's endpoints out of another's way. The scenarios
	// share a router — one container serves them all — and an external endpoint
	// identifier is agrirouter-wide rather than per tenant, so two scenarios
	// reusing the name "alpha" would be naming one endpoint.
	suffix string

	// applications maps the name a scenario calls an application by to the id
	// agrirouter knows it as. A world of its own per scenario is what keeps two
	// scenarios reusing "fmis-alpha" from being one application reading both
	// their tenants, so the names need no suffixing.
	applications map[string]uuid.UUID

	control *control
	closers []func()
}

// NewWorld creates a tenant on the router at baseURL.
func NewWorld(ctx context.Context, baseURL string, say *Narrator) (*World, error) {
	c := &control{baseURL: baseURL, http: &http.Client{}}
	tenant, err := c.createTenant(ctx)
	if err != nil {
		return nil, err
	}
	return &World{
		BaseURL: baseURL, Say: say, Tenant: tenant,
		suffix:       "-" + tenant.String()[:8],
		applications: map[string]uuid.UUID{},
		control:      c,
	}, nil
}

// application returns the id agrirouter knows one of the scenario's named
// applications by, minting it the first time the name is used.
//
// The name is for the reader and goes nowhere near the wire. Two endpoints
// joined under one name are two endpoints of one application, which is what
// gives them one live stream and one identifier-mapping namespace.
func (w *World) application(name string) uuid.UUID {
	if id, ok := w.applications[name]; ok {
		return id
	}
	id := uuid.New()
	w.applications[name] = id
	return id
}

// Close releases what the scenario opened.
func (w *World) Close() {
	for i := len(w.closers) - 1; i >= 0; i-- {
		w.closers[i]()
	}
	w.closers = nil
}

// Platform is one participant: an application, an endpoint in this world's
// tenant, and a database of its own.
//
// It is a whole small FMIS rather than a client handle, because that is what the
// scenarios are about — the protocol's hard parts are all about what a
// participant does with its own store.
type Platform struct {
	// Name is what the narration calls it.
	Name string

	// ApplicationID is the application the endpoint belongs to. It is one thing
	// wearing two hats rather than two: it is what the bearer token
	// authenticates as, and it is the application_id sent on onboarding. The
	// live stream is per application; the endpoint is what acts.
	ApplicationID uuid.UUID
	ExternalID    string
	EndpointID    uuid.UUID

	Client   *agmasync.Client
	Store    *store.Store
	Applier  *psync.Applier
	Receiver *psync.Receiver

	world *World
	ids   *counterIDs
}

// counterIDs mints identifiers the way a platform with an auto-increment key
// does: sequentially per type, and not of the platform's choosing in any
// meaningful sense. That is the reason binding exists at all.
//
// A scenario also gives a participant its own records under this same
// "prefix-type-n" shape by hand — "beta-farm-1" — so New skips over anything
// already reserved rather than risk minting the identifier of a record a
// scenario named itself. IDs.New is called from inside the sync package's own
// transaction, which is why this cannot simply look the answer up in the
// store: the connection is already spoken for.
type counterIDs struct {
	prefix string
	n      map[agmasync.EntityType]int
	taken  map[string]bool
}

// reserve keeps an identifier a scenario assigned by hand from later being
// handed out again by New.
func (c *counterIDs) reserve(id string) {
	if c.taken == nil {
		c.taken = map[string]bool{}
	}
	c.taken[id] = true
}

func (c *counterIDs) New(typ agmasync.EntityType) string {
	if c.n == nil {
		c.n = map[agmasync.EntityType]int{}
	}
	var id string
	for {
		c.n[typ]++
		id = fmt.Sprintf("%s-%s-%d", c.prefix, typ, c.n[typ])
		if !c.taken[id] {
			break
		}
	}
	c.reserve(id)
	return id
}

// Join onboards a participant: an endpoint in the tenant, an empty database, and
// a declaration of what its software can exchange.
//
// It is opted into nothing yet. Declaring is the participant's step and enables
// nothing; the opt-in is the user's decision made in agrirouter, and it is what
// starts an initial load — see [Platform.OptIn].
func (w *World) Join(ctx context.Context, name, appID, externalID string) (*Platform, error) {
	// The identifiers a scenario reads are the plain ones; only what goes over
	// the wire is made unique.
	local := externalID
	externalID += w.suffix

	// The application is one identity, not two: the id sent as application_id on
	// onboarding is the same id the bearer token authenticates as, so both come
	// from here. appID is only the name this scenario calls it by.
	applicationID := w.application(appID)

	// software_version_id names the release of the participant's software, which
	// a scenario does not have, so it mints a placeholder — stable being the
	// point: it is sent on onboarding and must still be the same on every write
	// that follows.
	softwareVersionID := uuid.New()

	endpointID, err := w.onboard(ctx, externalID, applicationID, softwareVersionID)
	if err != nil {
		return nil, err
	}

	client, err := agmasync.NewClient(w.BaseURL, bearing(applicationID))
	if err != nil {
		return nil, err
	}

	db, err := store.Open(":memory:")
	if err != nil {
		return nil, err
	}
	w.closers = append(w.closers, func() { _ = db.Close() })

	p := &Platform{
		Name: name, ApplicationID: applicationID,
		ExternalID: externalID, EndpointID: endpointID,
		Client: client, Store: db, world: w,
		ids: &counterIDs{prefix: local},
	}
	p.Applier = &psync.Applier{
		Store:  db,
		Tenant: "tenant-" + externalID,
		Endpoint: client.For(endpointID, externalID,
			applicationID, w.Tenant, softwareVersionID, endpointType),
		IDs: p.ids,
	}
	p.Receiver = &psync.Receiver{
		Client:  client,
		Store:   db,
		Tenants: map[uuid.UUID]*psync.Applier{w.Tenant: p.Applier},
	}
	return p, nil
}

const endpointType = oapi.EndpointTypeToCreate("cloud_software")

// token is the credential an application presents. A real one is an OAuth
// access token that agrirouter resolves to the application; the test router
// takes the application id itself, so that the identity a request arrives with
// is visibly the same identity as the application_id it carries.
func token(applicationID uuid.UUID) string { return applicationID.String() }

// bearing authenticates an agmasync client as an application.
func bearing(applicationID uuid.UUID) agmasync.Option {
	return agmasync.WithBearerToken(token(applicationID))
}

// onboard creates the participant's endpoint, declaring in the same call what
// its software can exchange.
//
// This is endpoint management rather than masterdata sync, which is why it does
// not go through agmasync: `PUT /endpoints/{external_id}` is a create-or-update
// on the whole endpoint resource, so a participant already consuming the g4 API
// has this call and an agmasync equivalent would only duplicate it. What
// agmasync adds is everything downstream of an endpoint existing.
//
// It is also why creating an endpoint is not in the control plane below. The
// control plane stands in for what agrirouter does for a user; this is the
// participant's own first call, and a scenario that faked it would be hiding the
// one step every reader has to make work first.
//
// This platform's software handles all five entity types, so it says so.
// Declaring enables nothing: it is the list the user is later offered a choice
// from, and until they choose, the endpoint exchanges nothing.
func (w *World) onboard(
	ctx context.Context, externalID string, applicationID, softwareVersionID uuid.UUID,
) (uuid.UUID, error) {
	api, err := oapi.NewClientWithResponses(w.BaseURL, oapi.WithRequestEditorFn(
		func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token(applicationID))
			return nil
		}))
	if err != nil {
		return uuid.Nil, err
	}

	declaration := agmasync.Declaration(agmasync.EntityTypes...)
	res, err := api.PutEndpointWithResponse(ctx, externalID,
		&oapi.PutEndpointParams{XAgrirouterTenantId: w.Tenant},
		oapi.PutEndpointJSONRequestBody{
			ApplicationId:     applicationID,
			SoftwareVersionId: softwareVersionID,
			EndpointType:      endpointType,
			Capabilities:      []oapi.EndpointCapability{},
			Masterdata:        &declaration,
		})
	if err != nil {
		return uuid.Nil, fmt.Errorf("onboarding %s: %w", externalID, err)
	}
	// 201 rather than 200: an endpoint a scenario joins is new, and a 200 would
	// mean it had joined this world twice under one identifier.
	if res.JSON201 == nil {
		return uuid.Nil, fmt.Errorf("onboarding %s: HTTP %d, want 201",
			externalID, res.HTTPResponse.StatusCode)
	}
	return res.JSON201.Id, nil
}

// Contributor onboards a participant that is already synchronizing: opted in,
// and past its initial load. It is what a scenario uses for the participants
// whose data the story is about rather than whose behaviour it is about.
func (w *World) Contributor(
	ctx context.Context, name, appID, externalID string, types ...agmasync.EntityType,
) (*Platform, error) {
	p, err := w.Join(ctx, name, appID, externalID)
	if err != nil {
		return nil, err
	}
	if err := p.OptIn(ctx, types...); err != nil {
		return nil, err
	}
	// An endpoint joining an empty tenant still runs a load: the set is empty,
	// there is nothing to reconcile, and it completes having offered whatever it
	// held. Skipping it would leave the endpoint stuck before RECONCILING.
	if _, err := p.Load(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// Deliveries opens the live stream from the platform's stored position and
// collects what is waiting, without applying any of it.
//
// It is not how a participant works — see [psync.Receiver] for that — but a
// scenario that wants to show what a frame carries, or to apply one by hand,
// needs the frames themselves.
func (p *Platform) Deliveries(ctx context.Context) ([]agmasync.Event, error) {
	from, err := p.Store.Position()
	if err != nil {
		return nil, err
	}
	stream, err := p.Client.Events(ctx, from)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()

	var out []agmasync.Event
	for ev, err := range stream.Events() {
		if err != nil {
			return nil, err
		}
		if ev.Type == agmasync.EventCaughtUp {
			return out, nil
		}
		if ev.HasEntity() {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Transitions lists the initial-load transitions the router recorded for an
// endpoint, each with the side that drove it. The two sides drive two each,
// which is the shape of the state machine.
func (w *World) Transitions(ctx context.Context, p *Platform) ([]string, error) {
	observations, err := w.Observations(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, obs := range observations {
		if obs.Kind != "initialLoadState" || obs.Detail["externalEndpointId"] != p.ExternalID {
			continue
		}
		out = append(out, fmt.Sprintf("%v (%v)", obs.Detail["state"], obs.Detail["driver"]))
	}
	return out, nil
}

// OptIn is the user's decision in agrirouter, which the test router's control
// plane stands in for. There is no participant-facing write for it.
//
// Opting in is what starts an initial load, and opting into a further type
// starts it over: the set is fixed when a load begins.
func (p *Platform) OptIn(ctx context.Context, types ...agmasync.EntityType) error {
	names := make([]string, 0, len(types))
	for _, t := range types {
		names = append(names, string(t))
	}
	return p.world.control.setOptIn(ctx, p.ExternalID, names)
}

// OptOut turns every entity type off, as a user withdrawing from master-data
// exchange does. The identifier mapping survives it, which is what makes a later
// opt-in a repeat rather than a first connection.
func (p *Platform) OptOut(ctx context.Context) error {
	return p.world.control.setOptIn(ctx, p.ExternalID, []string{})
}

// Edit is a change made by a user in the platform's own software, with no
// involvement from the exchange. Keys are the protocol's attribute names.
//
// An identifier the platform does not hold creates the record, which is how the
// scenarios give a participant data of its own.
func (p *Platform) Edit(
	typ agmasync.EntityType, localID string, changes map[string]any,
) error {
	return p.Store.Tx(p.Applier.Tenant, func(tx *store.Tx) error {
		record, err := tx.LoadRecord(typ, localID)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			record = store.Record{
				EntityType: typ,
				LocalID:    localID,
				Modelled:   map[string]json.RawMessage{},
				Unmodelled: map[string]json.RawMessage{},
			}
		}
		for key, value := range changes {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			record.Modelled[key] = raw
		}
		return tx.UpsertRecord(record, localID)
	})
}

// AddFarm creates a farm in the platform's own tables.
func (p *Platform) AddFarm(localID, name, city string) error {
	p.ids.reserve(localID)
	return p.Edit(agmasync.TypeFarm, localID, map[string]any{
		"name":    name,
		"address": map[string]string{"city": city},
	})
}

// AddField creates a field on one of the platform's own farms.
func (p *Platform) AddField(localID, name string, area float64, farmLocalID string) error {
	p.ids.reserve(localID)
	return p.Edit(agmasync.TypeField, localID, map[string]any{
		"name": name,
		"area": area,
		"farm": map[string]string{"local_id": farmLocalID},
	})
}

// Send offers one of the platform's records to agrirouter and applies the
// result.
func (p *Platform) Send(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (psync.Outcome, error) {
	return p.Applier.Send(ctx, typ, localID)
}

// Deactivate tells agrirouter the record was deactivated in this platform.
func (p *Platform) Deactivate(
	ctx context.Context, typ agmasync.EntityType, localID string,
) (psync.Outcome, error) {
	return p.Applier.Deactivate(ctx, typ, localID)
}

// Unbind declares that the platform no longer holds a canonical object.
func (p *Platform) Unbind(
	ctx context.Context, typ agmasync.EntityType, localID string,
) error {
	return p.Applier.Unbind(ctx, typ, localID)
}

// Delete removes a record from the platform's own tables, as its user does.
// agrirouter is not told by this; see [Platform.Unbind].
func (p *Platform) Delete(typ agmasync.EntityType, localID string) error {
	return p.Store.Tx(p.Applier.Tenant, func(tx *store.Tx) error {
		return tx.DeleteRecord(typ, localID)
	})
}

// Load drives the endpoint's initial load to completion, with the sample's own
// recognition step behind it.
//
// It starts by learning what the user selected, which is the user's decision and
// not the participant's: its own declaration is a superset and would have it
// loading types nobody chose.
func (p *Platform) Load(ctx context.Context) (psync.LoadResult, error) {
	types, err := p.selectedTypes(ctx)
	if err != nil {
		return psync.LoadResult{}, err
	}
	loader := &psync.Loader{Applier: p.Applier, Reconciler: psync.ByName{}, Types: types}
	return loader.Run(ctx)
}

// selectedTypes learns this endpoint's selection off the live stream.
//
// There is nothing to read: the selection reaches a participant on the
// ROUTE_CHANGED frame and nowhere else. Connecting is what produces the answer,
// catch-up restating the selection of every endpoint whose selection changed
// above the participant's position — which, for one that has never heard it, is
// every endpoint that ever took part. A participant that missed the move live
// therefore still learns it here, and one whose user withdrew while it was away
// is told so by a frame carrying an empty selection rather than by silence.
//
// The frames are read without being applied and the stored position is left
// alone, as [Platform.Deliveries] does: this answers a question about
// configuration, and the entities waiting are the receive loop's business.
//
// An empty result means the user selected nothing and there is nothing to load.
func (p *Platform) selectedTypes(ctx context.Context) ([]agmasync.EntityType, error) {
	from, err := p.Store.Position()
	if err != nil {
		return nil, err
	}
	stream, err := p.Client.Events(ctx, from)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()

	var types []agmasync.EntityType
	for ev, err := range stream.Events() {
		if err != nil {
			return nil, err
		}
		if ev.Type == agmasync.EventCaughtUp {
			break
		}
		// The last frame naming this endpoint wins: each states the selection
		// in full, so a later one replaces an earlier rather than adding to it.
		if ev.Selection != nil && ev.Selection.ExternalId == p.ExternalID {
			types = agmasync.SelectedTypes(*ev.Selection)
		}
	}
	return types, nil
}

// CatchUp applies the backlog on the live stream and returns when agrirouter
// says there is none left.
func (p *Platform) CatchUp(ctx context.Context) (psync.ReceiveResult, error) {
	return p.Receiver.CatchUp(ctx)
}

// Status reads the endpoint's initial-load status.
func (p *Platform) Status(ctx context.Context) (oapi.InitialLoadStatus, error) {
	return p.Applier.Endpoint.InitialLoadStatus(ctx)
}

// Row reads what the platform knows about one record's place in the exchange:
// the canonical identifier it is bound to, and the revision it holds.
func (p *Platform) Row(typ agmasync.EntityType, localID string) (store.SyncRow, error) {
	var row store.SyncRow
	err := p.Store.Tx(p.Applier.Tenant, func(tx *store.Tx) error {
		var err error
		row, err = tx.SyncRow(typ, localID)
		return err
	})
	return row, err
}

// Record reads one of the platform's own records.
func (p *Platform) Record(typ agmasync.EntityType, localID string) (store.Record, error) {
	var record store.Record
	err := p.Store.Tx(p.Applier.Tenant, func(tx *store.Tx) error {
		var err error
		record, err = tx.LoadRecord(typ, localID)
		return err
	})
	return record, err
}

// Attr reads one modelled attribute of a record as a string, for narration. A
// missing attribute reads as empty.
func (p *Platform) Attr(typ agmasync.EntityType, localID, key string) string {
	record, err := p.Record(typ, localID)
	if err != nil {
		return ""
	}
	var out string
	if raw, ok := record.Modelled[key]; ok {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

// LocalIDs lists every record of one type the platform holds.
func (p *Platform) LocalIDs(typ agmasync.EntityType) ([]string, error) {
	var ids []string
	err := p.Store.Tx(p.Applier.Tenant, func(tx *store.Tx) error {
		var err error
		ids, err = tx.LocalIDs(typ)
		return err
	})
	return ids, err
}

// Position is where the platform's live stream would resume from.
func (p *Platform) Position() (string, error) { return p.Store.Position() }

// Bind tells agrirouter what this platform calls a canonical object.
func (p *Platform) Bind(
	ctx context.Context, typ agmasync.EntityType, localID string, agrirouterID uuid.UUID,
) error {
	return p.Applier.Endpoint.Bind(ctx, typ, localID, agrirouterID)
}

// Adopt records that one of the platform's own records is a canonical object,
// at both ends: agrirouter is told, and the platform writes the pair into its
// own mapping.
//
// It is what a resolved reconciliation comes down to. Where the platform could
// not tell which of its records a canonical object was, nothing bound it — so
// once a person has said which, somebody has to write down the answer, and both
// sides need it. agrirouter first, as everywhere else: a pair the platform holds
// and agrirouter does not is one its next send resolves through and finds
// nothing.
func (p *Platform) Adopt(
	ctx context.Context, typ agmasync.EntityType, localID string, agrirouterID uuid.UUID,
) error {
	if err := p.Bind(ctx, typ, localID, agrirouterID); err != nil {
		return err
	}
	return p.Store.Tx(p.Applier.Tenant, func(tx *store.Tx) error {
		return tx.PutSyncRow(store.SyncRow{
			EntityType: typ, LocalID: localID, AgrirouterID: &agrirouterID,
		})
	})
}

// short abbreviates a canonical identifier for narration. Nothing may be read
// out of one, so only enough of it is printed to tell two apart.
func short(id uuid.UUID) string { return id.String()[:8] }

// --- the control plane ---------------------------------------------------
//
// These stand in for the parts of agrirouter that are not participant-facing:
// creating a tenant, and the user's opt-in. A participant never calls them, and
// they are not in openapi.yaml.
//
// Creating an endpoint is deliberately not among them — see [World.onboard].

type control struct {
	baseURL string
	http    *http.Client
}

func (c *control) createTenant(ctx context.Context) (uuid.UUID, error) {
	var out testrouter.TenantResponse
	if err := c.post(ctx, "/_test/tenants", nil, &out); err != nil {
		return uuid.Nil, err
	}
	return out.TenantID, nil
}

func (c *control) setOptIn(ctx context.Context, externalID string, collections []string) error {
	path := "/_test/endpoints/" + url.PathEscape(externalID) + "/opt-in"
	return c.do(ctx, http.MethodPut, path,
		testrouter.OptInRequest{EntityTypes: collections}, nil)
}

// Observations returns what the router saw, which is how a scenario checks what
// a participant sent rather than only what it ended up holding.
func (w *World) Observations(ctx context.Context) ([]testrouter.Observation, error) {
	var out []testrouter.Observation
	err := w.control.do(ctx, http.MethodGet, "/_test/observations", nil, &out)
	return out, err
}

func (c *control) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *control) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("control plane %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("control plane %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
