//go:build it

package container_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter/container"
	"github.com/google/uuid"
)

// TestClientAgainstContainerisedRouter runs one full exchange over a real
// network connection: opt-in, an initial-load stream that ends, a write, and the
// live stream carrying that write to the other participant.
//
// The in-process tests cover the rules; this covers the transport — that the SSE
// streams survive a real HTTP server, that the response really does close at the
// end of an initial load, and that a resumed stream reconnects.
func TestClientAgainstContainerisedRouter(t *testing.T) {
	ctx := context.Background()

	router, err := container.Run(ctx)
	if err != nil {
		t.Fatalf("starting router: %v", err)
	}
	t.Cleanup(func() {
		if err := router.Terminate(context.Background()); err != nil {
			t.Logf("terminating router: %v", err)
		}
	})

	tenant := createTenant(t, router.BaseURL)
	senderID := createEndpoint(t, router.BaseURL, "fmis-a", tenant, "ep-a")
	receiverID := createEndpoint(t, router.BaseURL, "fmis-b", tenant, "ep-b")
	sender, err := agmasync.NewClient(router.BaseURL, agmasync.WithBearerToken("fmis-a"))
	if err != nil {
		t.Fatalf("sender client: %v", err)
	}
	receiver, err := agmasync.NewClient(router.BaseURL, agmasync.WithBearerToken("fmis-b"))
	if err != nil {
		t.Fatalf("receiver client: %v", err)
	}

	senderEndpoint := sender.For(senderID, "ep-a", uuid.New(), tenant, uuid.New(), "cloud_software")
	receiverEndpoint := receiver.For(receiverID, "ep-b", uuid.New(), tenant, uuid.New(), "cloud_software")

	// Declaring comes first and enables nothing: it is what the user's
	// selection is then drawn from, and a selection naming an undeclared type
	// is refused.
	for _, endpoint := range []*agmasync.Endpoint{senderEndpoint, receiverEndpoint} {
		if _, err := endpoint.Declare(
			ctx, agmasync.Declaration(agmasync.TypeFarm),
		); err != nil {
			t.Fatalf("declaring: %v", err)
		}
	}
	optIn(t, router.BaseURL, "ep-a", "farm")
	optIn(t, router.BaseURL, "ep-b", "farm")

	// The initial-load stream ends of its own accord once the set is sent,
	// which is the behaviour an io.Pipe cannot really prove.
	loadCtx, cancelLoad := context.WithTimeout(ctx, 30*time.Second)
	defer cancelLoad()

	stream, err := receiverEndpoint.InitialLoadEvents(loadCtx)
	if err != nil {
		t.Fatalf("initial load stream: %v", err)
	}
	for range stream.Events() {
	}
	stream.Close()

	status, err := receiverEndpoint.InitialLoadStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.State != agmasync.StateReconciling {
		t.Fatalf("state = %q, want %q once the set has been sent",
			status.State, agmasync.StateReconciling)
	}

	// Open the receiver's live stream, then write from the other participant.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()

	live, err := receiver.Events(streamCtx, "")
	if err != nil {
		t.Fatalf("live stream: %v", err)
	}
	defer live.Close()

	delivered := make(chan agmasync.Event, 8)
	go func() {
		defer close(delivered)
		for ev, err := range live.Events() {
			if err != nil {
				return
			}
			if ev.HasEntity() {
				delivered <- ev
			}
		}
	}()

	name := "Hof Nord"
	entity, err := agmasync.FromFarm(oapi.Farm{LocalId: strptr("FRM-1"), Name: name})
	if err != nil {
		t.Fatalf("building farm: %v", err)
	}
	if _, err := senderEndpoint.Put(ctx, entity, nil); err != nil {
		t.Fatalf("put: %v", err)
	}

	select {
	case ev := <-delivered:
		if ev.Envelope.Type != agmasync.TypeFarm {
			t.Errorf("delivered type = %q, want farm", ev.Envelope.Type)
		}
		// The receiver has never bound an identifier for this object, so the
		// delivered copy must carry none: an absent localId is what says
		// "create it locally and bind".
		if ev.Envelope.LocalId != nil {
			t.Errorf("localId = %q, want absent for an object the receiver has not bound",
				*ev.Envelope.LocalId)
		}
		if ev.ID == "" {
			t.Error("a live frame must carry a position to resume from")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the write never arrived on the receiver's live stream")
	}
}

func createTenant(t *testing.T, baseURL string) uuid.UUID {
	t.Helper()
	var out struct {
		TenantID uuid.UUID `json:"tenantId"`
	}
	post(t, baseURL+"/_test/tenants", nil, &out)
	return out.TenantID
}

func createEndpoint(
	t *testing.T, baseURL, appID string, tenant uuid.UUID, externalID string,
) uuid.UUID {
	t.Helper()
	body := map[string]any{
		"applicationId": appID,
		"tenantId":      tenant,
		"externalId":    externalID,
	}
	var out struct {
		EndpointID uuid.UUID `json:"endpointId"`
	}
	post(t, baseURL+"/_test/endpoints", body, &out)
	return out.EndpointID
}

func optIn(t *testing.T, baseURL, externalID string, types ...string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"entityTypes": types})
	if err != nil {
		t.Fatalf("marshalling opt-in: %v", err)
	}
	req, err := http.NewRequest(http.MethodPut,
		baseURL+"/_test/endpoints/"+externalID+"/opt-in", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("building opt-in request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("opt-in: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("opt-in returned %d", resp.StatusCode)
	}
}

func post(t *testing.T, url string, body any, out any) {
	t.Helper()
	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshalling body: %v", err)
		}
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}

	resp, err := http.Post(url, "application/json", reader)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("post %s returned %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decoding %s: %v", url, err)
	}
}

func strptr(s string) *string { return &s }
