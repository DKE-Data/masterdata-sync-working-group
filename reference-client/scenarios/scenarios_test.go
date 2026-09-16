package scenarios_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/scenarios"
)

// TestScenarios runs every scenario against the in-process test router.
//
// The scenarios are the assertions: each claim a scenario prints is checked as
// it is printed, so there is nothing to duplicate here. The narration goes to
// the test log, where `go test -v ./scenarios` reads exactly as
// `go run ./cmd/scenarios` does.
func TestScenarios(t *testing.T) {
	srv := httptest.NewServer(testrouter.New().Handler())
	t.Cleanup(srv.Close)

	for _, scenario := range scenarios.All() {
		t.Run(scenario.Title, func(t *testing.T) {
			if err := scenario.Execute(context.Background(), srv.URL, func(line string) {
				t.Log(line)
			}); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestScenariosAnswerTheOtherWay runs the scenarios again with every question
// answered with its last choice rather than its first.
//
// A scenario that asks something has two outcomes, and the defaults only ever
// exercise one. Both have to hold: the question is real — which of a platform's
// records a canonical object is — and a story that only works when the person
// picks the first option is not telling the truth about it.
func TestScenariosAnswerTheOtherWay(t *testing.T) {
	srv := httptest.NewServer(testrouter.New().Handler())
	t.Cleanup(srv.Close)

	last := func(_ string, choices []string) (string, error) {
		return choices[len(choices)-1], nil
	}

	for _, scenario := range scenarios.All() {
		t.Run(scenario.Title, func(t *testing.T) {
			if err := scenario.ExecuteWith(context.Background(), srv.URL, func(line string) {
				t.Log(line)
			}, last); err != nil {
				t.Error(err)
			}
		})
	}
}
