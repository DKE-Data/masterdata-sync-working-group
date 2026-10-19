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

// A party is a person, an organization, or of unknown party type, and
// `_agmasync_details` says which. See "Party details" in specification.md.

func put(t *testing.T, p *participant, body string, base *int) oapi.Entity {
	t.Helper()
	got, err := p.endpoint.Put(context.Background(), entityOf(t, body), base)
	if err != nil {
		t.Fatalf("put %s: %v", body, err)
	}
	return got
}

func putErr(t *testing.T, p *participant, body string, base *int) error {
	t.Helper()
	_, err := p.endpoint.Put(context.Background(), entityOf(t, body), base)
	return err
}

func agrirouterIDIn(t *testing.T, ent oapi.Entity) uuid.UUID {
	t.Helper()
	env, err := agrirouter.EnvelopeOf(ent)
	if err != nil || env.AgrirouterId == nil {
		t.Fatalf("reading the agrirouter entry: %v", err)
	}
	return *env.AgrirouterId
}

func detailsOf(t *testing.T, ent oapi.Entity) string {
	t.Helper()
	return string(attributesOf(t, ent)["_agmasync_details"])
}

var person = body("party", "PTY-1", `"name":"Anke Meyer",`+
	`"_agmasync_details":{"partyTypeCode":"PERSON","firstName":"Anke","lastName":"Meyer"}`)

var organization = body("party", "ORG-1", `"name":"Agrar GmbH",`+
	`"_agmasync_details":{"partyTypeCode":"ORGANIZATION"}`)

// member is a person who is a member of the party target names.
func member(target string) string {
	return body("party", "PTY-1", `"name":"Anke Meyer",`+
		`"_agmasync_details":{"partyTypeCode":"PERSON","lastName":"Meyer","memberships":[`+
		`{"organizationRef":{"id":{"referenceId":"`+target+`"}},"roleCode":"OWNER"}]}`)
}

// boundaryOf is a boundary of the field named fieldID.
func boundaryOf(fieldID string) string {
	return body("fieldBoundary", "FB-1",
		`"_agmasync_fieldRef":{"id":{"referenceId":"`+fieldID+`"}},`+
			`"boundary":{"geometry":"POLYGON ((8 52, 8.1 52, 8.1 52.1, 8 52))"}`)
}

func TestPartyDetailsMergeWhileTheirTypeStays(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	base := revisionOf(t, put(t, p, person, nil))

	got := put(t, p, body("party", "PTY-1", `"name":"Anke Meyer",`+
		`"_agmasync_details":{"partyTypeCode":"PERSON","title":"Dr."}`), &base)

	want := `{"firstName":"Anke","lastName":"Meyer","partyTypeCode":"PERSON","title":"Dr."}`
	if details := detailsOf(t, got); details != want {
		t.Errorf("_agmasync_details = %s, want %s", details, want)
	}
}

func TestChangingThePartyTypeReplacesDetailsWhole(t *testing.T) {
	// No attribute of the other party type survives: a person's last name on
	// an organization would be a schema violation, not a leftover. A null in
	// the replacing details removes, as anywhere in a patch, rather than being
	// stored.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	base := revisionOf(t, put(t, p, person, nil))

	got := put(t, p, body("party", "PTY-1", `"name":"Meyer Agrar",`+
		`"_agmasync_details":{"partyTypeCode":"ORGANIZATION","commercialRegistryNumber":"HRB 1","title":null}`), &base)

	want := `{"commercialRegistryNumber":"HRB 1","partyTypeCode":"ORGANIZATION"}`
	if details := detailsOf(t, got); details != want {
		t.Errorf("_agmasync_details = %s, want %s", details, want)
	}
}

func TestDetailsWithoutAPartyTypeAreRejected(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)

	err := putErr(t, p, body("party", "PTY-1", `"name":"Anke Meyer",`+
		`"_agmasync_details":{"lastName":"Meyer"}`), nil)
	if !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("error = %v, want ErrValidation", err)
	}
}

