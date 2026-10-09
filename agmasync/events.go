package agmasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"net/http"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
	"github.com/tmaxmax/go-sse"
)

// Event types carried on the streams.
const (
	// EventMasterdataChanged carries a canonical object.
	EventMasterdataChanged = "MASTERDATA_CHANGED"

	// EventMasterdataDeactivated carries a canonical object that has become
	// inactive. It is a lifecycle transition, not a removal: the object and
	// its identifier mapping are retained.
	EventMasterdataDeactivated = "MASTERDATA_DEACTIVATED"

	// EventCaughtUp marks the end of catch-up on the live stream. It carries
	// no entity and names no entity type: it covers the catch-up as a whole.
	EventCaughtUp = "CAUGHT_UP"

	// EventRouteChanged states which entity types the user has selected on one
	// endpoint. It carries an [oapi.RouteChangedEventData] rather than an
	// entity.
	EventRouteChanged = "ROUTE_CHANGED"

	// EventMasterdataReset states that the user wiped one tenant's master data:
	// every canonical object, identifier mapping, route and initial-load state
	// there is gone. It carries an [oapi.MasterdataResetEventData] rather than
	// an entity, and stands for an empty selection on every endpoint it lists.
	EventMasterdataReset = "RESET_MASTERDATA_SYNC"
)

// Event is one frame of a master-data stream.
type Event struct {
	// Type is one of the event constants above. It is a plain string because
	// a participant must tolerate a frame type it does not know rather than
	// fail on it.
	Type string

	// ID is the delivery position as of this frame, and is opaque.
	//
	// A participant MUST NOT interpret, compare, construct, or modify it: its
	// structure is undefined and may change between implementations and
	// versions. It does not necessarily advance on every frame — the same
	// value repeats across consecutive frames throughout catch-up — and
	// nothing may be read into that beyond the position being unchanged.
	//
	// Whatever ID a frame carries is safe to resume from once that frame and
	// every frame before it have been durably applied. The initial-load stream
	// carries no position at all, so this is empty there.
	ID string

	// Object is the canonical object the frame carries. It is the zero value
	// on a frame that carries none, such as EventCaughtUp.
	Object

	// RouteChange is set on an [EventRouteChanged] frame and nil on every other.
	// It is the one frame on the stream that carries something other than an
	// entity, and what it carries is the endpoint's whole selection as it
	// stands after the change.
	RouteChange *oapi.RouteChangedEventData

	// Reset is set on an [EventMasterdataReset] frame and nil on every other.
	// A participant receiving it discards every binding it holds in the tenant
	// and keeps its local records, and durably stores this frame's position
	// before taking part in that tenant's initial load again: a resume from
	// before it would deliver the reset once more, after the new bindings exist.
	Reset *oapi.MasterdataResetEventData
}

// HasEntity reports whether the frame carries a canonical object.
func (e Event) HasEntity() bool { return e.Envelope.Type != "" }

// Stream is an open master-data event stream.
//
// Close it when done; the underlying connection stays open until then.
type Stream struct {
	resp *http.Response

	// positioned is false for the initial-load stream, which delivers a fixed
	// set rather than a sequence of changes and therefore carries no position.
	positioned bool

	// MaxEventSize bounds one frame, in bytes; a larger frame ends the
	// iteration with an error. Zero means [DefaultMaxEventSize].
	MaxEventSize int
}

// Close releases the stream's connection.
func (s *Stream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	return s.resp.Body.Close()
}

// Events iterates the stream's frames until it ends, the context is canceled,
// or a frame cannot be decoded.
//
// The iteration ending is not by itself proof of anything. On the live stream
// it means the connection dropped and should be re-established from the last
// durably applied position. On an initial-load stream it does not prove the
// canonical set arrived — a dropped connection ends the response exactly as an
// orderly completion does — so the endpoint's initial-load state is what has to
// be consulted; see [GetInitialLoadStatus].
func (s *Stream) Events() iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		// The response is parsed with sse.Read rather than consumed through an
		// sse.Client, as the SDK's other event streams are, because an
		// sse.Client reconnects on its own and resumes from the id of the last
		// frame it read. On the live stream the resume position must be the last
		// frame the participant has durably applied, which only the participant
		// knows, so a dropped connection ends the iteration instead and the
		// participant reconnects with its own position. The initial-load stream
		// carries no position at all and is restarted from the beginning.
		maxEventSize := s.MaxEventSize
		if maxEventSize <= 0 {
			maxEventSize = DefaultMaxEventSize
		}
		for raw, err := range sse.Read(s.resp.Body, &sse.ReadConfig{MaxEventSize: maxEventSize}) {
			if err != nil {
				if errIsStreamEnd(err) {
					return
				}
				yield(Event{}, fmt.Errorf("agmasync: reading event stream: %w", err))
				return
			}

			ev := Event{Type: raw.Type}
			if s.positioned {
				ev.ID = raw.LastEventID
			}

			switch {
			case raw.Data == "":

			case raw.Type == EventMasterdataChanged || raw.Type == EventMasterdataDeactivated:
				obj, err := ObjectOf([]byte(raw.Data))
				if err != nil {
					yield(Event{}, fmt.Errorf("agmasync: decoding %s frame: %w", raw.Type, err))
					return
				}
				ev.Object = obj

			case raw.Type == EventRouteChanged:
				var selection oapi.RouteChangedEventData
				if err := json.Unmarshal([]byte(raw.Data), &selection); err != nil {
					yield(Event{}, fmt.Errorf("agmasync: decoding %s frame: %w", raw.Type, err))
					return
				}
				ev.RouteChange = &selection

			case raw.Type == EventMasterdataReset:
				var reset oapi.MasterdataResetEventData
				if err := json.Unmarshal([]byte(raw.Data), &reset); err != nil {
					yield(Event{}, fmt.Errorf("agmasync: decoding %s frame: %w", raw.Type, err))
					return
				}
				ev.Reset = &reset
			}

			if !yield(ev, nil) {
				return
			}
		}
	}
}

