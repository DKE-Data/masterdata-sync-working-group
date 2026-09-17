package scenarios

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func preservedAttributes() Scenario {
	return Scenario{
		Number: 11,
		Title:  "Attributes a participant does not model, relayed unchanged",
		Spec: []string{
			"Field", "Harvest period", "Applying what agrirouter returns",
		},
		Run: runPreservedAttributes,
	}
}

// runPreservedAttributes is the cost of a whole-object write, and what a
// participant owes the exchange because of it.
//
// Writes carry the whole object, so everything a sender leaves out is a
// deletion: an attribute it does not model, dropped on the way through, is
// erased for every participant that does — and erased as an ordinary revision,
// delivered to all of them, indistinguishable from a user's decision.
//
// Both participants here run this sample, which has typed columns for part of
// the canonical model and an opaque bag for the rest. Alpha stands in for a
// product that models these two attributes and Beta for one that does not: what
// is under test is Beta, which has to relay what it cannot read.
func runPreservedAttributes(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha", agmasync.TypeField)
	if err != nil {
		return err
	}
	if err := alpha.AddFarm("alpha-farm-1", "Hof Nord", "Kiel"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}

	say.Step("Alpha shares a field carrying two attributes its own product models.")
	if err := alpha.AddField("alpha-field-1", "Nordacker", 12.4, "alpha-farm-1"); err != nil {
		return err
	}
	// Put there directly, the sample having no feature that produces them: a
	// real Alpha fills them from its own screens, and the exchange cannot tell
	// the difference, a whole-object write being all agrirouter ever sees.
	if err := alpha.Carry(agmasync.TypeField, "alpha-field-1", map[string]any{
		"metadata": map[string]string{
			"contract": "PACHT-2029-114", "steward": "Jens Petersen",
		},
		"harvest_period": map[string]any{"start_month": 7, "end_month": 9},
	}); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-1"); err != nil {
		return err
	}
	original, err := alpha.Record(agmasync.TypeField, "alpha-field-1")
	if err != nil {
		return err
	}
	metadata := string(original.Unmodelled["metadata"])
	period := string(original.Unmodelled["harvest_period"])

	beta, err := w.Contributor(ctx, "Beta FMIS", "fmis-beta", "beta", agmasync.TypeField)
	if err != nil {
		return err
	}
	say.Step("Beta takes the set and holds the field.")

	fields, err := beta.LocalIDs(agmasync.TypeField)
	if err != nil {
		return err
	}
	if len(fields) != 1 {
		return fmt.Errorf("Beta holds %d fields, want the one it was sent", len(fields))
	}
	betaField := fields[0]

	received, err := beta.Record(agmasync.TypeField, betaField)
	if err != nil {
		return err
	}
	if err := say.Check(string(received.Unmodelled["metadata"]) == metadata,
		"the attributes Beta has no columns for arrive intact"); err != nil {
		return err
	}
	if err := say.Check(string(received.Unmodelled["harvest_period"]) == period,
		"both of them, exactly as they were sent"); err != nil {
		return err
	}

	say.Step("Beta's user renames the field.")
	if err := beta.Edit(agmasync.TypeField, betaField, map[string]any{
		"name": "Nordacker West",
	}); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeField, betaField); err != nil {
		return err
	}
	say.Detail("it is a whole-object write, because that is the only kind there is.")
	say.Detail("everything Beta leaves out of one is an attribute it is deleting")

	say.Step("Alpha hears the change.")
	caught, err := alpha.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(caught.Received == 1,
		"one revision of the field"); err != nil {
		return err
	}
	if err := say.Check(
		alpha.Attr(agmasync.TypeField, "alpha-field-1", "name") == "Nordacker West",
		"the rename is there"); err != nil {
		return err
	}

	survived, err := alpha.Record(agmasync.TypeField, "alpha-field-1")
	if err != nil {
		return err
	}
	if err := say.Check(string(survived.Unmodelled["metadata"]) == metadata,
		"and what Beta never understood came back untouched: %s",
		survived.Unmodelled["metadata"]); err != nil {
		return err
	}
	if err := say.Check(string(survived.Unmodelled["harvest_period"]) == period,
		"as did the harvest period"); err != nil {
		return err
	}
	say.Detail("had Beta dropped them, agrirouter would have read the omission as a")
	say.Detail("deletion")
	return nil
}
