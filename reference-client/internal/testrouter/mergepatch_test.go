package testrouter_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/agrirouter"
)

// A write's content is a JSON Merge Patch: a value replaces, null removes, and
// an attribute left out is left alone. See "Writing an entity" in
// specification.md.

// body is the JSON a participant sends: an entity of type typ written under
// localID, its content the given members after the id.
func body(typ, localID, members string) string {
	if members != "" {
		members = "," + members
	}
	return `{"type":"` + typ + `","content":{"id":{"referenceId":"` + localID + `"}` + members + `}}`
}

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

// fieldsOf reads an entity's top-level members: the envelope and `content`.
func fieldsOf(t *testing.T, ent oapi.Entity) map[string]json.RawMessage {
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

// attributesOf reads what agrirouter answered with, content attribute by
// content attribute.
func attributesOf(t *testing.T, ent oapi.Entity) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	if err := json.Unmarshal(fieldsOf(t, ent)["content"], &out); err != nil {
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

var richFarm = body("farm", "FRM-1", `"name":"Hof Nord",`+
	`"_agmasync_specialisedUsageType":"dairy",`+
	`"_agmasync_address":{"addressContactTypeCode":"PHYSICAL","addressLines":["Dorfstr. 1"],"city":"Husum"}`)

func TestAnAttributeLeftOutIsKept(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	got := attributesOf(t, putFarm(t, p,
		body("farm", "FRM-1", `"name":"Hof Süd"`), &base))

	if string(got["name"]) != `"Hof Süd"` {
		t.Errorf("name = %s, want the change applied", got["name"])
	}
	if string(got["_agmasync_specialisedUsageType"]) != `"dairy"` {
		t.Errorf("_agmasync_specialisedUsageType = %s, want it kept", got["_agmasync_specialisedUsageType"])
	}
	if _, ok := got["_agmasync_address"]; !ok {
		t.Error("_agmasync_address was erased by a write that left it out")
	}
}

func TestNullRemovesAnAttribute(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	removed := putFarm(t, p,
		body("farm", "FRM-1", `"name":"Hof Nord","_agmasync_specialisedUsageType":null`), &base)
	got := attributesOf(t, removed)

	if raw, ok := got["_agmasync_specialisedUsageType"]; ok {
		t.Errorf("_agmasync_specialisedUsageType = %s, want it removed", raw)
	}
	if revisionOf(t, removed) != base+1 {
		t.Error("removing an attribute is a change and must produce a revision")
	}
}

func TestNestedObjectsMergeRatherThanReplace(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeField)
	base := revisionOf(t, putFarm(t, p, body("field", "PFD-1",
		`"name":"North 40","_agmasync_soil":{"type":"SAND","ratingPoints":40}`), nil))

	got := attributesOf(t, putFarm(t, p,
		body("field", "PFD-1", `"name":"North 40","_agmasync_soil":{"ratingPoints":62}`), &base))
	if string(got["_agmasync_soil"]) != `{"ratingPoints":62,"type":"SAND"}` {
		t.Errorf("_agmasync_soil = %s, want the rating changed and the type kept", got["_agmasync_soil"])
	}

	base++
	got = attributesOf(t, putFarm(t, p,
		body("field", "PFD-1", `"name":"North 40","_agmasync_soil":{"type":null}`), &base))
	if string(got["_agmasync_soil"]) != `{"ratingPoints":62}` {
		t.Errorf("_agmasync_soil = %s, want only the type removed", got["_agmasync_soil"])
	}
}

func TestABoundaryMergesLikeAnyNestedObject(t *testing.T) {
	// A boundary's geometry is a WKT string and replaces like any value; the
	// boundary around it merges, so a participant that models the geometry and
	// not how it was recorded keeps what another said about the latter.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeField, agmasync.TypeFieldBoundary)
	putFarm(t, p, body("field", "PFD-1", `"name":"North 40"`), nil)
	base := revisionOf(t, putFarm(t, p, body("fieldBoundary", "FB-1",
		`"_agmasync_fieldRef":{"id":{"referenceId":"PFD-1"}},`+
			`"boundary":{"geometry":"POLYGON ((8 52, 8.1 52, 8.1 52.1, 8 52))","boundaryCreationMethodCode":"DRIVEN"}`), nil))

	got := attributesOf(t, putFarm(t, p, body("fieldBoundary", "FB-1",
		`"_agmasync_fieldRef":{"id":{"referenceId":"PFD-1"}},`+
			`"boundary":{"geometry":"POLYGON ((9 52, 9.1 52, 9.1 52.1, 9 52))"}`), &base))
	want := `{"boundaryCreationMethodCode":"DRIVEN","geometry":"POLYGON ((9 52, 9.1 52, 9.1 52.1, 9 52))"}`
	if string(got["boundary"]) != want {
		t.Errorf("boundary = %s, want %s", got["boundary"], want)
	}
}

func TestAFarmsAddressIsReplacedWhole(t *testing.T) {
	// An address is one value, as a party's addresses are: merging into it
	// would keep the street of an address the farm no longer has.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	got := attributesOf(t, putFarm(t, p, body("farm", "FRM-1",
		`"name":"Hof Nord","_agmasync_address":{"addressContactTypeCode":"PHYSICAL","city":"Kiel"}`), &base))
	if string(got["_agmasync_address"]) != `{"addressContactTypeCode":"PHYSICAL","city":"Kiel"}` {
		t.Errorf("_agmasync_address = %s, want the new address and nothing of the old", got["_agmasync_address"])
	}
}

