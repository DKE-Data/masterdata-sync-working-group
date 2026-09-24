package scenarios

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

func masterdataReset() Scenario {
	return Scenario{
		Number: 16,
		Title:  "A masterdata reset made while the participant was offline",
		Spec: []string{
			"Masterdata reset", "Identifier mapping", "Downtime and resume",
		},
		Run: runMasterdataReset,
	}
}

// runMasterdataReset is the one hard removal from the SSOT, seen from a
// participant that was away for it.
//
// The user wipes the tenant's master data in agrirouter: canonical objects,
// every participant's pairs, routes and initial-load state. The participant
// keeps its records and drops its pairs, and the frame's position is held with
// the discard, so a resume cannot deliver the reset again after the next load
// has bound anything.
func runMasterdataReset(ctx context.Context, w *World) error {
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
	if _, err := beta.Load(ctx); err != nil {
		return err
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if len(held) != 1 {
		return fmt.Errorf("Beta holds %d farms, want Alpha's", len(held))
	}
	betaFarm := held[0]
	before, err := beta.Row(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	say.Step("Beta holds Alpha's farm as %s, bound to %s.", betaFarm, short(*before.AgrirouterID))

	say.Step("Beta goes down. Its user wipes the tenant's master data in agrirouter, then routes Beta to farms again.")
	if err := w.Reset(ctx); err != nil {
		return err
	}
	if err := beta.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}

	say.Step("Beta comes back and connects from where it left off.")
	frames, err := beta.Frames(ctx)
	if err != nil {
		return err
	}
	var order []string
	for _, ev := range frames {
		order = append(order, ev.Type)
	}
	if err := say.Check(len(order) == 2 &&
		order[0] == agmasync.EventMasterdataReset && order[1] == agmasync.EventRouteChanged,
		"RESET_MASTERDATA_SYNC arrives first, then the new route: %v", order); err != nil {
		return err
	}

	// Read off the frame before catching up moves past it.
	types, err := beta.Selection(ctx)
	if err != nil {
		return err
	}

	resets := 0
	beta.Receiver.OnReset = func(_ *store.Tx, _ oapi.MasterdataResetEventData, discarded int) error {
		resets++
		say.Detail("Beta drops %d binding(s) and keeps its records", discarded)
		return nil
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}

	_, err = beta.Row(agmasync.TypeFarm, betaFarm)
	if err := say.Check(errors.Is(err, store.ErrNotFound),
		"the pair for %s is gone: the agrirouterId it named no longer exists", betaFarm); err != nil {
		return err
	}
	if _, err := beta.Record(agmasync.TypeFarm, betaFarm); err != nil {
		return say.Check(false, "the farm itself is still Beta's: %v", err)
	}
	say.Detail("the farm itself is still Beta's")

	say.Step("Beta takes part in the load the new route started.")
	res, err := beta.LoadSelected(ctx, types)
	if err != nil {
		return err
	}
	if err := say.Check(!res.Repeat,
		"it is a first load: the reset took the repeat marker with the pairs"); err != nil {
		return err
	}
	after, err := beta.Row(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	if err := say.Check(after.Bound() && *after.AgrirouterID != *before.AgrirouterID,
		"the SSOT was empty, so Beta sent its farm as new: bound to %s", short(*after.AgrirouterID)); err != nil {
		return err
	}

	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	return say.Check(resets == 1,
		"reconnecting does not repeat the reset: its position was held with the discard")
}
