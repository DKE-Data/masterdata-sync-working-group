package testrouter_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/agrirouter"
	"github.com/google/uuid"
)

// A party is a person, an organization, or of unknown party type, and `details`
// says which. See "Party details" in specification.md.

func put(t *testing.T, p *participant, body string, base *int) map[string]json.RawMessage {
	t.Helper()
	got, err := p.endpoint.Put(context.Background(), entityOf(t, body), base)
	if err != nil {
		t.Fatalf("put %s: %v", body, err)
	}
	return attributesOf(t, got)
}

func putErr(t *testing.T, p *participant, body string, base *int) error {
	t.Helper()
	_, err := p.endpoint.Put(context.Background(), entityOf(t, body), base)
	return err
}

func revisionIn(t *testing.T, attributes map[string]json.RawMessage) int {
	t.Helper()
	var revision int
	if err := json.Unmarshal(attributes["revision"], &revision); err != nil {
		t.Fatalf("reading revision: %v", err)
	}
	return revision
}

func agrirouterIDIn(t *testing.T, attributes map[string]json.RawMessage) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := json.Unmarshal(attributes["agrirouter_id"], &id); err != nil {
		t.Fatalf("reading agrirouter_id: %v", err)
	}
	return id
}

const person = `{"type":"party","local_id":"PTY-1","name":"Anke Meyer",` +
	`"details":{"party_type":"PERSON","first_name":"Anke","last_name":"Meyer"}}`

const organization = `{"type":"party","local_id":"ORG-1","name":"Agrar GmbH",` +
	`"details":{"party_type":"ORGANIZATION"}}`

func TestPartyDetailsMergeWhileTheirTypeStays(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	base := revisionIn(t, put(t, p, person, nil))

	got := put(t, p, `{"type":"party","local_id":"PTY-1","name":"Anke Meyer",`+
		`"details":{"party_type":"PERSON","title":"Dr."}}`, &base)

	want := `{"first_name":"Anke","last_name":"Meyer","party_type":"PERSON","title":"Dr."}`
	if string(got["details"]) != want {
		t.Errorf("details = %s, want %s", got["details"], want)
	}
}

func TestChangingThePartyTypeReplacesDetailsWhole(t *testing.T) {
	// No attribute of the other party type survives: a person's last name on
	// an organization would be a schema violation, not a leftover. A null in
	// the replacing details removes, as anywhere in a patch, rather than being
	// stored.
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	base := revisionIn(t, put(t, p, person, nil))

	got := put(t, p, `{"type":"party","local_id":"PTY-1","name":"Meyer Agrar",`+
		`"details":{"party_type":"ORGANIZATION","commercial_registry_number":"HRB 1","title":null}}`, &base)

	want := `{"commercial_registry_number":"HRB 1","party_type":"ORGANIZATION"}`
	if string(got["details"]) != want {
		t.Errorf("details = %s, want %s", got["details"], want)
	}
}

func TestDetailsWithoutAPartyTypeAreRejected(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)

	err := putErr(t, p, `{"type":"party","local_id":"PTY-1","name":"Anke Meyer",`+
		`"details":{"last_name":"Meyer"}}`, nil)
	if !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("error = %v, want ErrValidation", err)
	}
}

func TestAWriterThatDoesNotKnowTheKindKeepsIt(t *testing.T) {
	// A participant that keeps a customer without recording whether it is a
	// person or a business leaves details out, which keeps what another said.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	b := f.join("fmis-b", "ep-b", agmasync.TypeParty)
	created := put(t, a, person, nil)
	id := agrirouterIDIn(t, created)

	if err := b.endpoint.Bind(context.Background(), agmasync.TypeParty, "CUST-7", id); err != nil {
		t.Fatalf("bind: %v", err)
	}
	base := revisionIn(t, created)
	got := put(t, b, `{"type":"party","local_id":"CUST-7","name":"Anke Meyer-Lüth"}`, &base)

	if string(got["details"]) != `{"first_name":"Anke","last_name":"Meyer","party_type":"PERSON"}` {
		t.Errorf("details = %s, want them kept", got["details"])
	}
}