// DefaultMaxEventSize is the frame bound a [Stream] applies unless told
// otherwise.
const DefaultMaxEventSize = 16 << 20

func errIsStreamEnd(err error) bool {
	return errors.Is(err, io.EOF)
}

// Events opens the application's live master-data stream.
//
// The stream is application-scoped and carries every tenant the application is
// routed to and every entity type it is opted into, so a receiver holding data
// for several tenants partitions on each object's tenantId rather than on the
// connection. agrirouter never echoes a change back to the endpoint it came
// from, though it does deliver it to the application's other endpoints.
//
// lastEventID resumes from a previous position and MUST be the id of a frame
// the participant has durably applied — not whatever its stream client last
// read, delivery being at-least-once. A participant applying frames in parallel
// resends the id of the earliest frame, in the order the stream delivered them,
// that it has not yet applied.
//
// An empty lastEventID is served as a first connection: agrirouter delivers
// everything the application is entitled to.
//
// Positions do not expire. agrirouter serves catch-up from the current state of
// each entity rather than from a retained log, so a long absence is a larger
// catch-up rather than a failed one — and, for the same reason, a participant
// receives each changed entity once carrying its current value and MUST NOT
// assume it observed every intermediate change. Catch-up ends with an
// [EventCaughtUp] frame.
func Events(ctx context.Context, api *oapi.ClientWithResponses, lastEventID string) (*Stream, error) {
	var params oapi.StreamMasterdataEventsParams
	if lastEventID != "" {
		// Passed back exactly as agrirouter issued it in the frame's id field.
		params.LastEventID = &lastEventID
	}
	resp, err := api.StreamMasterdataEvents(ctx, &params, acceptEventStream)
	if err != nil {
		return nil, fmt.Errorf("agmasync: opening stream: %w", err)
	}
	return openStream(resp, true)
}

// InitialLoadEvents opens this endpoint's initial-load stream and collects the
// canonical set it is owed.
//
// The set is every object of every entity type the endpoint is entitled to.
//
// Order is agrirouter's. A referenced object precedes the objects that
// reference it, and opt-in is dependency-closed, so every reference to a
// selected type resolves as objects arrive and each can be applied on arrival.
// A reference to a type not selected is ignored (ADR 13). That is the only property
// of the order to rely on: a participant MUST NOT depend on the position of one
// entity type relative to another, or read completeness of a type out of it.
//
// The stream carries no position and takes no Last-Event-ID. A connection that
// drops before the set is complete is recovered by connecting again and taking
// the set from the beginning.
func InitialLoadEvents(
	ctx context.Context, api *oapi.ClientWithResponses,
	externalEndpointID string, tenantID uuid.UUID,
) (*Stream, error) {
	resp, err := api.StreamInitialLoadEvents(ctx, externalEndpointID,
		&oapi.StreamInitialLoadEventsParams{XAgrirouterTenantId: tenantID},
		acceptEventStream)
	if err != nil {
		return nil, fmt.Errorf("agmasync: opening stream: %w", err)
	}
	return openStream(resp, false)
}

// acceptEventStream asks the two stream operations for the media type they
// answer in, and says the response must not be cached.
var acceptEventStream oapi.RequestEditorFn = func(
	_ context.Context, req *http.Request,
) error {
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-store")
	return nil
}

// openStream turns the response of one of the generated stream operations into
// a [Stream].
//
// Its callers reach for the generated low-level method rather than the
// WithResponse wrapper the rest of this package uses: the wrapper reads the
// whole body and closes it, which for a stream means waiting for agrirouter to
// finish, buffering everything, and handing back nothing that can be read frame
// by frame.
//
// positioned says whether the stream carries a delivery position, which the
// live stream does and the initial-load stream does not.
func openStream(resp *http.Response, positioned bool) (*Stream, error) {
	const maxErrorBody = 4096
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		return nil, writeResult{statusCode: resp.StatusCode, body: body}.err()
	}
	// A 200 in another media type is not a stream and parsed as one it would yield no
	// frames and no error.
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "text/event-stream" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		_ = resp.Body.Close()
		return nil, &APIError{resp.StatusCode, fmt.Sprintf("stream answered as %q, not text/event-stream: %s",
			resp.Header.Get("Content-Type"), unexpected(body)), ErrNotEventStream}
	}
	return &Stream{resp: resp, positioned: positioned}, nil
}
