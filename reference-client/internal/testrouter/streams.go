package testrouter

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

const (
	eventMasterdataChanged     = "MASTERDATA_CHANGED"
	eventMasterdataDeactivated = "MASTERDATA_DEACTIVATED"
	eventCaughtUp              = "CAUGHT_UP"

	// eventRouteChanged states which entity types the user has selected on one
	// endpoint. One frame covers every way the selection moves: routed to the
	// hub, a type selected, a type deselected, and the last one deselected.
	eventRouteChanged = "ROUTE_CHANGED"
)

// routeChangedFrame renders a ROUTE_CHANGED frame stating what one endpoint
// exchanges. Call it under the store lock.
//
// The endpoint is named by both of its identifiers: the participant needs
// whichever it is about to use, and the configuration and initial-load resources
// are addressed by the external one. The selection travels on the frame itself,
// stated in full rather than as a delta, so a participant replaces what it held
// for this endpoint with it and has nothing to go and read.
//
// An empty entityTypes is rendered rather than the frame being withheld: it is a
// statement and not an omission, saying the endpoint exchanges nothing because
// the user deselected the last type or the route was removed.
//
// One endpoint, because a selection belongs to one: the user makes it on that
// endpoint's route to the hub, so every way it moves moves exactly one.
//
// It takes a change number of its own rather than repeating the one before it.
// The selection is state a participant applies, and catch-up delivers it above
// the participant's position exactly as it delivers a changed object, which it
// could not do if the change were not numbered.
func routeChangedFrame(ep *endpoint, id string) frame {
	data, _ := json.Marshal(map[string]any{
		// eventType repeats the event: line in band, as the other agrirouter
		// event streams do, so a frame stays self-describing once it is off the
		// wire. The entity frames carry no such attribute: what they hold is a
		// canonical object, discriminated by its own type.
		"eventType":          eventRouteChanged,
		"endpointId":         ep.id,
		"externalEndpointId": ep.externalID,
		"entityTypes":        selectedTogglesLocked(ep),
		"changedAt":          ep.selectionChangedAt,
	})
	return frame{event: eventRouteChanged, id: id, entity: data}
}

// frame is one SSE frame waiting to go out.
type frame struct {
	event  string
	id     string
	entity json.RawMessage
}

func (f frame) encode() string {
	var b strings.Builder
	if f.id != "" {
		fmt.Fprintf(&b, "id: %s\n", f.id)
	}
	fmt.Fprintf(&b, "event: %s\n", f.event)
	if len(f.entity) > 0 {
		fmt.Fprintf(&b, "data: %s\n", f.entity)
	}
	b.WriteString("\n")
	return b.String()
}

// encodePosition turns a sequence number into the opaque event id a participant
// hands back as Last-Event-ID.
//
// The structure is deliberately not something a participant can read: the
// specification says an id must not be interpreted, compared, constructed, or
// modified, so this encodes and versions it rather than emitting the number. A
// participant that tries to sort or increment these will get nowhere, which is
// the point of testing against them.
func encodePosition(seq uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], seq^0x5a5a5a5a5a5a5a5a)
	return "v1." + base64.RawURLEncoding.EncodeToString(buf[:])
}

// decodePosition reads a position back. Only agrirouter does this.
func decodePosition(id string) (uint64, bool) {
	raw, ok := strings.CutPrefix(id, "v1.")
	if !ok {
		return 0, false
	}
	buf, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(buf) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(buf) ^ 0x5a5a5a5a5a5a5a5a, true
}

// hub fans live changes out to the connected applications.
//
// Subscription is per application rather than per endpoint, because the live
// stream is: one connection carries every tenant the application is routed to
// and every endpoint it holds there.
type hub struct {
	mu   sync.Mutex
	subs map[string]map[chan frame]struct{}
}

func newHub() *hub {
	return &hub{subs: map[string]map[chan frame]struct{}{}}
}

func (h *hub) subscribe(appID string) chan frame {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan frame, 256)
	if h.subs[appID] == nil {
		h.subs[appID] = map[chan frame]struct{}{}
	}
	h.subs[appID][ch] = struct{}{}
	return ch
}

func (h *hub) unsubscribe(appID string, ch chan frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.subs[appID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(h.subs, appID)
		}
	}
	close(ch)
}

func (h *hub) publish(appID string, f frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[appID] {
		select {
		case ch <- f:
		default:
			// A participant that cannot keep up is dropped rather than allowed
			// to stall the hub. It resumes from its last applied position,
			// which is exactly what the position exists for.
		}
	}
}

// liveStream serves GET /masterdata/events: catch-up from the participant's
// position, a CAUGHT_UP frame, then live changes until the client goes away.
func (r *Router) liveStream(ctx context.Context, appID, lastEventID string) io.Reader {
	pipeR, pipeW := io.Pipe()
	ch := r.hub.subscribe(appID)

	// Subscribing before catch-up is read means a change landing mid-catch-up
	// is queued rather than lost. It may then be delivered twice, once in
	// catch-up and once live, which is harmless: applying is idempotent and
	// guarded by revision.
	catchUp := r.catchUp(appID, lastEventID)

	go func() {
		defer r.hub.unsubscribe(appID, ch)
		defer pipeW.Close()

		for _, f := range catchUp {
			if ctx.Err() != nil {
				return
			}
			if _, err := io.WriteString(pipeW, f.encode()); err != nil {
				return
			}
		}
		for {
			// A client that goes away leaves nobody reading the pipe, so a
			// blocked write would hold this goroutine — and the server's
			// shutdown with it — forever. The request context ending is what
			// says the connection is gone.
			select {
			case <-ctx.Done():
				return
			case f, ok := <-ch:
				if !ok {
					return
				}
				if _, err := io.WriteString(pipeW, f.encode()); err != nil {
					return
				}
			}
		}
	}()

	return pipeR
}

