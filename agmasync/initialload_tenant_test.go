package agmasync_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// recordTenantHeader answers like agrirouter does with or without the tenant
// header, and hands back the header as the first request sent it.
func recordTenantHeader(t *testing.T, header string) (*oapi.ClientWithResponses, <-chan string) {
	t.Helper()
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
			// One body for every call under test, since what is asserted here
			// is the request rather than the response. The stream still has
			// to answer as one.
			if strings.HasSuffix(r.URL.Path, "/events") {
				w.Header().Set("Content-Type", "text/event-stream")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"state":"COMPLETED","entity_types":[]}`))
		}))
	t.Cleanup(srv.Close)

	api, err := oapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return api, seen
}

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

	api, seen := recordTenantHeader(t, header)
	const externalEndpointID = "refclient:tenant:x:alpha"

	ctx := context.Background()
	calls := map[string]func() error{
		"GetInitialLoadStatus": func() error {
			_, err := agmasync.GetInitialLoadStatus(ctx, api, externalEndpointID, tenant)
			return err
		},
		"SetInitialLoadState": func() error {
			_, err := agmasync.SetInitialLoadState(ctx, api, externalEndpointID, tenant, oapi.InitialLoadStateUpdate{
				State: agmasync.StateCompleted,
			})
			return err
		},
		"ReportUserAttention": func() error {
			_, err := agmasync.ReportUserAttention(ctx, api, externalEndpointID, tenant)
			return err
		},
		"InitialLoadEvents": func() error {
			stream, err := agmasync.InitialLoadEvents(ctx, api, externalEndpointID, tenant)
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
