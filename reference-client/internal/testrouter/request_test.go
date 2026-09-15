package testrouter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// liveFrames opens the application's event stream, drains the catch-up that
// precedes the live end, and returns what catch-up carried along with a channel
// holding everything delivered after it.
//
// The split matters for a request: what catch-up holds is what the participant
// would have been given anyway, so only what arrives on the channel is
// attributable to having asked.
func liveFrames(t *testing.T, f *fixture, appID string) ([]agmasync.Event, <-chan agmasync.Event) {
	t.Helper()

	client, err := agmasync.NewClient(f.server.URL, agmasync.WithBearerToken(appID))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	stream, err := client.Events(ctx, "")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// One reader for the whole stream, with the frames that say anything here
	// passed out on a channel: the end of catch-up has to be waited for before
	// the request is made, and the delivery it produces waited for after it.
	frames := make(chan agmasync.Event, 16)
	go func() {
		defer close(frames)
		for ev, err := range stream.Events() {
			if err != nil {
				return
			}
			if ev.HasEntity() || ev.Type == agmasync.EventCaughtUp {
				frames <- ev
			}
		}
	}()

	var caught []agmasync.Event
	for {
		select {
		case ev, ok := <-frames:
			if !ok {
				t.Fatal("the stream ended before catch-up finished")
			}
			if ev.Type == agmasync.EventCaughtUp {
				return caught, frames
			}
			caught = append(caught, ev)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for the end of catch-up")
		}
	}
}

func TestARequestedObjectIsDeliveredEvenToItsOwnWriter(t *testing.T) {
	// The property the operation exists for. Origin suppression withholds from a
	// participant the objects whose current revision it wrote itself, on the
	// grounds that it already holds them — but a request states the opposite, so
	// it is delivered. Without this an endpoint that lost an object it had
	// written could never get it back short of taking the whole canonical set.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	created, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, err := agmasync.EnvelopeOf(created)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}

	// Catch-up is where suppression shows: the writer is told nothing about its
	// own object, which is what leaves it no way back to it but asking.
	caught, live := liveFrames(t, f, "fmis-a")
	if len(caught) != 0 {
		t.Fatalf("catch-up carried %d objects, want the writer's own suppressed", len(caught))
	}

	if err := p.endpoint.Request(
		context.Background(), agmasync.TypeFarm, *env.AgrirouterId,
	); err != nil {
		t.Fatalf("requesting an object this endpoint wrote: %v", err)
	}

	select {
	case ev := <-live:
		if ev.Envelope.AgrirouterId == nil || *ev.Envelope.AgrirouterId != *env.AgrirouterId {
			t.Errorf("delivered %v, want the requested object %s",
				ev.Envelope.AgrirouterId, *env.AgrirouterId)
		}
		if ev.Type != agmasync.EventMasterdataChanged {
			t.Errorf("frame type = %q, want an ordinary change frame", ev.Type)
		}
		// Delivered as the participant's own, so it lands on the record it
		// already has rather than arriving as something new to bind.
		if ev.Envelope.LocalId == nil || *ev.Envelope.LocalId != "FRM-1" {
			t.Errorf("localId = %v, want the requester's own", ev.Envelope.LocalId)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out: the requested object was never delivered")
	}
}

func TestARequestForAnObjectTheEndpointIsNotEntitledToIsRefused(t *testing.T) {
	// Asking does not widen what a participant may see. Opt-in is the only filter
	// on delivery, and a request goes through it like anything else — otherwise
	// the operation would be a way around the user's own selection.
	f := newFixture(t)
	owner := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	created, err := owner.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, err := agmasync.EnvelopeOf(created)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}

	// A second participant in the same tenancy that the user has selected nothing
	// for. The object is there and it is the right type — only the entitlement is
	// missing.
	outsider := f.join("fmis-b", "ep-b")

	err = outsider.endpoint.Request(context.Background(), agmasync.TypeFarm, *env.AgrirouterId)
	if !errors.Is(err, agmasync.ErrForbidden) {
		t.Errorf("request = %v, want it refused as forbidden", err)
	}
}

func TestARequestIsAnsweredOnTheNamedTypeAlone(t *testing.T) {
	// The request endpoints are per entity type, so the identifier is not the
	// whole of what is asked: the pair has to hold. An identifier resolved
	// without its type would let a participant fetch an object of a type it never
	// asked for through whichever endpoint it happened to call, and would answer
	// a typed slot's dereference with the wrong entity.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	created, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, err := agmasync.EnvelopeOf(created)
	if err != nil {
		t.Fatalf("reading envelope: %v", err)
	}

	// Opting into farms pulls in the parties farms reference, so this endpoint is
	// entitled to organizations too. What refuses the request is the type of the
	// object behind the identifier, not the endpoint's selection.
	err = p.endpoint.Request(context.Background(), agmasync.TypeOrganization, *env.AgrirouterId)
	if !errors.Is(err, agmasync.ErrNotFound) {
		t.Errorf("request on the wrong type = %v, want not found", err)
	}

	// And an identifier of no object at all.
	err = p.endpoint.Request(context.Background(), agmasync.TypeFarm, uuid.New())
	if !errors.Is(err, agmasync.ErrNotFound) {
		t.Errorf("request for an unknown object = %v, want not found", err)
	}
}
