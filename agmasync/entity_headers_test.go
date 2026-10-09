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

// recordHeaders answers every request with a party and hands back the first
// request's headers.
func recordHeaders(t *testing.T) (*oapi.ClientWithResponses, <-chan http.Header) {
	t.Helper()
	seen := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			select {
			case seen <- r.Header.Clone():
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			// Enough of a party to be read back as one, every call
			// under test being about the request rather than the response.
			_, _ = w.Write([]byte(`{"type":"party","local_id":"o-1","name":"Hof Nord"}`))
		}))
	t.Cleanup(srv.Close)

	api, err := oapi.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return api, seen
}

func TestEntityCallsNameTheEndpointAndTheTenant(t *testing.T) {
	const (
		endpointHeader = "X-Agrirouter-Endpoint-Id"
		tenantHeader   = "X-Agrirouter-Tenant-Id"
	)
	endpointID := uuid.MustParse("1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed")
	tenant := uuid.MustParse("6f1a4dcb-4a1a-4b1e-9d9a-5a0e6a2c2f11")

	api, seen := recordHeaders(t)

	localID := "o-1"
	party := oapi.Party{LocalId: &localID, Name: "Hof Nord"}

	ctx := context.Background()
	calls := map[string]func() error{
		"Put": func() error {
			_, err := agmasync.PutParty(ctx, api, endpointID, tenant, party, nil)
			return err
		},
		"Bind": func() error {
			return agmasync.Bind(ctx, api, endpointID, tenant, agmasync.TypeParty, "o-1", uuid.New())
		},
		"Unbind": func() error {
			return agmasync.Unbind(ctx, api, endpointID, tenant, agmasync.TypeParty, "o-1", uuid.New())
		},
		"Deactivate": func() error {
			_, err := agmasync.Deactivate(ctx, api, endpointID, tenant, agmasync.TypeParty, "o-1", nil)
			return err
		},
		"Request": func() error {
			return agmasync.Request(ctx, api, endpointID, tenant, agmasync.TypeParty, uuid.New())
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			if err := call(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			headers := <-seen
			for header, want := range map[string]uuid.UUID{
				endpointHeader: endpointID,
				tenantHeader:   tenant,
			} {
				// Indexed, not Get: the map key is the canonical spelling, so a
				// header sent under any other name is simply absent here.
				got := headers[header]
				if len(got) != 1 || got[0] != want.String() {
					t.Errorf("%s sent %s = %v, want [%s]", name, header, got, want)
				}
			}
		})
	}
}
