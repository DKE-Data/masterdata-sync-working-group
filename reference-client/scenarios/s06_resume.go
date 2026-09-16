package scenarios

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func resumeAfterDowntime() Scenario {
	return Scenario{
		Number: 6,
		Title:  "Resume after downtime, and a crash between applying and saving the position",
		Spec: []string{
			"Downtime and resume", "Disconnection and re-connection", "Initial load",
		},
		Run: runResumeAfterDowntime,
	}
}

// runResumeAfterDowntime is what the delivery position is worth and what it
// costs to get it wrong.
//
// Delivery is at-least-once, so a position that runs ahead of what was durably
// applied loses objects that will never be sent again, while one that lags costs
// a repeat and nothing else. That asymmetry is the whole design rule: derive the
// position from what has been applied, never from what has been read.
func runResumeAfterDowntime(ctx context.Context, w *World) error {
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

	beta, err := w.Contributor(ctx, "Beta FMIS", "fmis-beta", "beta", agmasync.TypeFarm)
	if err != nil {
		return err
	}

	say.Step("Beta connects its live stream for the first time.")
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	resumeFrom, err := beta.Position()
	if err != nil {
		return err
	}
	if err := say.Check(resumeFrom != "",
		"it keeps the position it reached, opaque and exactly as issued"); err != nil {
		return err
	}
	say.Detail("nothing may be read out of it: not order, not recency, not a count. It is")
	say.Detail("stored as a string and handed straight back")

	say.Step("Beta goes down for a week. Alpha keeps working.")
	say.Detail("it adds a farm, and renames the one they share twice")
	if err := alpha.AddFarm("alpha-farm-2", "Hof Süd", "Kiel"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-2"); err != nil {
		return err
	}
	for _, name := range []string{"Hof Nord I", "Hof Nord II"} {
		if err := alpha.Edit(agmasync.TypeFarm, "alpha-farm-1", map[string]any{"name": name}); err != nil {
			return err
		}
		if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
			return err
		}
	}

	say.Step("Beta comes back and resumes from where it left off.")
	caught, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(caught.Received == 2,
		"two objects changed while it was away, and two frames arrive"); err != nil {
		return err
	}
	say.Detail("not three, though Hof Nord was edited twice: catch-up is served from the")
	say.Detail("current state of each entity, not from a log, so a participant sees each")
	say.Detail("changed entity once and MUST NOT assume it observed every step")
	if err := say.Check(caught.Created == 1 && caught.Bound == 1,
		"the farm it had never heard of is created and bound"); err != nil {
		return err
	}
	say.Detail("positions do not expire, so a long absence is a bigger catch-up rather")
	say.Detail("than a failed one")

	applied, err := beta.Position()
	if err != nil {
		return err
	}
	if err := say.Check(applied != resumeFrom, "and the position has moved on"); err != nil {
		return err
	}

	say.Step("Now the crash: Beta applies an object and dies before saving the position.")
	if err := alpha.Edit(agmasync.TypeFarm, "alpha-farm-1", map[string]any{
		"name": "Hof Nord III",
	}); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}

	waiting, err := beta.Deliveries(ctx)
	if err != nil {
		return err
	}
	if len(waiting) != 1 {
		return fmt.Errorf("%d frames waiting, want the one edit", len(waiting))
	}
	// Applying with no position is what a platform that commits the object and
	// advances the position separately is left holding when it dies in between.
	// This sample cannot do it by accident — both go in one transaction — so the
	// scenario has to ask for it.
	if _, err := beta.Applier.Apply(waiting[0].Entity, ""); err != nil {
		return err
	}

	stalled, err := beta.Position()
	if err != nil {
		return err
	}
	if err := say.Check(stalled == applied,
		"the object is in its tables; the position still points before it"); err != nil {
		return err
	}

	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	before := map[string]int{}
	for _, id := range held {
		row, err := beta.Row(agmasync.TypeFarm, id)
		if err != nil {
			return err
		}
		before[id] = *row.Revision
	}

	say.Step("Beta restarts and resumes from the position it last saved.")
	again, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(again.Received == 1,
		"the frame it already applied is delivered a second time"); err != nil {
		return err
	}
	say.Detail("delivery is at-least-once, and this is the ordinary shape of it")

	after, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if err := say.Check(len(after) == len(held),
		"applying it again changes nothing: still %d farms", len(after)); err != nil {
		return err
	}
	for _, id := range after {
		row, err := beta.Row(agmasync.TypeFarm, id)
		if err != nil {
			return err
		}
		if before[id] != *row.Revision {
			return fmt.Errorf("%s moved from revision %d to %d on a repeat",
				id, before[id], *row.Revision)
		}
	}
	if err := say.Check(true,
		"and no revision moved, applying being idempotent and guarded by revision",
	); err != nil {
		return err
	}
	say.Detail("a lagging position costs a repeat. The other way round — position saved,")
	say.Detail("object lost — costs the object outright: agrirouter considers it")
	say.Detail("delivered and has no log to send it from again")

	recovered, err := beta.Position()
	if err != nil {
		return err
	}
	return say.Check(recovered != stalled, "this time the position moves with it")
}
