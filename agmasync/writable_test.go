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
	"github.com/oapi-codegen/nullable"

	"github.com/google/uuid"
)

// captureBodies serves response to every request and hands back the first
// request body, decoded into its top-level fields.
func captureBodies(t *testing.T, response string) (*oapi.ClientWithResponses, <-chan map[string]json.RawMessage) {
	t.Helper()
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
			_, _ = w.Write([]byte(response))
		}))
	t.Cleanup(srv.Close)

	api, err := oapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return api, bodies
}

// TestPutSendsBackWhatAgrirouterAssigns pins the agrirouter-assigned
// properties, the agrirouter entry of `content.id` among them, into a write: a participant sends back what it was delivered, and
// agrirouter decides which of them to check and which to ignore.
func TestPutSendsBackWhatAgrirouterAssigns(t *testing.T) {
	api, bodies := captureBodies(t, `{"type":"party","content":{"id":{"referenceId":"o-1"},"name":"Hof Nord"}}`)
	actingEndpointID, actingTenantID := uuid.New(), uuid.New()

	localID := "o-1"
	revision := 7
	agrirouterID := uuid.New()
	tenantID := uuid.New()
	sourceEndpointID := uuid.New()
	modifiedAt := time.Now().UTC()

	// No type: Put sets it from the model. The identity is the one delivered,
	// the agrirouter entry included.
	source := agmasync.AgrirouterIDSource
	id := oapi.Id{ReferenceId: &localID, UniqueIds: &[]oapi.UniqueId{
		{IdText: agrirouterID.String(), IdTypeCode: agmasync.IDTypeUUID, IdSource: &source},
		{IdText: localID, IdTypeCode: agmasync.IDTypeString},
	}}
	party := oapi.Party{
		Content:  oapi.PartyContent{Id: id, Name: "Hof Nord"},
		Revision: &revision, TenantId: &tenantID,
		SourceEndpointId: &sourceEndpointID, ModifiedAt: &modifiedAt,
	}

	if _, err := agmasync.PutParty(context.Background(), api, actingEndpointID, actingTenantID, party, &revision); err != nil {
		t.Fatalf("Put: %v", err)
	}

	sent := <-bodies
	for name, want := range map[string]any{
		"modified_at":        modifiedAt,
		"revision":           revision,
		"source_endpoint_id": sourceEndpointID,
		"tenant_id":          tenantID,
		"type":               "party",
		"content":            oapi.PartyContent{Id: id, Name: "Hof Nord"},
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
// set on the model.
//
// A reference the participant does not hold must be left out rather than sent
// as `{}`, which agrirouter rejects as naming neither identifier. And a write is
// a merge patch, where null and left out mean opposite things — remove it, and
// leave it alone — so the body has to keep each as the caller set it through
// the model's nullable fields.
func TestPutSendsTheEntityAsTheParticipantBuiltIt(t *testing.T) {
	api, bodies := captureBodies(t,
		`{"type":"farm","content":{"id":{"referenceId":"f-1"},"name":"Hof Nord",`+
			`"_agmasync_growerRef":{"id":{"referenceId":"o-1"}}}}`)
	actingEndpointID, actingTenantID := uuid.New(), uuid.New()

	// A farm as a platform holds it: no grower, because the reference it was
	// delivered with resolved to nothing it holds, a usage type it has cleared,
	// and no address, which it does not model.
	active := true
	farm := oapi.Farm{
		Active: &active,
		Content: oapi.FarmContent{
			Id: agmasync.LocalID("f-1"), Name: "Hof Nord",
			UnderscoreAgmasyncSpecialisedUsageType: nullable.NewNullNullable[string](),
		},
	}

	if _, err := agmasync.PutFarm(context.Background(), api, actingEndpointID, actingTenantID, farm, nil); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var content map[string]json.RawMessage
	if err := json.Unmarshal((<-bodies)["content"], &content); err != nil {
		t.Fatalf("reading content: %v", err)
	}
	if raw, found := content["_agmasync_growerRef"]; found {
		t.Errorf("Put sent _agmasync_growerRef = %s, want a grower the participant does not hold left out", raw)
	}
	if raw, found := content["_agmasync_specialisedUsageType"]; !found || string(raw) != "null" {
		t.Errorf("_agmasync_specialisedUsageType = %s (sent %v), want null: it removes the attribute", raw, found)
	}
	if raw, found := content["_agmasync_address"]; found {
		t.Errorf("Put sent _agmasync_address = %s, want an attribute the participant left out left out", raw)
	}
}

// TestPutSetsTheTypeFromTheModel pins the discriminator: the models leave
// `type` to the caller, and an unset one would go out as null.
func TestPutSetsTheTypeFromTheModel(t *testing.T) {
	types := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			var sent struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &sent)
			types <- sent.Type
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"type":"` + sent.Type + `","content":{"id":{"referenceId":"x-1"}}}`))
		}))
	defer srv.Close()

	api, err := oapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, id, endpointID, tenantID := context.Background(), agmasync.LocalID("x-1"), uuid.New(), uuid.New()
	calls := map[agmasync.EntityType]func() error{
		agmasync.TypeParty: func() error {
			_, err := agmasync.PutParty(ctx, api, endpointID, tenantID, oapi.Party{Content: oapi.PartyContent{Id: id}}, nil)
			return err
		},
		agmasync.TypeFarm: func() error {
			_, err := agmasync.PutFarm(ctx, api, endpointID, tenantID, oapi.Farm{Content: oapi.FarmContent{Id: id}}, nil)
			return err
		},
		agmasync.TypeField: func() error {
			_, err := agmasync.PutField(ctx, api, endpointID, tenantID, oapi.Field{Content: oapi.FieldContent{Id: id}}, nil)
			return err
		},
		agmasync.TypeFieldBoundary: func() error {
			_, err := agmasync.PutFieldBoundary(ctx, api, endpointID, tenantID, oapi.FieldBoundary{Content: oapi.FieldBoundaryContent{Id: id}}, nil)
			return err
		},
	}
	for want, call := range calls {
		t.Run(string(want), func(t *testing.T) {
			if err := call(); err != nil {
				t.Fatalf("Put: %v", err)
			}
			if got := <-types; got != string(want) {
				t.Errorf("Put sent type %q, want %q", got, want)
			}
		})
	}
}
