package testrouter_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/agrirouter"
)

// The lifecycle of a canonical object: the write that creates it sets its
// state, after which only deactivation and reactivation change it. A write
// states the state on every send, and one that contradicts it is refused. See
// "Lifecycle" in specification.md.

// inactive is the JSON body stating the object inactive rather than active.
func inactive(body string) string {
	return strings.Replace(body, `"active":true`, `"active":false`, 1)
}

func TestAWriteWithoutActiveIsRejected(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	write := `{"type":"farm","content":{"id":{"referenceId":"FRM-1"},"name":"Hof Nord"}}`
	if status := putRaw(t, f, "fmis-a", p, "FRM-1", write); status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a write without active", status)
	}
}

func TestACreateMayBeInactive(t *testing.T) {
	// A record the participant holds as archived is sent inactive in one
	// write, so that it is never delivered as active in between.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	created := putFarm(t, p, inactive(body("farm", "FRM-1", `"name":"Hof Alt"`)), nil)
	if got := string(fieldsOf(t, created)["active"]); got != "false" {
		t.Errorf("active = %s, want the object created inactive", got)
	}
	if got := revisionOf(t, created); got != 1 {
		t.Errorf("revision = %d, want 1", got)
	}
}

func TestAContradictingActiveIsALifecycleConflict(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	// Retiring an object is not a write's to do.
	_, err := p.endpoint.Put(context.Background(), entityOf(t, inactive(richFarm)), &base)
	var conflict *agmasync.LifecycleConflict
	if !errors.As(err, &conflict) || !conflict.CurrentActive {
		t.Fatalf("error = %v, want a lifecycle conflict naming the object active", err)
	}

	deactivated, err := p.endpoint.Deactivate(context.Background(), agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// Nor is reviving one, even unchanged and without a base: the check comes
	// before the no-op and before concurrency control.
	_, err = p.endpoint.Put(context.Background(), entityOf(t, richFarm), nil)
	if !errors.As(err, &conflict) || conflict.CurrentActive {
		t.Fatalf("error = %v, want a lifecycle conflict naming the object inactive", err)
	}
	if !errors.Is(err, agmasync.ErrLifecycleConflict) {
		t.Errorf("error = %v, want it to match ErrLifecycleConflict", err)
	}

	stale := revisionOf(t, deactivated) - 1
	_, err = p.endpoint.Put(context.Background(), entityOf(t, richFarm), &stale)
	if !errors.Is(err, agmasync.ErrLifecycleConflict) {
		t.Errorf("error = %v, want the lifecycle conflict ahead of the stale base", err)
	}
}

func TestAnEditToAnInactiveObjectKeepsItInactive(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	deactivated, err := p.endpoint.Deactivate(context.Background(), agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	base = revisionOf(t, deactivated)

	renamed := putFarm(t, p, inactive(body("farm", "FRM-1", `"name":"Hof Süd"`)), &base)
	fields := fieldsOf(t, renamed)
	if got := string(fields["active"]); got != "false" {
		t.Errorf("active = %s, want the object to stay inactive", got)
	}
	if got := revisionOf(t, renamed); got != base+1 {
		t.Errorf("revision = %d, want %d: an edit to an inactive object is a write like any other", got, base+1)
	}
}

func TestReactivationMirrorsDeactivation(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	ctx := context.Background()

	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	// Reactivating an object that is active changes nothing, whatever the base.
	unchanged, err := p.endpoint.Reactivate(ctx, agmasync.TypeFarm, "FRM-1", nil)
	if err != nil {
		t.Fatalf("reactivating an active object must succeed: %v", err)
	}
	if revisionOf(t, unchanged) != base {
		t.Error("reactivating an active object produced a new revision")
	}

	deactivated, err := p.endpoint.Deactivate(ctx, agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	inactiveAt := revisionOf(t, deactivated)

	if _, err := p.endpoint.Reactivate(ctx, agmasync.TypeFarm, "FRM-1", nil); !errors.Is(
		err, agmasync.ErrBaseRevisionRequired,
	) {
		t.Errorf("error = %v, want ErrBaseRevisionRequired for a first reactivation without a base", err)
	}
	stale := inactiveAt - 1
	if _, err := p.endpoint.Reactivate(ctx, agmasync.TypeFarm, "FRM-1", &stale); !errors.Is(
		err, agmasync.ErrRevisionConflict,
	) {
		t.Errorf("error = %v, want ErrRevisionConflict for a stale base", err)
	}

	reactivated, err := p.endpoint.Reactivate(ctx, agmasync.TypeFarm, "FRM-1", &inactiveAt)
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	env, _ := agrirouter.EnvelopeOf(reactivated)
	if !env.Active {
		t.Error("the object should be active after reactivation")
	}
	if revisionOf(t, reactivated) != inactiveAt+1 {
		t.Errorf("revision = %d, want %d", revisionOf(t, reactivated), inactiveAt+1)
	}
	if got, want := fieldsOf(t, reactivated)["content"], fieldsOf(t, deactivated)["content"]; string(got) != string(want) {
		t.Errorf("content = %s, want it unchanged by reactivation: %s", got, want)
	}

	// A retry, base and all, is safe.
	again, err := p.endpoint.Reactivate(ctx, agmasync.TypeFarm, "FRM-1", &inactiveAt)
	if err != nil {
		t.Fatalf("reactivating a reactivated object must succeed: %v", err)
	}
	if revisionOf(t, again) != revisionOf(t, reactivated) {
		t.Error("repeating a reactivation produced a new revision")
	}

	if _, err := p.endpoint.Reactivate(ctx, agmasync.TypeFarm, "FRM-NONE", &inactiveAt); !errors.Is(
		err, agmasync.ErrNotFound,
	) {
		t.Errorf("error = %v, want ErrNotFound for an object this endpoint does not know", err)
	}
}

func TestAnInactiveObjectIsDeliveredAsAChange(t *testing.T) {
	// There is one object event. An inactive object is MASTERDATA_CHANGED with
	// `active: false`, and a reactivated one MASTERDATA_CHANGED with
	// `active: true`.
	f := newFixture(t)
	writer := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	f.join("fmis-b", "ep-b", agmasync.TypeFarm)
	ctx := context.Background()

	base := revisionOf(t, putFarm(t, writer, richFarm, nil))
	deactivated, err := writer.endpoint.Deactivate(ctx, agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	caught, _ := liveFrames(t, f, "fmis-b")
	if len(caught) != 1 {
		t.Fatalf("catch-up carried %d objects, want the inactive farm", len(caught))
	}
	if caught[0].Type != agmasync.EventMasterdataChanged || caught[0].Envelope.Active {
		t.Errorf("frame = %q active=%t, want MASTERDATA_CHANGED with active false",
			caught[0].Type, caught[0].Envelope.Active)
	}

	inactiveAt := revisionOf(t, deactivated)
	if _, err := writer.endpoint.Reactivate(ctx, agmasync.TypeFarm, "FRM-1", &inactiveAt); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	caught, _ = liveFrames(t, f, "fmis-b")
	if len(caught) != 1 || caught[0].Type != agmasync.EventMasterdataChanged || !caught[0].Envelope.Active {
		t.Errorf("after reactivation got %d frames, want one MASTERDATA_CHANGED with active true", len(caught))
	}
}
