package scenarios

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

func unresolvedReference() Scenario {
	return Scenario{
		Number: 15,
		Title:  "A reference that does not resolve, refused rather than repaired",
		Spec: []string{
			"References", "Hard validation", "Entity dependencies",
		},
		Run: runUnresolvedReference,
	}
}

// runUnresolvedReference is what a reference costs in each direction, and why it
// is worth it.
//
// A participant builds references out of its own identifiers and never has to
// hold a canonical one to send: agrirouter resolves each against the sender's
// own mapping. The price is ordering — a reference to something not yet sent
// resolves to nothing — and the answer to that is a refusal rather than a
// repair, because every other answer invents data. On the way out the same
// reference is rewritten per recipient, which is not a convenience: the sender's
// identifier means nothing in the receiver's namespace and disclosing it is a
// leak.
func runUnresolvedReference(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha", agmasync.TypeField)
	if err != nil {
		return err
	}
	beta, err := w.Contributor(ctx, "Beta FMIS", "fmis-beta", "beta", agmasync.TypeField)
	if err != nil {
		return err
	}

	say.Step("Beta's user creates a farm and a field on it, in Beta's own software.")
	if err := beta.AddFarm("beta-farm-1", "Hof West", "Husum"); err != nil {
		return err
	}
	if err := beta.AddField("beta-field-1", "Westacker", 9.5, "beta-farm-1"); err != nil {
		return err
	}
	say.Detail("neither has been sent, so agrirouter has never heard either identifier")

	say.Step("Beta sends the field first.")
	_, err = beta.Send(ctx, agmasync.TypeField, "beta-field-1")
	if err := say.Check(errors.Is(err, agmasync.ErrValidation),
		"and is refused: the farm it names resolves to nothing"); err != nil {
		return err
	}
	say.Detail("the reference travelled as %q, which is Beta's key and not a canonical", "beta-farm-1")
	say.Detail("identifier. agrirouter resolves it against Beta's own mapping, and there")
	say.Detail("is no pair there yet to resolve it to")

	// Rejected means rejected: not applied, not forwarded, and not patched into
	// something agrirouter could accept.
	if _, err := beta.Row(agmasync.TypeField, "beta-field-1"); !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("a refused write left bookkeeping behind: %v", err)
	}
	waiting, err := alpha.Deliveries(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(waiting) == 0,
		"nothing was created for it, and nothing reached Alpha"); err != nil {
		return err
	}
	say.Step("So Beta sends the farm, and then the field.")
	if _, err := beta.Send(ctx, agmasync.TypeFarm, "beta-farm-1"); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeField, "beta-field-1"); err != nil {
		return fmt.Errorf("the reference should resolve now: %w", err)
	}
	say.Detail("which is what dependency order is for: an entity is sent")
	say.Detail("before the first reference to it, and the mapping does the rest")

	say.Step("Both are delivered to Alpha, and the field references its farm.")
	frames, err := alpha.Deliveries(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(frames) == 2,
		"two objects, the farm ahead of the field that references it"); err != nil {
		return err
	}

	var deliveredField agmasync.Event
	for _, ev := range frames {
		if ev.Envelope.Type == agmasync.TypeField {
			deliveredField = ev
		}
	}
	farm, err := referenceOn(deliveredField.Entity, "farm")
	if err != nil {
		return err
	}
	if err := say.Check(farm.AgrirouterID != nil,
		"agrirouter populated the canonical identifier on it"); err != nil {
		return err
	}
	if err := say.Check(farm.LocalID == nil,
		"and no localId: Alpha holds no identifier for that farm yet"); err != nil {
		return err
	}

	say.Step("Alpha applies both and resolves the link through the canonical identifier.")
	if _, err := alpha.CatchUp(ctx); err != nil {
		return err
	}
	alphaFarms, err := alpha.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	alphaFields, err := alpha.LocalIDs(agmasync.TypeField)
	if err != nil {
		return err
	}
	if len(alphaFarms) != 1 || len(alphaFields) != 1 {
		return fmt.Errorf("Alpha holds %d farms and %d fields, want one of each",
			len(alphaFarms), len(alphaFields))
	}
	applied, err := alpha.Record(agmasync.TypeField, alphaFields[0])
	if err != nil {
		return err
	}
	if err := say.Check(containsLocalID(applied.Modelled["farm"], alphaFarms[0]),
		"its field points at its own farm %q", alphaFarms[0]); err != nil {
		return err
	}
	say.Detail("delivery order is what made that possible: the farm arrived first, so")
	say.Detail("the pair it needed was already there when the field was applied")

	say.Step("Beta renames the field, and the same reference is rendered again.")
	if err := beta.Edit(agmasync.TypeField, "beta-field-1", map[string]any{
		"name": "Westacker Süd",
	}); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeField, "beta-field-1"); err != nil {
		return err
	}
	next, err := alpha.Deliveries(ctx)
	if err != nil {
		return err
	}
	if len(next) != 1 {
		return fmt.Errorf("Alpha has %d objects waiting, want the renamed field", len(next))
	}
	renamed, err := referenceOn(next[0].Entity, "farm")
	if err != nil {
		return err
	}
	if err := say.Check(renamed.LocalID != nil && *renamed.LocalID == alphaFarms[0],
		"this time it carries Alpha's own %q for the farm", alphaFarms[0]); err != nil {
		return err
	}
	say.Detail("localId is populated now because Alpha has bound the farm")
	return nil
}