func TestNullOnARequiredAttributeIsRejected(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	base := revisionOf(t, putFarm(t, p, richFarm, nil))

	// Sent as JSON: a typed farm cannot carry a null name.
	_, err := p.endpoint.PutJSON(context.Background(),
		[]byte(body("farm", "FRM-1", `"name":null`)), &base)
	var status *agrirouter.StatusError
	if !errors.As(err, &status) || status.StatusCode != http.StatusBadRequest {
		t.Errorf("error = %v, want HTTP 400", err)
	}
}

func TestAFarmsGrowerIsOptional(t *testing.T) {
	// A farm may be written without a grower, and null removes one, since a
	// system that records no parties has none to give (ADR 13).
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty, agmasync.TypeFarm)
	putFarm(t, p, body("party", "PTY-1", `"name":"Jens Petersen",`+
		`"_agmasync_details":{"partyTypeCode":"PERSON"}`), nil)
	base := revisionOf(t, putFarm(t, p, body("farm", "FRM-1",
		`"name":"Hof Nord","_agmasync_growerRef":{"id":{"referenceId":"PTY-1"}}`), nil))

	got := attributesOf(t, putFarm(t, p,
		body("farm", "FRM-1", `"name":"Hof Nord","_agmasync_growerRef":null`), &base))
	if raw, ok := got["_agmasync_growerRef"]; ok {
		t.Errorf("_agmasync_growerRef = %s, want it removed", raw)
	}
}

func TestNullOnCreateMeansAbsent(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	got := attributesOf(t, putFarm(t, p,
		body("farm", "FRM-1", `"name":"Hof Nord","_agmasync_specialisedUsageType":null`), nil))
	if raw, ok := got["_agmasync_specialisedUsageType"]; ok {
		t.Errorf("_agmasync_specialisedUsageType = %s, want it absent", raw)
	}
}

func TestAPartialWriteThatChangesNothingIsANoOp(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)
	before := revisionOf(t, putFarm(t, p, richFarm, nil))

	// No base: a write that changes nothing succeeds whatever the base.
	again := putFarm(t, p, body("farm", "FRM-1", `"name":"Hof Nord"`), nil)
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
	env, _ := agrirouter.EnvelopeOf(created)
	if err := b.endpoint.Bind(
		context.Background(), agmasync.TypeFarm, "B-FARM-9", *env.AgrirouterID,
	); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// A changes the usage type.
	putFarm(t, a, body("farm", "FRM-1", `"name":"Hof Nord","_agmasync_specialisedUsageType":"arable"`), &base)

	// B, from the old base and not modelling the usage type, renames.
	got := attributesOf(t, putFarm(t, b,
		body("farm", "B-FARM-9", `"name":"Hof West"`), &base))

	if string(got["name"]) != `"Hof West"` {
		t.Errorf("name = %s, want B's rename", got["name"])
	}
	if string(got["_agmasync_specialisedUsageType"]) != `"arable"` {
		t.Errorf("_agmasync_specialisedUsageType = %s, want A's change kept", got["_agmasync_specialisedUsageType"])
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

	renamed := putFarm(t, p, body("farm", "FRM-1", `"name":"Hof Süd"`), &base)
	if active := fieldsOf(t, renamed)["active"]; string(active) != "false" {
		t.Errorf("active = %s, want the object to stay inactive", active)
	}
}
