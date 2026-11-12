package scenarios

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func unarchived() Scenario {
	return Scenario{
		Number: 20,
		Title:  "An archived farm is corrected, then unarchived",
		Spec: []string{
			"Lifecycle", "Unarchiving", "Receiving an archived object", "Archiving",
		},
		Run: runUnarchived,
	}
}

// runUnarchived is the lifecycle after archiving: an archived entity can
// still be corrected without being revived, and coming back is an operation of
// its own rather than a side effect of a write.
func runUnarchived(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	say.Step("Alpha shares Hof Alt.")
	if err := alpha.AddFarm("alpha-farm-1", "Hof Alt", "Kiel"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}

	say.Step("Beta onboards and takes Hof Alt as a farm of its own.")
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
	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if len(held) != 1 {
		return fmt.Errorf("Beta holds %d farms, want Hof Alt", len(held))
	}
	betaFarm := held[0]

	say.Step("Alpha archives Hof Alt.")
	if _, err := alpha.Archive(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	record, err := beta.Record(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	if err := say.Check(record.Archived,
		"Beta receives it as an ordinary change carrying archived true, and archives its copy"); err != nil {
		return err
	}

	say.Step("Alpha corrects the archived farm's name.")
	if err := alpha.Edit(agmasync.TypeFarm, "alpha-farm-1", map[string]any{"name": "Hof Alt (verkauft)"}); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	say.Detail("the write states the state the farm is in, archived; a write that")
	say.Detail("said not archived would be refused, since only unarchiving revives")
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	record, err = beta.Record(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	if err := say.Check(string(record.Modelled["name"]) == `"Hof Alt (verkauft)"`,
		"Beta takes the correction"); err != nil {
		return err
	}
	if err := say.Check(record.Archived,
		"and its copy stays archived: correcting an archived entity does not revive it"); err != nil {
		return err
	}

	say.Step("Alpha unarchives Hof Alt.")
	if _, err := alpha.Unarchive(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	record, err = beta.Record(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	return say.Check(!record.Archived,
		"Beta receives it no longer archived and restores its copy")
}
