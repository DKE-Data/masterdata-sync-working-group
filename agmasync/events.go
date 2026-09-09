package agmasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
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

	// Entity is the canonical object the frame carries. It is the zero value
	// on a frame that carries none, such as EventCaughtUp.
	Entity oapi.Entity

	// Envelope holds the common fields of Entity, already decoded. A receiver
	// needs the type and the revision before it can decide what to do with the
	// object, and every frame on the live stream may be any of the five types.
	Envelope Envelope
}

// HasEntity reports whether the frame carries a canonical object.
func (e Event) HasEntity() bool { return e.Type != EventCaughtUp && e.Envelope.Type != "" }

// Stream is an open master-data event stream.
//
// Close it when done; the underlying connection stays open until then.
type Stream struct {
	resp *http.Response

	// positioned is false for the initial-load stream, which delivers a fixed
	// set rather than a sequence of changes and therefore carries no position.
	positioned bool
}

// Close releases the stream's connection.
func (s *Stream) Close() error {
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	return s.resp.Body.Close()
}

// Events iterates the stream's frames until it ends, the context is cancelled,
// or a frame cannot be decoded.
//
// The iteration ending is not by itself proof of anything. On the live stream
// it means the connection dropped and should be re-established from the last
// durably applied position. On an initial-load stream it does not prove the
// canonical set arrived — a dropped connection ends the response exactly as an
// orderly completion does — so the endpoint's initial-load state is what has to
// be consulted; see [Endpoint.InitialLoadStatus].
func (s *Stream) Events() iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		for raw, err := range sse.Read(s.resp.Body, nil) {
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

			if raw.Type != EventCaughtUp && raw.Data != "" {
				if err := json.Unmarshal([]byte(raw.Data), &ev.Entity); err != nil {
					yield(Event{}, fmt.Errorf("agmasync: decoding %s frame: %w", raw.Type, err))
					return
				}
				env, err := EnvelopeOf(ev.Entity)
				if err != nil {
					yield(Event{}, err)
					return
				}
				ev.Envelope = env
			}

			if !yield(ev, nil) {
				return
			}
		}
	}
}

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
// everything the application is entitled to. That is the widest way to ask for
// data again, so a participant that needs less should ask for one endpoint's
// canonical set or request objects individually instead.
//
// Positions do not expire. agrirouter serves catch-up from the current state of
// each entity rather than from a retained log, so a long absence is a larger
// catch-up rather than a failed one — and, for the same reason, a participant
// receives each changed entity once carrying its current value and MUST NOT
// assume it observed every intermediate change. Catch-up ends with an
// [EventCaughtUp] frame.
func (c *Client) Events(ctx context.Context, lastEventID string) (*Stream, error) {
	u, err := url.JoinPath(c.baseURL, "masterdata", "events")
	if err != nil {
		return nil, fmt.Errorf("agmasync: building stream URL: %w", err)
	}

	header := http.Header{}
	if lastEventID != "" {
		// Passed back exactly as agrirouter issued it in the frame's id field.
		header.Set("Last-Event-ID", lastEventID)
	}
	return c.openStream(ctx, u, header, true)
}

// InitialLoadEvents opens this endpoint's initial-load stream and collects the
// canonical set it is owed.
//
// The set is every object of every entity type the endpoint is opted into that
// it is entitled to, and it is complete in two ways participants get wrong.
// Objects that are inactive are part of it, carrying active false, because
// omitting them would have an endpoint taking the set report its own copy as
// missing from the SSOT and so resurrect something a user archived. Objects whose
// current revision this participant itself wrote are part of it too — origin
// suppression does not apply here, since an endpoint taking the set has
// declared that it does not know what it holds.
//
// Order is agrirouter's. A referenced object precedes the objects that
// reference it, and opt-in is dependency-closed, so every reference resolves as
// objects arrive and each can be applied on arrival. That is the only property
// of the order to rely on: a participant MUST NOT depend on the position of one
// entity type relative to another, or read completeness of a type out of it.
//
// The stream carries no position and takes no Last-Event-ID. A connection that
// drops before the set is complete is recovered by connecting again and taking
// the set from the beginning.
func (e *Endpoint) InitialLoadEvents(ctx context.Context) (*Stream, error) {
	u, err := url.JoinPath(
		e.client.baseURL, "endpoints", e.externalID, "masterdata-initial-load", "events")
	if err != nil {
		return nil, fmt.Errorf("agmasync: building stream URL: %w", err)
	}
	return e.client.openStream(ctx, u, http.Header{}, false)
}

func (c *Client) openStream(
	ctx context.Context, u string, header http.Header, positioned bool,
) (*Stream, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("agmasync: building stream request: %w", err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-store")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agmasync: opening stream: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, writeResult{statusCode: resp.StatusCode, body: body}.err()
	}
	return &Stream{resp: resp, positioned: positioned}, nil
}
