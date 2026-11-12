package scenarios

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func concurrentEdit() Scenario {
	return Scenario{
		Number: 3,
		Title:  "A concurrent edit rejected, rebased, and accepted",
		Spec: []string{
			"Concurrency control", "Applying what agrirouter returns", "Loop prevention",
		},
		Run: runConcurrentEdit,
	}
}

// runConcurrentEdit is what base revisions buy: two participants editing the
// same object at the same time, and the one that wrote from a stale base is
// refused - even though the two touched different attributes - and rebases.
func runConcurrentEdit(ctx context.Context, w *World) error {
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
	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	betaFarm := held[0]

	say.Step("Both systems hold the same farm at revision 1.")
	say.Detail("Alpha calls it alpha-farm-1, Beta calls it %q, and neither holds the other's name",
		betaFarm)

	say.Step("Alpha corrects the address while Beta renames the farm.")
	if err := alpha.Edit(agmasync.TypeFarm, "alpha-farm-1", map[string]any{
		"_agmasync_address": map[string]string{"addressContactTypeCode": "PHYSICAL", "city": "Lübeck"},
	}); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	alphaRow, err := alpha.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err != nil {
		return err
	}
	say.Detail("Alpha's write lands first and makes revision %d", *alphaRow.Revision)

	if err := beta.Edit(agmasync.TypeFarm, betaFarm, map[string]any{
		"name": "Hof Nord Betrieb",
	}); err != nil {
		return err
	}
	_, err = beta.Send(ctx, agmasync.TypeFarm, betaFarm)

	var conflict *agmasync.RevisionConflict
	if !errors.As(err, &conflict) {
		return fmt.Errorf("a write from a stale base must be refused, got %v", err)
	}
	if err := say.Check(errors.Is(err, agmasync.ErrRevisionConflict),
		"Beta writes from base 1 and is refused with 412"); err != nil {
		return err
	}
	if err := say.Check(conflict.CurrentRevision == 2,
		"the refusal names the revision that stands, %d, so Beta can rebase.",
		conflict.CurrentRevision); err != nil {
		return err
	}
	say.Detail("the two changes touch different attributes, and agrirouter still does not")
	say.Detail("merge them: attributes are not independent. A first name changed on one")
	say.Detail("side and a last name on the other would merge into a name neither sent")

	say.Step("Beta takes the current object, reapplies its change, and writes again.")
	caught, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(caught.Received == 1,
		"Alpha's revision was waiting on Beta's stream all along"); err != nil {
		return err
	}
	if err := beta.Edit(agmasync.TypeFarm, betaFarm, map[string]any{
		"name": "Hof Nord Betrieb",
	}); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeFarm, betaFarm); err != nil {
		return fmt.Errorf("a rebased write should succeed: %w", err)
	}

	betaRow, err := beta.Row(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	if err := say.Check(*betaRow.Revision == 3,
		"the rebased write is accepted at revision %d", *betaRow.Revision); err != nil {
		return err
	}
	if err := say.Check(beta.Attr(agmasync.TypeFarm, betaFarm, "name") == "Hof Nord Betrieb",
		"Beta holds its own change"); err != nil {
		return err
	}
	record, err := beta.Record(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	return say.Check(
		string(record.Modelled["_agmasync_address"]) == `{"addressContactTypeCode":"PHYSICAL","city":"Lübeck"}`,
		"and Alpha's address, which it rebased on rather than overwrote: %s",
		record.Modelled["_agmasync_address"])
}
