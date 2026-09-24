package agmasync_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/google/uuid"
)

// TestPutSendsBackWhatAgrirouterAssigns pins the six agrirouter-assigned
// properties into a write: a participant sends back what it was delivered, and
// agrirouter decides which of them to check and which to ignore.
func TestPutSendsBackWhatAgrirouterAssigns(t *testing.T) {
	bodies := make(chan map[string]json.RawMessage, 1)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Errorf("request body is not a JSON object: %v", err)
			}
			select {
			case bodies <- fields:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"organization","local_id":"o-1","name":"Hof Nord"}`))
		}))
	defer srv.Close()

	client, err := agmasync.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := client.For(uuid.New(), "refclient:tenant:x:alpha",
		uuid.New(), uuid.New(), uuid.New(), oapi.EndpointTypeToCreate("cloud_software"))

	localID := "o-1"
	revision := 7
	agrirouterID := openapi_types.UUID(uuid.New())
	tenantID := openapi_types.UUID(uuid.New())
	sourceEndpointID := openapi_types.UUID(uuid.New())
	modifiedAt := time.Now().UTC()

	organization, err := agmasync.FromOrganization(oapi.Organization{
		Type: "organization", LocalId: &localID, Name: "Hof Nord",
		AgrirouterId: &agrirouterID, Revision: &revision, TenantId: &tenantID,
		SourceEndpointId: &sourceEndpointID, ModifiedAt: &modifiedAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := endpoint.Put(context.Background(), organization, &revision); err != nil {
		t.Fatalf("Put: %v", err)
	}

	sent := <-bodies
	for name, want := range map[string]any{
		"agrirouter_id":      agrirouterID,
		"modified_at":        modifiedAt,
		"revision":           revision,
		"source_endpoint_id": sourceEndpointID,
		"tenant_id":          tenantID,
		"type":               "organization",
		"local_id":           localID,
		"name":               "Hof Nord",
	} {
		wantRaw, err := json.Marshal(want)
		if err != nil {
			t.Fatal(err)
		}
		if raw, found := sent[name]; !found {
			t.Errorf("Put did not send %s, want it sent back as delivered", name)
		} else if string(raw) != string(wantRaw) {
			t.Errorf("Put sent %s = %s, want %s", name, raw, wantRaw)
		}
	}
}

// TestPutSendsTheEntityAsTheParticipantBuiltIt pins the body to what the caller
// passed, which the generated models cannot be trusted to reproduce.
//
// Two ways they do not. A required reference the participant does not hold is
// invented, the generated Farm carrying its owner by value: an absent owner
// marshals as `{"type":""}`, and agrirouter answers 400 at `/owner`, "doesn't
// match any schema from anyOf", because the reference names neither identifier.
// A participant reading that has to work back from an owner it never sent to
// the owner it does not have. And an attribute the model does not name is
// dropped, since a generated struct has nowhere to put it — which would quietly
// undo the relaying of unmodelled attributes that the specification requires.
func TestPutSendsTheEntityAsTheParticipantBuiltIt(t *testing.T) {
	bodies := make(chan map[string]json.RawMessage, 1)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Errorf("request body is not a JSON object: %v", err)
			}
			select {
			case bodies <- fields:
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"type":"farm","local_id":"f-1","name":"Hof Nord",` +
					`"owner":{"type":"organization","local_id":"o-1"}}`))
		}))
	defer srv.Close()

	client, err := agmasync.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := client.For(uuid.New(), "refclient:tenant:x:alpha",
		uuid.New(), uuid.New(), uuid.New(), oapi.EndpointTypeToCreate("cloud_software"))

	// A farm as a platform holds it: no owner, because the reference it was
	// delivered with resolved to nothing it holds, and one attribute it does not
	// model and is relaying.
	var farm oapi.Entity
	if err := farm.UnmarshalJSON([]byte(
		`{"type":"farm","local_id":"f-1","active":true,"name":"Hof Nord",` +
			`"cadastral_district":"Husum"}`)); err != nil {
		t.Fatal(err)
	}

	if _, err := endpoint.Put(context.Background(), farm, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}

	sent := <-bodies
	if raw, found := sent["owner"]; found {
		t.Errorf("Put sent owner = %s, want an owner the participant does not hold left out", raw)
	}
	if raw, found := sent["cadastral_district"]; !found {
		t.Error("Put dropped an unmodelled attribute the participant was relaying")
	} else if string(raw) != `"Husum"` {
		t.Errorf("cadastral_district = %s, want it relayed unchanged", raw)
	}
}
