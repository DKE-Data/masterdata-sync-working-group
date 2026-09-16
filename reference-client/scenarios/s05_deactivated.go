package scenarios

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func deactivatedInTheSet() Scenario {
	return Scenario{
		Number: 5,
		Title:  "A deactivated object in the canonical set, recognised and not",
		Spec: []string{
			"Deactivated objects are part of the set", "Deactivation", "Initial load",
		},
		Run: runDeactivatedInTheSet,
	}
}

// runDeactivatedInTheSet is why inactive objects are part of a canonical set at
// all.
//
// Leaving them out would be tidier and wrong in both directions: an endpoint
// holding its own copy of an archived entity would find it missing from the set,
// offer it back as new, and resurrect for everybody something a user archived.
func runDeactivatedInTheSet(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	say.Step("Alpha shares two farms, Hof Nord and Hof Alt.")
	for _, farm := range []struct{ id, name string }{
		{"alpha-farm-1", "Hof Nord"},
		{"alpha-farm-2", "Hof Alt"},
	} {
		if err := alpha.AddFarm(farm.id, farm.name, "Kiel"); err != nil {
			return err
		}
		if _, err := alpha.Send(ctx, agmasync.TypeFarm, farm.id); err != nil {
			return err
		}
	}

	say.Step("Alpha archives both of its farms.")
	for _, id := range []string{"alpha-farm-1", "alpha-farm-2"} {
		if _, err := alpha.Deactivate(ctx, agmasync.TypeFarm, id); err != nil {
			return err
		}
	}
	say.Detail("deactivation is a lifecycle transition, not a removal: the objects and")
	say.Detail("their identifier mappings are kept, so references stay intact")

	say.Step("Beta onboards, holding its own record of one of them.")
	beta, err := w.Join(ctx, "Beta FMIS", "fmis-beta", "beta")
	if err != nil {
		return err
	}
	if err := beta.AddFarm("beta-farm-1", "Hof Nord", "Kiel"); err != nil {
		return err
	}
	if err := beta.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}

	load, err := beta.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(load.Received == 2,
		"both inactive objects are part of the set it is sent"); err != nil {
		return err
	}
	if err := say.Check(load.Matched == 1,
		"Hof Nord is recognised as the record Beta already holds"); err != nil {
		return err
	}
	if err := say.Check(load.Ignored == 1,
		"Hof Alt is not, and nothing is created for it"); err != nil {
		return err
	}
	say.Detail("the rule that an absent localId means create-and-bind is about objects")
	say.Detail("the endpoint is expected to hold. Creating a record for an entity the")
	say.Detail("world considers gone would be inventing data")

	row, err := beta.Row(agmasync.TypeFarm, "beta-farm-1")
	if err != nil {
		return err
	}
	if err := say.Check(row.Bound(),
		"the recognised object is bound all the same, dead or not"); err != nil {
		return err
	}
	if err := say.Check(load.Sent == 0,
		"which is what stops Beta offering its copy back as new, and resurrecting "+
			"it for every other participant"); err != nil {
		return err
	}

	record, err := beta.Record(agmasync.TypeFarm, "beta-farm-1")
	if err != nil {
		return err
	}
	if err := say.Check(record.Archived,
		"Beta marks its own copy inactive, in whatever way its schema expresses that",
	); err != nil {
		return err
	}

	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if len(held) != 1 {
		return fmt.Errorf("Beta holds %d farms, want only the one it started with", len(held))
	}
	return say.Check(load.State == agmasync.StateCompleted,
		"and the load completes with one farm held, not three")
}
