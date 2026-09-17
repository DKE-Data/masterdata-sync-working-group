package scenarios

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func withdrawalWhileOffline() Scenario {
	return Scenario{
		Number: 13,
		Title:  "Masterdata route removal made while the participant was offline",
		Spec: []string{
			"Learning what an endpoint exchanges", "Routing and opt-in", "Downtime and resume",
		},
		Run: runWithdrawalWhileOffline,
	}
}

// runWithdrawalWhileOffline is the selection as a thing a participant is told
// rather than a thing it can read.
//
// There is no resource to poll: what an endpoint exchanges reaches it on
// ROUTE_CHANGED and nowhere else. Two properties carry the whole design, and a
// participant that assumes either away keeps exchanging data its user has
// switched off — the frame states the selection rather than a transition, and
// catch-up restates it for an endpoint whose selection was emptied, which is the
// only reason a withdrawal survives a disconnection at all.
func runWithdrawalWhileOffline(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if err := alpha.AddFarm("alpha-farm-1", "Hof Nord", "Kiel"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}

	beta, err := w.Join(ctx, "Beta FMIS", "fmis-beta", "beta")
	if err != nil {
		return err
	}
	if err := beta.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}

	say.Step("Beta's opt into farms and Beta learns about it.")
	selected, err := beta.Selection(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(selected) == 3,
		"three entity types are routed: %s", names(selected)); err != nil {
		return err
	}
	if _, err := beta.Load(ctx); err != nil {
		return err
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	say.Detail("Beta performs initial load")

	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if len(held) != 1 {
		return fmt.Errorf("Beta holds %d farms, want Alpha's", len(held))
	}
	betaFarm := held[0]

	say.Step("Then Beta goes down for a month, and its user removes the masterdata route.")
	if err := beta.OptOut(ctx); err != nil {
		return err
	}

	say.Step("Beta comes back and connects from where it left off.")
	frames, err := beta.Selections(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(frames) == 1,
		"one ROUTE_CHANGED event is delivered"); err != nil {
		return err
	}
	if err := say.Check(len(frames[0].EntityTypes) == 0,
		"stating an empty selection rather than not arriving at all"); err != nil {
		return err
	}

	withdrawn, err := beta.Selection(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(withdrawn) == 0,
		"so Beta reads its selection as empty, and stops offering anything"); err != nil {
		return err
	}

	_, err = beta.Send(ctx, agmasync.TypeFarm, betaFarm)
	if err := say.Check(errors.Is(err, agmasync.ErrForbidden),
		"its next write is refused — the endpoint is opted into nothing"); err != nil {
		return err
	}

	return nil
}

// names renders entity types for narration.
func names(types []agmasync.EntityType) string {
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, string(t))
	}
	return strings.Join(out, ", ")
}
