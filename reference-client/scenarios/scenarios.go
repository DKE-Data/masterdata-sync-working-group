package scenarios

import (
	"context"
	"fmt"
)

// All returns the scenarios in order.
//
// They are independent: each builds its own tenant and onboards its own
// participants, so one can be run alone and none inherits another's state.
func All() []Scenario {
	return []Scenario{
		onboarding(),
		repeatLoad(),
		concurrentEdit(),
		unbindAndRecreate(),
		deactivatedInTheSet(),
		resumeAfterDowntime(),
		mappingRejections(),
		blockedReconciliation(),
		requestedObjects(),
		preservedAttributes(),
		stricterRecipient(),
		withdrawalWhileOffline(),
		splitAndMerge(),
		unresolvedReference(),
	}
}

// ByNumber returns one scenario.
func ByNumber(number int) (Scenario, bool) {
	for _, s := range All() {
		if s.Number == number {
			return s, true
		}
	}
	return Scenario{}, false
}

// RunAll executes every scenario against the router at baseURL, printing each
// as it goes. It runs them all even where one fails, because a reader watching
// the output learns more from the rest than from an early exit.
func RunAll(ctx context.Context, baseURL string, print func(string)) error {
	return RunAllWith(ctx, baseURL, print, nil)
}

// RunAllWith is [RunAll] with somebody to answer the questions a scenario asks.
func RunAllWith(ctx context.Context, baseURL string, print func(string), ask Asker) error {
	var failed []int
	for _, s := range All() {
		if err := s.ExecuteWith(ctx, baseURL, print, ask); err != nil {
			failed = append(failed, s.Number)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("scenarios failed: %v", failed)
	}
	return nil
}
