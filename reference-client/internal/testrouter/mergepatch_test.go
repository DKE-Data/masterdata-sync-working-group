package testrouter_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
)

// A write is a JSON Merge Patch: a value replaces, null removes, and an
// attribute left out is left alone. See "Writing an entity" in
// specification.md.

// entityOf builds an entity from the JSON a participant sends, which is the
// only way to say null or to leave an attribute out.
func entityOf(t *testing.T, body string) oapi.Entity {
	t.Helper()
	var ent oapi.Entity
	if err := ent.UnmarshalJSON([]byte(body)); err != nil {
		t.Fatalf("building entity: %v", err)
	}
	return ent
}

// attributesOf reads what agrirouter answered with, attribute by attribute.
func attributesOf(t *testing.T, ent oapi.Entity) map[string]json.RawMessage {
	t.Helper()
	raw, err := ent.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func putFarm(t *testing.T, p *participant, body string, base *int) oapi.Entity {
	t.Helper()
	got, err := p.endpoint.Put(context.Background(), entityOf(t, body), base)
	if err != nil {
		t.Fatalf("put %s: %v", body, err)
	}
	return got
}

const richFarm = `{"type":"farm","local_id":"FRM-1","name":"Hof Nord",` +
	`"specialised_usage_type":"dairy",` +
	`"address":{"street":"Dorfstr. 1","city":"Husum"}}`

func TestAnAttributeLeftOutIsKept(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	got := attributesOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Süd"}`, &base))

	if string(got["name"]) != `"Hof Süd"` {
		t.Errorf("name = %s, want the change applied", got["name"])
	}
	if string(got["specialised_usage_type"]) != `"dairy"` {
		t.Errorf("specialised_usage_type = %s, want it kept", got["specialised_usage_type"])
	}
	if _, ok := got["address"]; !ok {
		t.Error("address was erased by a write that left it out")
	}
}

func TestNullRemovesAnAttribute(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	got := attributesOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Nord","specialised_usage_type":null}`, &base))

	if raw, ok := got["specialised_usage_type"]; ok {
		t.Errorf("specialised_usage_type = %s, want it removed", raw)
	}
	if revisionOf(t, entityOf(t, mustJSON(t, got))) != base+1 {
		t.Error("removing an attribute is a change and must produce a revision")
	}
}

func TestNestedObjectsMergeRatherThanReplace(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	got := attributesOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Nord","address":{"city":"Kiel"}}`, &base))
	if string(got["address"]) != `{"city":"Kiel","street":"Dorfstr. 1"}` {
		t.Errorf("address = %s, want the city changed and the street kept", got["address"])
	}

	base++
	got = attributesOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Nord","address":{"street":null}}`, &base))
	if string(got["address"]) != `{"city":"Kiel"}` {
		t.Errorf("address = %s, want only the street removed", got["address"])
	}
}

func TestGeometriesAreReplacedWhole(t *testing.T) {
	// Merging into a geometry would keep parts of the old one, a bbox for
	// instance, that describe a shape no longer there.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Nord",`+
			`"geo_reference":{"type":"Point","coordinates":[9,54],"bbox":[9,54,9,54]}}`, nil))

	got := attributesOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Nord",`+
			`"geo_reference":{"type":"Point","coordinates":[10,53]}}`, &base))
	if string(got["geo_reference"]) != `{"coordinates":[10,53],"type":"Point"}` {
		t.Errorf("geo_reference = %s, want the new point and nothing of the old", got["geo_reference"])
	}
}

func TestNullOnARequiredAttributeIsRejected(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	_, err := p.endpoint.Put(context.Background(),
		entityOf(t, `{"type":"farm","local_id":"FRM-1","name":null}`), &base)
	if !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("error = %v, want ErrValidation", err)
	}
}

func TestNullOnCreateMeansAbsent(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	got := attributesOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Nord","specialised_usage_type":null}`, nil))
	if raw, ok := got["specialised_usage_type"]; ok {
		t.Errorf("specialised_usage_type = %s, want it absent", raw)
	}
}

func TestAPartialWriteThatChangesNothingIsANoOp(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	before := revisionOf(t, putFarm(t, p, richFarm, nil))

	// No base: a write that changes nothing succeeds whatever the base.
	again := putFarm(t, p, `{"type":"farm","local_id":"FRM-1","name":"Hof Nord"}`, nil)
	if after := revisionOf(t, again); after != before {
		t.Errorf("revision moved from %d to %d on a write that changes nothing", before, after)
	}
}

func TestAStaleWriteNeitherConflictsWithNorRevertsWhatItLeavesOut(t *testing.T) {
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	b := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	created := putFarm(t, a, richFarm, nil)
	base := revisionOf(t, created)
	env, _ := agmasync.EnvelopeOf(created)
	if err := b.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-FARM-9", *env.AgrirouterId,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// A changes the usage type.
	putFarm(t, a, `{"type":"farm","local_id":"FRM-1","name":"Hof Nord","specialised_usage_type":"arable"}`, &base)

	// B, from the old base and not modelling the usage type, renames.
	got := attributesOf(t, putFarm(t, b,
		`{"type":"farm","local_id":"B-FARM-9","name":"Hof West"}`, &base))

	if string(got["name"]) != `"Hof West"` {
		t.Errorf("name = %s, want B's rename", got["name"])
	}
	if string(got["specialised_usage_type"]) != `"arable"` {
		t.Errorf("specialised_usage_type = %s, want A's change kept", got["specialised_usage_type"])
	}
}

func TestAWriteWithoutActiveLeavesTheStateAlone(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	deactivated, err := p.endpoint.Deactivate(context.Background(), agmasync.TypeFarm, "FRM-1", &base)
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	base = revisionOf(t, deactivated)

	got := attributesOf(t, putFarm(t, p,
		`{"type":"farm","local_id":"FRM-1","name":"Hof Süd"}`, &base))
	if string(got["active"]) != "false" {
		t.Errorf("active = %s, want the object to stay inactive", got["active"])
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
