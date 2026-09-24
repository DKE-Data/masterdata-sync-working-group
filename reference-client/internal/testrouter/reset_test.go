package testrouter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

// untilCaughtUp reads a stream up to and including its CAUGHT_UP frame.
func untilCaughtUp(t *testing.T, stream *agmasync.Stream) []agmasync.Event {
	t.Helper()
	var out []agmasync.Event
	for ev, err := range stream.Events() {
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		out = append(out, ev)
		if ev.Type == agmasync.EventCaughtUp {
			return out
		}
	}
	t.Fatal("stream ended before CAUGHT_UP")
	return nil
}

func openEvents(t *testing.T, p *participant, from string) *agmasync.Stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	stream, err := p.client.Events(ctx, from)
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

func TestAResetDiscardsTheTenantsDataAndIsAnnounced(t *testing.T) {
	// A reset takes the canonical objects, the mapping and the routes, and the
	// participant is told on its stream. Its writes are refused until the user
	// routes it again, and what it then sends is new: the pair it had is gone.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	completeLoad(t, p)

	created, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before, _ := agmasync.EnvelopeOf(created)

	stream := openEvents(t, p, "")
	untilCaughtUp(t, stream)

	if err := f.router.ResetTenant(f.tenant); err != nil {
		t.Fatalf("reset: %v", err)
	}

	var reset *agmasync.Event
	for ev, err := range stream.Events() {
		if err != nil {
			t.Fatalf("reading stream: %v", err)
		}
		if ev.Type == agmasync.EventRouteChanged {
			t.Fatal("a reset issues no ROUTE_CHANGED of its own")
		}
		if ev.Type == agmasync.EventMasterdataReset {
			reset = &ev
			break
		}
	}
	if reset == nil || reset.Reset == nil {
		t.Fatal("no RESET_MASTERDATA_SYNC frame")
	}
	if reset.Reset.TenantId != f.tenant {
		t.Errorf("tenant = %s, want %s", reset.Reset.TenantId, f.tenant)
	}
	if len(reset.Reset.Endpoints) != 1 || reset.Reset.Endpoints[0].ExternalId != "ep-a" {
		t.Errorf("endpoints = %+v, want ep-a alone", reset.Reset.Endpoints)
	}

	// No route, so no write.
	rev := *before.Revision
	if _, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Süd"), &rev); !errors.Is(err, agmasync.ErrForbidden) {
		t.Errorf("write after reset: err = %v, want forbidden", err)
	}

	// Routed again, the endpoint starts a first load, not a repeat.
	if err := f.router.OptIn("ep-a", agmasync.TypeFarm); err != nil {
		t.Fatalf("routing again: %v", err)
	}
	status, err := p.endpoint.InitialLoadStatus(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if agmasync.IsRepeatLoad(status) {
		t.Error("the load after a reset is marked as a repeat")
	}

	// The set is empty: nothing of the tenant survived.
	load, err := p.endpoint.InitialLoadEvents(context.Background())
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}
	for ev := range load.Events() {
		if ev.HasEntity() {
			t.Errorf("canonical set after reset carries %s", ev.Envelope.Type)
		}
	}
	load.Close()

	// The same localId now mints a new canonical object.
	completeLoadFromReconciling(t, p)
	again, err := p.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create after reset: %v", err)
	}
	after, _ := agmasync.EnvelopeOf(again)
	if *after.AgrirouterId == *before.AgrirouterId {
		t.Error("an agrirouterId was reissued after a reset")
	}
}

func TestAResetSurvivesBeingDisconnectedForIt(t *testing.T) {
	// A participant away when the user reset is told on catch-up, and told
	// before anything that came after — here, the route the user created again.
	// Once it holds a position past the reset, it is not told again.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	first := untilCaughtUp(t, openEvents(t, p, ""))
	position := first[len(first)-1].ID

	if err := f.router.ResetTenant(f.tenant); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := f.router.OptIn("ep-a", agmasync.TypeFarm); err != nil {
		t.Fatalf("routing again: %v", err)
	}

	var order []string
	var afterReset string
	for _, ev := range untilCaughtUp(t, openEvents(t, p, position)) {
		order = append(order, ev.Type)
		if ev.Type == agmasync.EventMasterdataReset {
			afterReset = ev.ID
		}
	}
	want := []string{agmasync.EventMasterdataReset, agmasync.EventRouteChanged, agmasync.EventCaughtUp}
	if len(order) != len(want) {
		t.Fatalf("catch-up = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("catch-up = %v, want %v", order, want)
		}
	}

	for _, ev := range untilCaughtUp(t, openEvents(t, p, afterReset)) {
		if ev.Type == agmasync.EventMasterdataReset {
			t.Error("a reset below the position was restated")
		}
	}
}

func TestAResetIsNotAnnouncedToAnApplicationThatNeverTookPart(t *testing.T) {
	// An application whose endpoint was never routed holds no bindings in the
	// tenant, so it has nothing to discard and is not told.
	f := newFixture(t)
	f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	f.router.AddEndpoint("fmis-b", f.tenant, "ep-b")

	client, err := agmasync.NewClient(f.server.URL, agmasync.WithBearerToken("fmis-b"))
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	if err := f.router.ResetTenant(f.tenant); err != nil {
		t.Fatalf("reset: %v", err)
	}

	stream := openEvents(t, &participant{client: client}, "")
	for _, ev := range untilCaughtUp(t, stream) {
		if ev.Type == agmasync.EventMasterdataReset {
			t.Error("an application that never took part was told of a reset")
		}
	}
}

// completeLoadFromReconciling finishes a load whose set has already been taken.
func completeLoadFromReconciling(t *testing.T, p *participant) {
	t.Helper()
	ctx := context.Background()
	if _, err := p.endpoint.ConfirmReconciled(ctx, nil); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if _, err := p.endpoint.CompleteInitialLoad(ctx); err != nil {
		t.Fatalf("complete: %v", err)
	}
}

func TestAResetReachesAnApplicationWhoseEndpointWasRemoved(t *testing.T) {
	// The mapping outlives endpoint removal, so an application with no endpoint
	// left in the tenant still holds bindings there. The reset is how it learns
	// they are stale; it lists no endpoints, the application having none there.
	f := newFixture(t)
	sender := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	receiver := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	created, err := sender.endpoint.Put(context.Background(), farm("FRM-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agmasync.EnvelopeOf(created)
	if err := receiver.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-1", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := f.router.RemoveEndpoint("ep-b"); err != nil {
		t.Fatalf("removing the endpoint: %v", err)
	}
	if err := f.router.ResetTenant(f.tenant); err != nil {
		t.Fatalf("reset: %v", err)
	}

	var reset *agmasync.Event
	for _, ev := range untilCaughtUp(t, openEvents(t, receiver, "")) {
		if ev.Type == agmasync.EventMasterdataReset {
			reset = &ev
		}
	}
	if reset == nil {
		t.Fatal("an application holding bindings in the tenant was not told of the reset")
	}
	if reset.Reset.TenantId != f.tenant || len(reset.Reset.Endpoints) != 0 {
		t.Errorf("reset = %+v, want this tenant and no endpoints", reset.Reset)
	}
}
