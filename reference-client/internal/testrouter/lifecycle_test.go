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
// state, after which only archiving and unarchiving change it. A write
// states the state on every send, and one that contradicts it is refused. See
// "Lifecycle" in specification.md.

// archived is the JSON body stating the object archived rather than not.
func archived(body string) string {
	return strings.Replace(body, `"archived":false`, `"archived":true`, 1)
}

func TestAWriteWithoutArchivedIsRejected(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	write := `{"type":"farm","content":{"id":{"referenceId":"FRM-1"},"name":"Hof Nord"}}`
	if status := putRaw(t, f, "fmis-a", p, "FRM-1", write); status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a write without archived", status)
	}
}

func TestACreateMayBeArchived(t *testing.T) {
	// A record the participant holds as archived is sent archived in one
	// write, so that it is never delivered as not archived in between.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	created := putFarm(t, p, archived(body("farm", "FRM-1", `"name":"Hof Alt"`)), nil)
	if got := string(fieldsOf(t, created)["archived"]); got != "true" {
		t.Errorf("archived = %s, want the object created archived", got)
	}
	if got := revisionOf(t, created); got != 1 {
		t.Errorf("revision = %d, want 1", got)
	}
}

func TestAContradictingArchivedIsALifecycleConflict(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	// Retiring an object is not a write's to do.
	_, err := p.endpoint.Put(context.Background(), entityOf(t, archived(richFarm)), &base)
	var conflict *agmasync.LifecycleConflict
	if !errors.As(err, &conflict) || conflict.CurrentArchived {
		t.Fatalf("error = %v, want a lifecycle conflict naming the object not archived", err)
	}

	archivedFarm, err := p.endpoint.Archive(context.Background(), agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Nor is reviving one, even unchanged and without a base: the check comes
	// before the no-op and before concurrency control.
	_, err = p.endpoint.Put(context.Background(), entityOf(t, richFarm), nil)
	if !errors.As(err, &conflict) || !conflict.CurrentArchived {
		t.Fatalf("error = %v, want a lifecycle conflict naming the object archived", err)
	}
	if !errors.Is(err, agmasync.ErrLifecycleConflict) {
		t.Errorf("error = %v, want it to match ErrLifecycleConflict", err)
	}

	stale := revisionOf(t, archivedFarm) - 1
	_, err = p.endpoint.Put(context.Background(), entityOf(t, richFarm), &stale)
	if !errors.Is(err, agmasync.ErrLifecycleConflict) {
		t.Errorf("error = %v, want the lifecycle conflict ahead of the stale base", err)
	}
}

func TestAnEditToAnArchivedObjectKeepsItArchived(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	archivedFarm, err := p.endpoint.Archive(context.Background(), agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	base = revisionOf(t, archivedFarm)

	renamed := putFarm(t, p, archived(body("farm", "FRM-1", `"name":"Hof Süd"`)), &base)
	fields := fieldsOf(t, renamed)
	if got := string(fields["archived"]); got != "true" {
		t.Errorf("archived = %s, want the object to stay archived", got)
	}
	if got := revisionOf(t, renamed); got != base+1 {
		t.Errorf("revision = %d, want %d: an edit to an archived object is a write like any other", got, base+1)
	}
}

func TestUnarchivingMirrorsArchiving(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	ctx := context.Background()

	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	// Unarchiving an object that is not archived changes nothing, whatever
	// the base.
	unchanged, err := p.endpoint.Unarchive(ctx, agmasync.TypeFarm, "FRM-1", nil)
	if err != nil {
		t.Fatalf("unarchiving an object that is not archived must succeed: %v", err)
	}
	if revisionOf(t, unchanged) != base {
		t.Error("unarchiving an object that is not archived produced a new revision")
	}

	archivedFarm, err := p.endpoint.Archive(ctx, agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	archivedAt := revisionOf(t, archivedFarm)

	if _, err := p.endpoint.Unarchive(ctx, agmasync.TypeFarm, "FRM-1", nil); !errors.Is(
		err, agmasync.ErrBaseRevisionRequired,
	) {
		t.Errorf("error = %v, want ErrBaseRevisionRequired for a first unarchiving without a base", err)
	}
	stale := archivedAt - 1
	if _, err := p.endpoint.Unarchive(ctx, agmasync.TypeFarm, "FRM-1", &stale); !errors.Is(
		err, agmasync.ErrRevisionConflict,
	) {
		t.Errorf("error = %v, want ErrRevisionConflict for a stale base", err)
	}

	unarchived, err := p.endpoint.Unarchive(ctx, agmasync.TypeFarm, "FRM-1", &archivedAt)
	if err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	env, _ := agrirouter.EnvelopeOf(unarchived)
	if env.Archived {
		t.Error("the object should not be archived after unarchiving")
	}
	if revisionOf(t, unarchived) != archivedAt+1 {
		t.Errorf("revision = %d, want %d", revisionOf(t, unarchived), archivedAt+1)
	}
	if got, want := fieldsOf(t, unarchived)["content"], fieldsOf(t, archivedFarm)["content"]; string(got) != string(want) {
		t.Errorf("content = %s, want it unchanged by unarchiving: %s", got, want)
	}

	// A retry, base and all, is safe.
	again, err := p.endpoint.Unarchive(ctx, agmasync.TypeFarm, "FRM-1", &archivedAt)
	if err != nil {
		t.Fatalf("unarchiving an unarchived object must succeed: %v", err)
	}
	if revisionOf(t, again) != revisionOf(t, unarchived) {
		t.Error("repeating an unarchiving produced a new revision")
	}

	if _, err := p.endpoint.Unarchive(ctx, agmasync.TypeFarm, "FRM-NONE", &archivedAt); !errors.Is(
		err, agmasync.ErrNotFound,
	) {
		t.Errorf("error = %v, want ErrNotFound for an object this endpoint does not know", err)
	}
}

func TestAnArchivedObjectIsDeliveredAsAChange(t *testing.T) {
	// There is one object event. An archived object is MASTERDATA_CHANGED with
	// `archived: true`, and an unarchived one MASTERDATA_CHANGED with
	// `archived: false`.
	f := newFixture(t)
	writer := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	f.join("fmis-b", "ep-b", agmasync.TypeFarm)
	ctx := context.Background()

	base := revisionOf(t, putFarm(t, writer, richFarm, nil))
	archivedFarm, err := writer.endpoint.Archive(ctx, agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}

	caught, _ := liveFrames(t, f, "fmis-b")
	if len(caught) != 1 {
		t.Fatalf("catch-up carried %d objects, want the archived farm", len(caught))
	}
	if caught[0].Type != agmasync.EventMasterdataChanged || !caught[0].Envelope.Archived {
		t.Errorf("frame = %q archived=%t, want MASTERDATA_CHANGED with archived true",
			caught[0].Type, caught[0].Envelope.Archived)
	}

	archivedAt := revisionOf(t, archivedFarm)
	if _, err := writer.endpoint.Unarchive(ctx, agmasync.TypeFarm, "FRM-1", &archivedAt); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	caught, _ = liveFrames(t, f, "fmis-b")
	if len(caught) != 1 || caught[0].Type != agmasync.EventMasterdataChanged || caught[0].Envelope.Archived {
		t.Errorf("after unarchiving got %d frames, want one MASTERDATA_CHANGED with archived false", len(caught))
	}
}
