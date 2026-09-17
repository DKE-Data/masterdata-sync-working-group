package scenarios

import (
	"context"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func lostOwnObject() Scenario {
	return Scenario{
		Number: 9,
		Title:  "An object asked back for after the endpoint that wrote it lost it",
		Spec: []string{
			"Requesting objects (lazy loading)", "Loop prevention", "Identifier mapping",
		},
		Run: runLostOwnObject,
	}
}

// runLostOwnObject is what a participant does when it knows an object exists
// and does not hold it, having been the last to write it.
//
// Delivery is not holding, and here the two come apart in the direction nothing
// on the stream will repair: origin suppression keeps an endpoint's own
// revisions off its stream because it already holds them, which is exactly what
// stopped being true. Waiting does not fix it, and taking the whole set again
// over one object is not proportionate.
func runLostOwnObject(ctx context.Context, w *World) error {
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
	say.Step("Alpha loses its own farm: a restore that missed the records.")
	if err := alpha.Forget(agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	held, err := alpha.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if err := say.Check(len(held) == 0,
		"the record is gone"); err != nil {
		return err
	}
	say.Detail("agrirouter is not told.")

	farmRow, err := alpha.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err != nil {
		return err
	}
	if err := say.Check(farmRow.AgrirouterID != nil,
		"the binding survived it, so Alpha knows which object it is missing"); err != nil {
		return err
	}
	canonicalFarm := *farmRow.AgrirouterID
	say.Detail("bookkeeping and records are not the same tables, and a partial restore")
	say.Detail("could restore just one of the two.")

	waiting, err := alpha.Deliveries(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(waiting) == 0,
		"nothing is waiting on Alpha's stream to restore it from"); err != nil {
		return err
	}
	say.Detail("nor ever will be. The farm's current revision is Alpha's own write, and")
	say.Detail("origin suppression keeps an endpoint's own revisions off its stream.")

	say.Step("So Alpha asks for it, by the identifier it kept.")
	restored, err := alpha.Fetch(ctx, agmasync.TypeFarm, canonicalFarm)
	if err != nil {
		return err
	}
	if err := say.Check(restored.Received == 1,
		"the object it wrote itself is delivered to it"); err != nil {
		return err
	}
	if err := say.Check(alpha.Attr(agmasync.TypeFarm, "alpha-farm-1", "name") == "Hof Nord",
		"and it lands back under Alpha's own identifier, %q.", "alpha-farm-1"); err != nil {
		return err
	}
	if err := say.Check(restored.Created == 0 && restored.Bound == 0,
		"in agrirouter nothing new is created or bound"); err != nil {
		return err
	}
	return nil
}
