package scenarios

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func preservedAttributes() Scenario {
	return Scenario{
		Number: 11,
		Title:  "Attributes a participant does not model, kept by leaving them out",
		Spec: []string{
			"Writing an entity", "Field", "Harvest period", "Applying what agrirouter returns",
		},
		Run: runPreservedAttributes,
	}
}

// runPreservedAttributes is what a merge-patch write buys a participant that
// models less than the canonical model does.
//
// A write only changes what it carries: an attribute a sender leaves out is
// kept, and only null removes one. So a participant that does not model an
// attribute neither stores it nor sends it, and every participant that does
// model it keeps it all the same.
//
// Both participants here run this sample, which has columns for part of the
// canonical model and keeps nothing else. Alpha stands in for a product that
// models two more attributes and Beta for one that does not: what is under
// test is that Beta can edit the field without holding them.
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
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-1"); err != nil {
		return err
	}
	// Written straight to agrirouter, the sample having no feature that
	// produces them: a real Alpha fills them from its own screens.
	original, err := alpha.Supplement(ctx, agmasync.TypeField, "alpha-field-1", map[string]any{
		"metadata": map[string]string{
			"contract": "PACHT-2029-114", "steward": "Jens Petersen",
		},
		"harvest_period": map[string]any{
			"valid_from": "2029-07-01", "valid_to": "2029-09-30", "label": "2029",
		},
	})
	if err != nil {
		return err
	}
	metadata := string(original["metadata"])
	period := string(original["harvest_period"])

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
	_, heldMetadata := received.Modelled["metadata"]
	_, heldPeriod := received.Modelled["harvest_period"]
	if err := say.Check(!heldMetadata && !heldPeriod,
		"Beta keeps what it has columns for, and nothing else"); err != nil {
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
	say.Detail("the write carries what Beta models. The two attributes it does not")
	say.Detail("model are not in it, and a write leaves alone what it leaves out")

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

	canonical, err := alpha.Canonical(ctx, agmasync.TypeField, "alpha-field-1")
	if err != nil {
		return err
	}
	if err := say.Check(string(canonical["metadata"]) == metadata,
		"and what Beta never held is untouched: %s", canonical["metadata"]); err != nil {
		return err
	}
	if err := say.Check(string(canonical["harvest_period"]) == period,
		"as is the harvest period"); err != nil {
		return err
	}
	say.Detail("to remove one, a participant sends it as null")
	return nil
}
