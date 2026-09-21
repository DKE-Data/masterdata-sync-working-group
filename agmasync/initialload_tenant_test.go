package agmasync_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// TestInitialLoadCallsNameTheTenant pins the tenant header onto every
// initial-load call.
//
// The deployed g4 API refuses anything under /endpoints/ that does not name a
// tenant — 400 "tenant ID header is missing in request" — because those paths
// address an endpoint by the application's own external id, which is unique per
// tenant rather than agrirouter-wide. openapi.yaml declares the header on
// PutEndpoint alone, so nothing in the generated client sends it here and the
// whole initial load fails at its first call.
func TestInitialLoadCallsNameTheTenant(t *testing.T) {
	const header = "x-agrirouter-tenant-id"
	tenant := uuid.MustParse("6f1a4dcb-4a1a-4b1e-9d9a-5a0e6a2c2f11")

	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			select {
			case seen <- r.Header.Get(header):
			default:
			}
			if r.Header.Get(header) == "" {
				// What a live agrirouter answers, so a client that stops
				// sending the header fails here the way it fails there.
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(
					`{"message":"tenant ID header is missing in request"}`))
				return
			}
			// One body for every call under test: the fields an initial-load
			// status is read out of and the ones an endpoint is, since what is
			// asserted here is the request rather than the response.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"state":"COMPLETED","entity_types":[],` +
					`"masterdata":{"capabilities":[]}}`))
		}))
	defer srv.Close()

	client, err := agmasync.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := client.For(uuid.New(), "refclient:tenant:x:alpha",
		uuid.New(), tenant, uuid.New(), oapi.EndpointTypeToCreate("cloud_software"))

	ctx := context.Background()
	calls := map[string]func() error{
		"InitialLoadStatus": func() error {
			_, err := endpoint.InitialLoadStatus(ctx)
			return err
		},
		"SetInitialLoadState": func() error {
			_, err := endpoint.SetInitialLoadState(ctx, oapi.InitialLoadStateUpdate{
				State: agmasync.StateCompleted,
			})
			return err
		},
		// Declared here too: openapi.yaml does name the tenant on PutEndpoint,
		// but under the spelling the deployed API does not read.
		"Declare": func() error {
			_, err := endpoint.Declare(ctx, oapi.MasterdataConfig{})
			return err
		},
		"ReportUserAttention": func() error {
			_, err := endpoint.ReportUserAttention(ctx)
			return err
		},
		"InitialLoadEvents": func() error {
			stream, err := endpoint.InitialLoadEvents(ctx)
			if err != nil {
				return err
			}
			return stream.Close()
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if got := <-seen; got != tenant.String() {
				t.Errorf("%s sent %s = %q, want %q", name, header, got, tenant)
			}
		})
	}
}
