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
		Title:  "A concurrent edit merged, and the overlapping one rejected",
		Spec: []string{
			"Concurrency control", "Applying what agrirouter returns", "Loop prevention",
		},
		Run: runConcurrentEdit,
	}
}

// runConcurrentEdit is what base revisions buy: two participants editing the
// same object at the same time, one merge and one refusal, and the difference
// between them decided by whether they touched the same attribute.
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
		"address": map[string]string{"city": "Lübeck"},
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
	if _, err := beta.Send(ctx, agmasync.TypeFarm, betaFarm); err != nil {
		return fmt.Errorf("a non-overlapping edit should merge, not fail: %w", err)
	}

	betaRow, err := beta.Row(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	if err := say.Check(*betaRow.Revision == 3,
		"Beta writes from base 1 and is merged, landing at revision %d",
		*betaRow.Revision); err != nil {
		return err
	}
	say.Detail("not base + 1: another participant wrote in between. A participant that")
	say.Detail("assumed base + 1 would now hold a revision agrirouter never issued")

	if err := say.Check(beta.Attr(agmasync.TypeFarm, betaFarm, "name") == "Hof Nord Betrieb",
		"the response carries Beta's own change"); err != nil {
		return err
	}
	record, err := beta.Record(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	if err := say.Check(string(record.Modelled["address"]) == `{"city":"Lübeck"}`,
		"and Alpha's, which Beta had never seen: %s", record.Modelled["address"]); err != nil {
		return err
	}
	say.Detail("which is why a write response is applied rather than acknowledged. Origin")
	say.Detail("suppression keeps this revision off Beta's own stream, so there is no")
	say.Detail("second chance to learn what the merge produced")

	say.Step("Now both of them rename the farm, from a base each believes is current.")
	if err := alpha.Edit(agmasync.TypeFarm, "alpha-farm-1", map[string]any{
		"name": "Hof Nord GbR",
	}); err != nil {
		return err
	}
	_, err = alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1")

	var conflict *agmasync.RevisionConflict
	if !errors.As(err, &conflict) {
		return fmt.Errorf("an overlapping edit must be refused, got %v", err)
	}
	if err := say.Check(errors.Is(err, agmasync.ErrRevisionConflict),
		"Alpha is refused with 412: both changed `name`, and differently"); err != nil {
		return err
	}
	if err := say.Check(conflict.CurrentRevision == 3,
		"the refusal names the revision that stands, %d, so the loser can rebase.",
		conflict.CurrentRevision); err != nil {
		return err
	}
	say.Detail("a merge is only possible where the two changes do not overlap. Nothing")
	say.Detail("here can decide between two values for one attribute, so it does not try")

	say.Step("Alpha takes the current object, reapplies its change, and writes again.")
	caught, err := alpha.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(caught.Received == 1,
		"the merged revision was waiting on its stream all along"); err != nil {
		return err
	}
	if err := alpha.Edit(agmasync.TypeFarm, "alpha-farm-1", map[string]any{
		"name": "Hof Nord GbR",
	}); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return fmt.Errorf("a rebased write should succeed: %w", err)
	}

	alphaRow, err = alpha.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err != nil {
		return err
	}
	return say.Check(*alphaRow.Revision == 4,
		"the rebased write is accepted at revision %d", *alphaRow.Revision)
}
