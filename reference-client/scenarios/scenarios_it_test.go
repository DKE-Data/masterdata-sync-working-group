//go:build it

package scenarios_test

import (
	"context"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter/container"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/scenarios"
)

// TestScenariosAgainstContainerisedRouter runs the same scenarios against the
// test router in Docker.
//
// It is the same scenario code and the same claims; what changes is the
// transport. In process the streams are an io.Pipe, which cannot show whether a
// long-lived SSE response survives a real server, whether an initial-load
// response really closes, or whether a resumed stream reconnects over a socket.
// Here they can.
//
// They all share one router, as they do in process: each scenario builds its
// own tenant and its own participants, so they do not see each other.
func TestScenariosAgainstContainerisedRouter(t *testing.T) {
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

	for _, scenario := range scenarios.All() {
		t.Run(scenario.Title, func(t *testing.T) {
			if err := scenario.Execute(ctx, router.BaseURL, func(line string) {
				t.Log(line)
			}); err != nil {
				t.Error(err)
			}
		})
	}
}
