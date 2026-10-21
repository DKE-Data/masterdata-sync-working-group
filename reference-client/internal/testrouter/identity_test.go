package testrouter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/agrirouter"
	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
)

// An object's identity is its `content.id`. See "Identity" in
// specification.md.

// putRaw sends a farm's JSON under a path of the caller's choosing, past
// everything the client would check or derive on the way.
func putRaw(t *testing.T, f *fixture, appID string, p *participant, localID, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut,
		f.server.URL+"/masterdata/farms/"+localID, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+appID)
	req.Header.Set("X-Agrirouter-Endpoint-Id", p.endpoint.ID().String())
	req.Header.Set("X-Agrirouter-Tenant-Id", p.endpoint.TenantID().String())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put over HTTP: %v", err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func TestAWriteIsAddressedByItsReferenceId(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm)

	for name, write := range map[string]string{
		"another referenceId": body("farm", "FRM-2", `"name":"Hof Nord"`),
		"no id":               `{"type":"farm","content":{"name":"Hof Nord"}}`,
		"no content":          `{"type":"farm"}`,
		"a local entry from agrirouter's source": `{"type":"farm","content":{"id":{"referenceId":"FRM-1",` +
			`"uniqueIds":[{"idText":"FRM-1","idTypeCode":"STRING","idSource":"https://agrirouter.com"}]},` +
			`"name":"Hof Nord"}}`,
	} {
		if status := putRaw(t, f, "fmis-a", p, "FRM-1", write); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, status)
		}
	}
	if status := putRaw(t, f, "fmis-a", p, "FRM-1", body("farm", "FRM-1", `"name":"Hof Nord"`)); status != http.StatusCreated {
		t.Errorf("status = %d, want 201", status)
	}
}

func TestASentIdHoldsOnlyTheLocalAndTheAgrirouterEntry(t *testing.T) {
	f := newFixture(t)
	p := f.join("fmis-a", "ep-a", agmasync.TypeFarm, agmasync.TypeField)

	local := `{"idText":"FRM-1","idTypeCode":"STRING"}`
	legacy := `{"idText":"LEGACY-9","idTypeCode":"STRING","idSource":"https://legacy.example.com"}`
	for name, uniqueIDs := range map[string]string{
		"another entry":          local + "," + legacy,
		"another entry alone":    legacy,
		"two local entries":      local + "," + local,
		"two agrirouter entries": agrirouterEntry(uuid.New()) + "," + agrirouterEntry(uuid.New()),
		"a non-UUID agrirouter entry": `{"idText":"FRM-9","idTypeCode":"UUID",` +
			`"idSource":"https://agrirouter.com"}`,
	} {
		write := `{"type":"farm","content":{"id":{"referenceId":"FRM-1","uniqueIds":[` + uniqueIDs + `]},` +
			`"name":"Hof Nord"}}`
		if status := putRaw(t, f, "fmis-a", p, "FRM-1", write); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, status)
		}
	}
	write := `{"type":"farm","content":{"id":{"referenceId":"FRM-1","uniqueIds":[` + local + `]},` +
		`"name":"Hof Nord"}}`
	if status := putRaw(t, f, "fmis-a", p, "FRM-1", write); status != http.StatusCreated {
		t.Fatalf("status = %d, want 201", status)
	}

	// A reference's id follows the same rule.
	_, err := p.endpoint.PutJSON(context.Background(), []byte(body("field", "PFD-1",
		`"name":"North 40","_agmasync_farmRef":{"id":{"referenceId":"FRM-1","uniqueIds":[`+
			local+","+legacy+`]}}`)), nil)
	var status *agrirouter.StatusError
	if !errors.As(err, &status) || status.StatusCode != http.StatusBadRequest {
		t.Errorf("reference with another entry: error = %v, want HTTP 400", err)
	}
}

func TestTheLocalEntryIsRelayedToItsWriterAlone(t *testing.T) {
	// A participant's own idTypeCode and idSource come back to it as its latest
	// write of the object sent them, in the object's id and in references to
	// it. Another participant sees its own identifier for the object, or none.
	f := newFixture(t)
	a := f.join("fmis-a", "ep-a", agmasync.TypeFarm, agmasync.TypeField)
	b := f.join("fmis-b", "ep-b", agmasync.TypeFarm)

	source := "https://fmis-a.example.com"
	ownID := oapi.Id{ReferenceId: strptr("4711"), UniqueIds: &[]oapi.UniqueId{
		{IdText: "4711", IdTypeCode: "LONGINT", IdSource: &source},
	}}
	written, err := agrirouter.FromFarm(oapi.Farm{Content: oapi.FarmContent{Id: ownID, Name: "Hof Nord"}})
	if err != nil {
		t.Fatal(err)
	}
	created, err := a.endpoint.Put(context.Background(), written, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env, _ := agrirouter.EnvelopeOf(created)
	farmID := *env.AgrirouterID

	relayed := `{"referenceId":"4711","uniqueIds":[` + agrirouterEntry(farmID) + `,` +
		`{"idSource":"https://fmis-a.example.com","idText":"4711","idTypeCode":"LONGINT"}]}`
	if got := idOf(t, attributesOf(t, created)["id"]); got != relayed {
		t.Errorf("id = %s, want %s", got, relayed)
	}

	field, err := agrirouter.FromField(oapi.Field{Content: oapi.FieldContent{
		Id: agmasync.LocalID("PFD-1"), Name: "North 40",
		UnderscoreAgmasyncFarmRef: nullable.NewNullableWithValue(agmasync.LocalRef("4711")),
	}})
	if err != nil {
		t.Fatal(err)
	}
	withRef, err := a.endpoint.Put(context.Background(), field, nil)
	if err != nil {
		t.Fatalf("field: %v", err)
	}
	var ref struct {
		ID json.RawMessage `json:"id"`
	}
	_ = json.Unmarshal(attributesOf(t, withRef)["_agmasync_farmRef"], &ref)
	if got := idOf(t, ref.ID); got != relayed {
		t.Errorf("reference id = %s, want %s", got, relayed)
	}

	if err := b.endpoint.Bind(context.Background(), agmasync.TypeFarm, "B-1", farmID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	seen, err := b.endpoint.Put(context.Background(), farm("B-1", "Hof Nord"), nil)
	if err != nil {
		t.Fatalf("no-op write: %v", err)
	}
	want := `{"referenceId":"B-1","uniqueIds":[` + agrirouterEntry(farmID) + `,` +
		`{"idText":"B-1","idTypeCode":"STRING"}]}`
	if got := idOf(t, attributesOf(t, seen)["id"]); got != want {
		t.Errorf("B's id = %s, want %s", got, want)
	}

	// A write without a local entry resets it.
	base := revisionOf(t, created)
	renamed, err := a.endpoint.Put(context.Background(), farm("4711", "Hof Süd"), &base)
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	want = `{"referenceId":"4711","uniqueIds":[` + agrirouterEntry(farmID) + `,` +
		`{"idText":"4711","idTypeCode":"STRING"}]}`
	if got := idOf(t, attributesOf(t, renamed)["id"]); got != want {
		t.Errorf("id after a write without a local entry = %s, want %s", got, want)
	}
}

func agrirouterEntry(id uuid.UUID) string {
	return `{"idSource":"https://agrirouter.com","idText":"` + id.String() + `","idTypeCode":"UUID"}`
}

// idOf re-encodes an id with sorted keys, so it compares by meaning.
func idOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("reading id %s: %v", raw, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