func TestAMembershipMustNameAnOrganization(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	put(t, p, `{"type":"party","local_id":"CUST-1","name":"Hof Nord"}`, nil)
	put(t, p, organization, nil)

	member := func(target string) string {
		return `{"type":"party","local_id":"PTY-1","name":"Anke Meyer",` +
			`"details":{"party_type":"PERSON","last_name":"Meyer","memberships":[` +
			`{"organization_id":{"local_id":"` + target + `"},"member_role":"OWNER"}]}}`
	}

	// A party of unknown party type might be a person; naming it is a 400.
	if err := putErr(t, p, member("CUST-1"), nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("membership naming a party of unknown party type: error = %v, want ErrValidation", err)
	}
	put(t, p, member("ORG-1"), nil)
}

func TestAnOrganizationPersonsNameKeepsItsKind(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeParty)
	base := revisionIn(t, put(t, p, organization, nil))
	put(t, p, `{"type":"party","local_id":"PTY-1","name":"Anke Meyer",`+
		`"details":{"party_type":"PERSON","memberships":[`+
		`{"organization_id":{"local_id":"ORG-1"},"member_role":"OWNER"}]}}`, nil)

	for name, body := range map[string]string{
		"changed": `{"type":"party","local_id":"ORG-1","name":"Agrar GmbH",` +
			`"details":{"party_type":"PERSON"}}`,
		"removed": `{"type":"party","local_id":"ORG-1","name":"Agrar GmbH","details":null}`,
	} {
		if err := putErr(t, p, body, &base); !errors.Is(err, agmasync.ErrValidation) {
			t.Errorf("party type %s on a named organization: error = %v, want ErrValidation", name, err)
		}
	}
}

func TestABoundaryNamesItsField(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFieldBoundary)

	boundary := `{"type":"fieldBoundary","local_id":"FB-1","field":{"local_id":"PFD-1"},` +
		`"boundary":{"type":"Polygon","coordinates":[[[8,52],[8.1,52],[8.1,52.1],[8,52]]]}}`
	if err := putErr(t, p, boundary, nil); !errors.Is(err, agmasync.ErrValidation) {
		t.Errorf("boundary naming an unsent field: error = %v, want ErrValidation", err)
	}

	put(t, p, `{"type":"field","local_id":"PFD-1","name":"North 40"}`, nil)
	put(t, p, boundary, nil)
}

func TestDeliveryOrderFollowsReferencesNotAge(t *testing.T) {
	// Each object is delivered after what it names, even where the target last
	// changed after the object naming it: an organization edited after its
	// member, a field edited after its boundary. Persons sit below the other
	// parties, boundaries below fields.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.EntityTypes...)

	orgBase := revisionIn(t, put(t, a, organization, nil))
	put(t, a, `{"type":"party","local_id":"PTY-1","name":"Anke Meyer",`+
		`"details":{"party_type":"PERSON","memberships":[`+
		`{"organization_id":{"local_id":"ORG-1"},"member_role":"OWNER"}]}}`, nil)
	put(t, a, `{"type":"party","local_id":"ORG-1","name":"Agrar AG",`+
		`"details":{"party_type":"ORGANIZATION"}}`, &orgBase)

	fieldBase := revisionIn(t, put(t, a, `{"type":"field","local_id":"PFD-1","name":"North 40"}`, nil))
	put(t, a, `{"type":"fieldBoundary","local_id":"FB-1","field":{"local_id":"PFD-1"},`+
		`"boundary":{"type":"Polygon","coordinates":[[[8,52],[8.1,52],[8.1,52.1],[8,52]]]}}`, nil)
	put(t, a, `{"type":"field","local_id":"PFD-1","name":"North 41"}`, &fieldBase)

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
			attributes := attributesOf(t, entity)
			var details struct {
				PartyType string `json:"party_type"`
			}
			_ = json.Unmarshal(attributes["details"], &details)
			label = details.PartyType
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
