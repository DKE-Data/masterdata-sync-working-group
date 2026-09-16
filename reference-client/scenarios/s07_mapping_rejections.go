package scenarios

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func mappingRejections() Scenario {
	return Scenario{
		Number: 7,
		Title:  "Non-unique mapping rejected, both causes, singly and in bulk",
		Spec: []string{
			"Identifier mapping", "Asymmetric and non-unique mappings", "Initial load",
		},
		Run: runMappingRejections,
	}
}

// runMappingRejections is why the cause of a rejected binding has to be
// machine-readable.
//
// Within one endpoint a local identifier denotes exactly one canonical object
// and the other way round, so there are two ways to break it — and a participant
// has to handle them differently. One is a disagreement about what is one entity
// and needs a person; the other is the participant holding the answer already.
func runMappingRejections(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	for _, farm := range []struct{ id, name string }{
		{"alpha-farm-1", "Hof Nord"},
		{"alpha-farm-2", "Hof Süd"},
	} {
		if err := alpha.AddFarm(farm.id, farm.name, "Kiel"); err != nil {
			return err
		}
		if _, err := alpha.Send(ctx, agmasync.TypeFarm, farm.id); err != nil {
			return err
		}
	}

	beta, err := w.Contributor(ctx, "Beta FMIS", "fmis-beta", "beta", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if len(held) != 2 {
		return fmt.Errorf("Beta holds %d farms, want the two in the set", len(held))
	}
	first, err := beta.Row(agmasync.TypeFarm, held[0])
	if err != nil {
		return err
	}
	second, err := beta.Row(agmasync.TypeFarm, held[1])
	if err != nil {
		return err
	}

	say.Step("Beta holds two farms, %q and %q, each bound to one canonical object.",
		held[0], held[1])

	say.Step("It claims that its %q is also the other canonical object.", held[0])
	err = beta.Bind(ctx, agmasync.TypeFarm, held[0], *second.AgrirouterID)

	var conflict *agmasync.MappingConflict
	if !errors.As(err, &conflict) {
		return fmt.Errorf("binding a second object to one localId must be refused, got %v", err)
	}
	if err := say.Check(conflict.Rejection.Reason == agmasync.ReasonLocalIDAlreadyBound,
		"refused with %s", conflict.Rejection.Reason); err != nil {
		return err
	}
	if err := say.Check(conflict.Rejection.ExistingMapping != nil &&
		conflict.Rejection.ExistingMapping.AgrirouterId == *first.AgrirouterID,
		"naming the mapping that stands, so the claim can be resolved against it",
	); err != nil {
		return err
	}
	if err := say.Check(agmasync.NeedsUser(conflict.Rejection),
		"and this one needs a person"); err != nil {
		return err
	}
	say.Detail("two canonical objects are being claimed to be one record: the n:1")
	say.Detail("granularity disagreement the protocol deliberately does not merge for you")

	say.Step("Then it claims the first object again under a fresh identifier.")
	err = beta.Bind(ctx, agmasync.TypeFarm, "beta-farm-3", *first.AgrirouterID)
	if !errors.As(err, &conflict) {
		return fmt.Errorf("binding a bound object under a second localId must be refused, got %v",
			err)
	}
	if err := say.Check(conflict.Rejection.Reason == agmasync.ReasonAgrirouterIDAlreadyBound,
		"refused with %s", conflict.Rejection.Reason); err != nil {
		return err
	}
	if err := say.Check(!agmasync.NeedsUser(conflict.Rejection),
		"and this one does not need a person: Beta already holds that object as %q",
		conflict.Rejection.ExistingMapping.LocalId); err != nil {
		return err
	}
	say.Detail("nothing to ask anybody. The two identifiers are both Beta's own, so it")
	say.Detail("has the answer in front of it — which is the point of the causes being")
	say.Detail("distinct rather than one 409")

	say.Step("The same rejection in bulk, on a reconciliation that must not fail over it.")
	gamma, err := w.Join(ctx, "Gamma", "fmis-gamma", "gamma")
	if err != nil {
		return err
	}
	if err := gamma.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}

	// A partial failure, of the shape that actually happens: the endpoint
	// created a record for a delivered object and then told agrirouter it calls
	// that object something else. Its own table and agrirouter's now disagree,
	// and nothing reports the drift until the confirmation.
	waiting, err := gamma.Deliveries(ctx)
	if err != nil {
		return err
	}
	if len(waiting) == 0 {
		return errors.New("Gamma was sent nothing to create a record from")
	}
	outcome, err := gamma.Applier.Apply(waiting[0].Entity, "")
	if err != nil {
		return err
	}
	stray := *waiting[0].Envelope.AgrirouterId
	if err := gamma.Bind(ctx, agmasync.TypeFarm, "gamma-farm-alt", stray); err != nil {
		return err
	}
	say.Detail("Gamma holds the object as %q while agrirouter has it as %q",
		outcome.LocalID, "gamma-farm-alt")

	load, err := gamma.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(load.Rejected) == 1,
		"the confirmation carries every pair it holds and one comes back refused",
	); err != nil {
		return err
	}
	if err := say.Check(load.State == agmasync.StateCompleted,
		"the load still completes: pairs are applied independently, and one "+
			"unresolvable pair must not block a whole set"); err != nil {
		return err
	}
	if err := say.Check(load.Rejected[0].Reason == agmasync.ReasonAgrirouterIDAlreadyBound,
		"the refused pair says %s", load.Rejected[0].Reason); err != nil {
		return err
	}

	row, err := gamma.Row(agmasync.TypeFarm, outcome.LocalID)
	if err != nil {
		return err
	}
	if err := say.Check(!row.Bound(),
		"Gamma gives up its half of the pair: agrirouter's mapping is what a send "+
			"resolves through"); err != nil {
		return err
	}
	return say.Check(load.Sent == 0,
		"and it sends nothing for that record, which would mint a duplicate of an "+
			"object that already exists")
}