func TestAWriterThatDoesNotKnowTheKindKeepsIt(t *testing.T) {
	// A participant that keeps a customer without recording whether it is a
	// person or a business leaves _agmasync_details out, which keeps what
	// another said.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	b := f.join("fmis-b", "ep-b", agmasync.TypeParty)
	created := put(t, a, person, nil)
	id := agrirouterIDIn(t, created)

	if err := b.endpoint.Bind(context.Background(), agmasync.TypeParty, "CUST-7", id); err != nil {
		t.Fatalf("bind: %v", err)
	}
	base := revisionOf(t, created)
	got := put(t, b, body("party", "CUST-7", `"name":"Anke Meyer-Lüth"`), &base)

	if details := detailsOf(t, got); details != `{"firstName":"Anke","lastName":"Meyer","partyTypeCode":"PERSON"}` {
		t.Errorf("_agmasync_details = %s, want them kept", details)
	}
}

func TestAMembershipMustNameAnOrganization(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	put(t, p, body("party", "CUST-1", `"name":"Hof Nord"`), nil)
	put(t, p, organization, nil)

	// A party of unknown party type might be a person; naming it is a 400.
	if err := putErr(t, p, member("CUST-1"), nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("membership naming a party of unknown party type: error = %v, want ErrValidation", err)
	}
	put(t, p, member("ORG-1"), nil)
}

func TestAnOrganizationPersonsNameKeepsItsKind(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	base := revisionOf(t, put(t, p, organization, nil))
	put(t, p, member("ORG-1"), nil)

	for name, write := range map[string]string{
		"changed": body("party", "ORG-1", `"name":"Agrar GmbH",`+
			`"_agmasync_details":{"partyTypeCode":"PERSON"}`),
		"removed": body("party", "ORG-1", `"name":"Agrar GmbH","_agmasync_details":null`),
	} {
		if err := putErr(t, p, write, &base); !errors.Is(err, agmasync.ErrValidation) {
			t.Errorf("party type %s on a named organization: error = %v, want ErrValidation", name, err)
		}
	}
}

func TestABoundaryNamesItsField(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFieldBoundary)

	if err := putErr(t, p, boundaryOf("PFD-1"), nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("boundary naming an unsent field: error = %v, want ErrValidation", err)
	}

	put(t, p, body("field", "PFD-1", `"name":"North 40"`), nil)
	put(t, p, boundaryOf("PFD-1"), nil)
}

func TestDeliveryOrderFollowsReferencesNotAge(t *testing.T) {
	// Each object is delivered after what it names, even where the target last
	// changed after the object naming it: an organization edited after its
	// member, a field edited after its boundary. Persons sit below the other
	// parties, boundaries below fields.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.EntityTypes...)

	orgBase := revisionOf(t, put(t, a, organization, nil))
	put(t, a, member("ORG-1"), nil)
	put(t, a, body("party", "ORG-1", `"name":"Agrar AG",`+
		`"_agmasync_details":{"partyTypeCode":"ORGANIZATION"}`), &orgBase)

	fieldBase := revisionOf(t, put(t, a, body("field", "PFD-1", `"name":"North 40"`), nil))
	put(t, a, boundaryOf("PFD-1"), nil)
	put(t, a, body("field", "PFD-1", `"name":"North 41"`), &fieldBase)

	b := f.join("fmis-b", "ep-b", agmasync.EntityTypes...)
	stream, err := b.endpoint.InitialLoadEvents(context.Background())
	if err != nil {
		t.Fatalf("initial load stream: %v", err)
	}
	defer stream.Close()

	var order []string
	for ev := range stream.Events() {
		if !ev.HasEntity() {
			continue
		}
		label := string(ev.Envelope.Type)
		if ev.Envelope.Type == agmasync.TypeParty {
			entity, err := agrirouter.EntityOf(ev.Object)
			if err != nil {
				t.Fatal(err)
			}
			var details struct {
				PartyTypeCode string `json:"partyTypeCode"`
			}
			_ = json.Unmarshal(attributesOf(t, entity)["_agmasync_details"], &details)
			label = details.PartyTypeCode
		}
		order = append(order, label)
	}

	want := []string{"ORGANIZATION", "PERSON", "field", "fieldBoundary"}
	if len(order) != len(want) {
		t.Fatalf("delivered %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("delivered %v, want %v", order, want)
		}
	}
}
