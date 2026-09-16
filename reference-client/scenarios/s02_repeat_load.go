package scenarios

import (
	"context"
	"errors"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func repeatLoad() Scenario {
	return Scenario{
		Number: 2,
		Title:  "Repeat load after opt-out and opt-in, matched through the surviving mapping",
		Spec:   []string{"Initial load", "Re-connection", "Identifier mapping"},
		Run:    runRepeatLoad,
	}
}

// runRepeatLoad is the failure a participant falls into by assuming that a
// canonical set arriving means a first connection: it creates a second local
// copy of everything it already holds.
//
// Nothing in the set says otherwise — the objects are the same objects — so the
// question has to be asked before a single one is applied, and
// previousLoadCompletedAt is what answers it.
func runRepeatLoad(ctx context.Context, w *World) error {
	say := w.Say

	say.Step("Alpha contributes one farm; Beta onboards and takes the set.")
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
	first, err := beta.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(first.Created == 1,
		"the farm is new to Beta, so it creates a record and binds it"); err != nil {
		return err
	}

	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	before, err := beta.Row(agmasync.TypeFarm, held[0])
	if err != nil {
		return err
	}
	say.Detail("Beta calls it %q; agrirouter calls it %s", held[0], short(*before.AgrirouterID))

	say.Step("Beta then contributes a farm of its own, Hof West.")
	if err := beta.AddFarm("beta-farm-2", "Hof West", "Husum"); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeFarm, "beta-farm-2"); err != nil {
		return err
	}
	own, err := beta.Row(agmasync.TypeFarm, "beta-farm-2")
	if err != nil {
		return err
	}

	// Its own write is the one thing Beta's live stream will never carry, which
	// is what makes the next beat matter rather than merely tidy.
	waiting, err := beta.Deliveries(ctx)
	if err != nil {
		return err
	}
	for _, ev := range waiting {
		if ev.Envelope.AgrirouterId != nil && *ev.Envelope.AgrirouterId == *own.AgrirouterID {
			return errors.New("origin suppression should keep Beta's own write off its stream")
		}
	}
	say.Detail("which is not on Beta's own stream and never will be: origin suppression")
	say.Detail("withholds from an endpoint the revisions its own writes produced")

	say.Step("Months later the user opts Beta out of master data entirely.")
	if err := beta.OptOut(ctx); err != nil {
		return err
	}
	if _, err := beta.Status(ctx); !errors.Is(err, agmasync.ErrNotFound) {
		return errors.New("an endpoint opted into nothing should have no initial-load state")
	}
	say.Detail("its initial-load state is gone with the last toggle: an endpoint that")
	say.Detail("takes part in nothing has no load to be in a state of")

	say.Step("Then they change their mind and opt it back in.")
	if err := beta.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}
	say.Detail("which starts a load, because the set is fixed when a load starts and")
	say.Detail("agrirouter cannot enumerate what the endpoint missed while it was out")

	second, err := beta.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(second.Repeat,
		"previousLoadCompletedAt survived the opt-out, so this set is a repeat"); err != nil {
		return err
	}
	if err := say.Check(second.Created == 0 && second.Matched == 0,
		"and nothing has to be recognised: %d created, %d matched",
		second.Created, second.Matched); err != nil {
		return err
	}
	say.Detail("the identifier mapping survived too, so every object arrives carrying")
	say.Detail("Beta's own localId and is simply applied to the record it names")

	if err := say.Check(second.Received == 2,
		"the set holds both farms, Beta's own among them"); err != nil {
		return err
	}
	say.Detail("origin suppression does not apply to a set. It exists to avoid handing")
	say.Detail("an endpoint a revision it already holds, and an endpoint taking the set")
	say.Detail("has just declared that it does not know what it holds — withholding it")
	say.Detail("would have Beta report its own farm as missing from the SSOT, and")
	say.Detail("agrirouter would mint a second canonical object for it")

	after, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if err := say.Check(len(after) == 2,
		"Beta still holds %d farms, not the duplicates a blind load would create",
		len(after)); err != nil {
		return err
	}

	now, err := beta.Row(agmasync.TypeFarm, held[0])
	if err != nil {
		return err
	}
	if err := say.Check(*now.AgrirouterID == *before.AgrirouterID,
		"under the same pair it held before"); err != nil {
		return err
	}
	return say.Check(second.Sent == 0,
		"and it offers nothing back: agrirouter already has everything it holds")
}
