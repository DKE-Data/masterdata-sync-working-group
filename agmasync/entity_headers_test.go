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

func TestEntityCallsNameTheEndpointAndTheTenant(t *testing.T) {
	const (
		endpointHeader = "X-Agrirouter-Endpoint-Id"
		tenantHeader   = "X-Agrirouter-Tenant-Id"
	)
	endpointID := uuid.MustParse("1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed")
	tenant := uuid.MustParse("6f1a4dcb-4a1a-4b1e-9d9a-5a0e6a2c2f11")

	seen := make(chan http.Header, 1)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			select {
			case seen <- r.Header.Clone():
			default:
			}
			w.Header().Set("Content-Type", "application/json")
			// Enough of an organization to be read back as one, every call
			// under test being about the request rather than the response.
			_, _ = w.Write([]byte(`{"type":"organization","local_id":"o-1","name":"Hof Nord"}`))
		}))
	defer srv.Close()

	client, err := agmasync.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := client.For(endpointID, "refclient:tenant:x:alpha",
		uuid.New(), tenant, uuid.New(), oapi.EndpointTypeToCreate("cloud_software"))

	localID := "o-1"
	organization, err := agmasync.FromOrganization(oapi.Organization{
		Type: "organization", LocalId: &localID, Name: "Hof Nord",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	calls := map[string]func() error{
		"Put": func() error {
			_, err := endpoint.Put(ctx, organization, nil)
			return err
		},
		"Bind": func() error {
			return endpoint.Bind(ctx, agmasync.TypeOrganization, "o-1", uuid.New())
		},
		"Unbind": func() error {
			return endpoint.Unbind(ctx, agmasync.TypeOrganization, "o-1", uuid.New())
		},
		"Deactivate": func() error {
			_, err := endpoint.Deactivate(ctx, agmasync.TypeOrganization, "o-1", nil)
			return err
		},
		"Request": func() error {
			return endpoint.Request(ctx, agmasync.TypeOrganization, uuid.New())
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