// catchUp collects everything the application is entitled to that changed after
// its position, ordered so that a referenced object precedes the objects
// referencing it, and ends with CAUGHT_UP.
//
// A participant that sends no position, or one agrirouter cannot validate, is
// served as a first connection: everything it is entitled to.
func (r *Router) catchUp(appID, lastEventID string) []frame {
	var from uint64
	if lastEventID != "" {
		if pos, ok := decodePosition(lastEventID); ok {
			from = pos
		}
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	frames := []frame{}

	// Catch-up restates the selection of every endpoint of this application
	// whose selection changed above the participant's position, emptied ones
	// included. The second half is what makes a withdrawal survive a
	// disconnection: an emptied endpoint has no selection left to state, so on
	// the first rule alone it would never be heard from again and a participant
	// that was away when its user withdrew would go on believing it was opted
	// in. What agrirouter retains for that is selectionChangedSeq, which is
	// non-zero exactly for the endpoints that took part at some point.
	//
	// They go out ahead of the objects so that a participant knows what it is
	// opted into before the set for it arrives.
	for _, ep := range r.store.sortedEndpoints() {
		if ep.appID != appID || ep.selectionChangedSeq <= from {
			continue
		}
		frames = append(frames, routeChangedFrame(ep, encodePosition(ep.selectionChangedSeq)))
	}

	for _, typ := range deliveryOrder {
		for _, obj := range r.store.sortedObjects(typ) {
			if obj.seq <= from {
				continue
			}
			// Origin suppression applies here as it does live, which is why a
			// full re-delivery is not a complete recovery: it withholds the
			// objects whose current revision this participant's own endpoint
			// wrote. One frame per object, as live delivery does: entitlement
			// is decided per endpoint, but the frame it produces belongs to
			// the application, so siblings collapse to one.
			for _, ep := range r.store.recipients(obj, obj.sourceEndpointID) {
				if ep.appID != appID {
					continue
				}
				event := eventMasterdataChanged
				if !obj.active {
					event = eventMasterdataDeactivated
				}
				frames = append(frames, frame{
					event:  event,
					id:     encodePosition(obj.seq),
					entity: r.store.renderLocked(obj, ep),
				})
			}
		}
	}

	// CAUGHT_UP covers the catch-up as a whole and names no entity type. It
	// carries the position reached, so a participant that applies everything
	// and then stops has somewhere to resume from.
	frames = append(frames, frame{event: eventCaughtUp, id: encodePosition(r.store.seq)})
	return frames
}

// initialLoadStream serves one endpoint its canonical set and then ends.
//
// The response closing is what marks the set complete, and agrirouter advances
// the endpoint to RECONCILING only after sending everything — which is why the
// state, and not the response ending, is what an endpoint reads to know the set
// arrived.
func (r *Router) initialLoadStream(ctx context.Context, ep *endpoint) io.Reader {
	pipeR, pipeW := io.Pipe()

	r.store.mu.Lock()
	frames := []frame{}
	for _, typ := range deliveryOrder {
		if !ep.optedInto(typ) {
			continue
		}
		for _, obj := range r.store.sortedObjects(typ) {
			if obj.tenantID != ep.tenantID {
				continue
			}
			// The set is complete in two ways participants get wrong. Objects
			// that are inactive are part of it, and so are objects whose
			// current revision this participant itself wrote: origin
			// suppression does not apply here, since an endpoint taking the set
			// has declared that it does not know what it holds.
			frames = append(frames, frame{
				event:  eventMasterdataChanged,
				entity: r.store.renderLocked(obj, ep),
			})
		}
	}
	r.store.mu.Unlock()

	// A drop armed by a test cuts the response short. The state is deliberately
	// left where it is, which is the whole point: the endpoint sees a response
	// end exactly as it does on an orderly completion, and only the state tells
	// the two apart.
	drop := r.takeDrop(ep)

	go func() {
		defer pipeW.Close()
		for i, f := range frames {
			if ctx.Err() != nil {
				return
			}
			if drop && i > 0 {
				return
			}
			if _, err := io.WriteString(pipeW, f.encode()); err != nil {
				return
			}
		}
		if drop {
			return
		}
		// Sent in full, so the endpoint now owes a decision rather than
		// agrirouter owing data.
		r.setLoadState(ep, stateReconciling)
	}()

	return pipeR
}

// sortedObjects returns one type's objects in a stable order, so that two runs
// of the same scenario deliver the same sequence.
//
// Within a type the order is by the position at which each object last changed,
// which keeps a parent created before its child ahead of it even where both are
// the same type — a person who belongs to an organization, for instance.
// sortedEndpoints iterates the endpoints in a stable order, so that two reads of
// the selection collection return them the same way.
func (s *store) sortedEndpoints() []*endpoint {
	out := make([]*endpoint, 0, len(s.endpoints))
	for _, ep := range s.endpoints {
		out = append(out, ep)
	}
	slices.SortFunc(out, func(a, b *endpoint) int {
		return cmp.Compare(a.externalID, b.externalID)
	})
	return out
}

func (s *store) sortedObjects(typ agmasync.EntityType) []*object {
	var out []*object
	for _, obj := range s.objects {
		if obj.typ == typ {
			out = append(out, obj)
		}
	}
	slices.SortFunc(out, func(a, b *object) int {
		return cmp.Compare(a.seq, b.seq)
	})
	return out
}
