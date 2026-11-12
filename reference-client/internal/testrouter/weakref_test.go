package testrouter_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/agrirouter"
	"github.com/google/uuid"
)

// A field names its active boundary by a weak reference: resolved on send like
// any other, but outside the delivery order. See "Weak references" in
// specification.md.

// fieldWithActive is field PFD-1 naming its active boundary by ref.
func fieldWithActive(name, ref string) string {
	return body("field", "PFD-1", `"name":"`+name+`","_agmasync_activeBoundaryRef":{"id":`+ref+`}`)
}

func byLocal(localID string) string { return `{"referenceId":"` + localID + `"}` }

func byAgrirouter(id uuid.UUID) string { return `{"uniqueIds":[` + agrirouterEntry(id) + `]}` }

// boundaryNamed is boundary localID of field PFD-1.
func boundaryNamed(localID string) string {
	return body("fieldBoundary", localID,
		`"_agmasync_fieldRef":{"id":{"referenceId":"PFD-1"}},`+
			`"boundary":{"geometry":"POLYGON ((8 52, 8.1 52, 8.1 52.1, 8 52))"}`)
}

// activeBoundaryOf reads the field's weak reference: its agrirouter entry, and
// the referenceId where the reader has one.
func activeBoundaryOf(t *testing.T, ent oapi.Entity) (uuid.UUID, *string) {
	t.Helper()
	raw, ok := attributesOf(t, ent)["_agmasync_activeBoundaryRef"]
	if !ok {
		t.Fatal("no _agmasync_activeBoundaryRef")
	}
	var ref oapi.WeakRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatalf("reading %s: %v", raw, err)
	}
	id, err := agmasync.IdentityOf(ref.Id)
	if err != nil || id.AgrirouterID == nil {
		t.Fatalf("no agrirouter entry in %s: %v", raw, err)
	}
	return *id.AgrirouterID, id.LocalID
}

func fieldAndBoundaries(t *testing.T, f *fixture) *participant {
	t.Helper()
	return f.join("fmis-a", "ep-a", agmasync.TypeField, agmasync.TypeFieldBoundary)
}

// threeWrites creates field PFD-1, its boundary FB-1, and names FB-1 the
// field's active boundary, returning the boundary's agrirouter id.
func threeWrites(t *testing.T, p *participant) uuid.UUID {
	t.Helper()
	base := revisionOf(t, put(t, p, body("field", "PFD-1", `"name":"North 40"`), nil))
	boundary := agrirouterIDIn(t, put(t, p, boundaryNamed("FB-1"), nil))
	put(t, p, fieldWithActive("North 40", byLocal("FB-1")), &base)
	return boundary
}

func TestAFieldNamesANewActiveBoundaryInThreeWrites(t *testing.T) {
	f := newFixture(t)
	p := fieldAndBoundaries(t, f)
	base := revisionOf(t, put(t, p, body("field", "PFD-1", `"name":"North 40"`), nil))
	boundary := agrirouterIDIn(t, put(t, p, boundaryNamed("FB-1"), nil))

	field := put(t, p, fieldWithActive("North 40", byLocal("FB-1")), &base)

	id, referenceID := activeBoundaryOf(t, field)
	if id != boundary || referenceID == nil || *referenceID != "FB-1" {
		t.Errorf("active boundary = %s %v, want %s named FB-1", id, referenceID, boundary)
	}
}

func TestAWeakReferenceCannotPointForward(t *testing.T) {
	f := newFixture(t)
	p := fieldAndBoundaries(t, f)

	if err := putErr(t, p, fieldWithActive("North 40", byLocal("FB-1")), nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("active boundary not sent yet: error = %v, want ErrValidation", err)
	}
	if err := putErr(t, p, fieldWithActive("North 40", byAgrirouter(uuid.New())), nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("active boundary naming nothing: error = %v, want ErrValidation", err)
	}
}

func TestAWeakReferenceIsCheckedLikeAnyOther(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm, agmasync.TypeField, agmasync.TypeFieldBoundary)
	farmID := agrirouterIDIn(t, putFarm(t, p, body("farm", "FRM-1", `"name":"Hof Nord"`), nil))

	if err := putErr(t, p, fieldWithActive("North 40", byAgrirouter(farmID)), nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("active boundary naming a farm: error = %v, want ErrValidation", err)
	}

	elsewhere := f.joinIn(f.router.AddTenant(), "fmis-a", "ep-x", agmasync.TypeField, agmasync.TypeFieldBoundary)
	put(t, elsewhere, body("field", "PFD-1", `"name":"Far away"`), nil)
	foreign := agrirouterIDIn(t, put(t, elsewhere, boundaryNamed("FB-1"), nil))
	if err := putErr(t, p, fieldWithActive("North 40", byAgrirouter(foreign)), nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("active boundary in another tenant: error = %v, want ErrValidation", err)
	}
}

func TestAnArchivedActiveBoundaryStaysNamed(t *testing.T) {
	f := newFixture(t)
	p := fieldAndBoundaries(t, f)
	boundary := threeWrites(t, p)

	if _, err := p.endpoint.Archive(context.Background(), agmasync.TypeFieldBoundary, "FB-1", intptr(1)); err != nil {
		t.Fatalf("archiving FB-1: %v", err)
	}
	// Nothing cascades: the field still names it, and a write naming it is accepted.
	got := put(t, p, fieldWithActive("North 41", byAgrirouter(boundary)), intptr(2))
	if id, _ := activeBoundaryOf(t, got); id != boundary {
		t.Errorf("active boundary after archiving = %s, want %s", id, boundary)
	}
}

func TestAFieldArrivesBeforeItsActiveBoundary(t *testing.T) {
	f := newFixture(t)
	a := fieldAndBoundaries(t, f)
	boundary := threeWrites(t, a)

	b := f.join("fmis-b", "ep-b", agmasync.TypeField, agmasync.TypeFieldBoundary)
	stream, err := b.endpoint.InitialLoadEvents(context.Background())
	if err != nil {
		t.Fatalf("initial load stream: %v", err)
	}
	defer stream.Close()

	var order []agmasync.EntityType
	for ev := range stream.Events() {
		if !ev.HasEntity() {
			continue
		}
		order = append(order, ev.Envelope.Type)
		if ev.Envelope.Type != agmasync.TypeField {
			continue
		}
		ent, err := agrirouter.EntityOf(ev.Object)
		if err != nil {
			t.Fatal(err)
		}
		id, referenceID := activeBoundaryOf(t, ent)
		if id != boundary || referenceID != nil {
			t.Errorf("delivered reference = %s %v, want %s with no referenceId", id, referenceID, boundary)
		}
	}
	if len(order) != 2 || order[0] != agmasync.TypeField || order[1] != agmasync.TypeFieldBoundary {
		t.Errorf("delivered %v, want the field before its boundary", order)
	}
}

func intptr(v int) *int { return &v }
