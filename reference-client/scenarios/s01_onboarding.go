package scenarios

import (
	"context"
	"fmt"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func onboarding() Scenario {
	return Scenario{
		Number: 1,
		Title:  "Onboarding and first initial load",
		Spec:   []string{"Routing and opt-in", "Initial load", "Identifier mapping"},
		Run:    runOnboarding,
	}
}

// runOnboarding is the whole of a first connection: a user opts an endpoint in,
// agrirouter sends it everything the tenant holds, the endpoint reconciles that
// against its own records, and then offers back what agrirouter did not have.
func runOnboarding(ctx context.Context, w *World) error {
	say := w.Say

	say.Step("A farmer's business already has one connected system, Alpha FMIS.")
	alpha, err := w.Join(ctx, "Alpha FMIS", "fmis-alpha", "alpha")
	if err != nil {
		return err
	}
	say.Detail("onboarding it declared the entity types its software can exchange, which")
	say.Detail("enables nothing on its own: a declaration is the list a user is offered")
	if err := alpha.OptIn(ctx, agmasync.TypeFarm, agmasync.TypeField); err != nil {
		return err
	}
	say.Detail("the user then selects farms and fields, in agrirouter. There is no")
	say.Detail("participant-facing write for that, and it is what starts a load")

	// Its own two records, made in its own software with no involvement from
	// the exchange.
	if err := alpha.AddFarm("alpha-farm-1", "Hof Nord", "Kiel"); err != nil {
		return err
	}
	if err := alpha.AddField("alpha-field-1", "Nordacker", 12.4, "alpha-farm-1"); err != nil {
		return err
	}

	say.Step("Alpha runs its initial load against an empty tenant.")
	first, err := alpha.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(first.Received == 0,
		"the canonical set is empty, so there is nothing to reconcile"); err != nil {
		return err
	}
	if err := say.Check(first.Sent == 2,
		"it offers agrirouter the %d records the set did not contain", first.Sent); err != nil {
		return err
	}
	say.Detail("in dependency order: the farm, then the field that references it. A")
	say.Detail("reference travels as the sender's own identifier and is resolved against")
	say.Detail("its mapping, so a field sent first would name a farm agrirouter cannot find")

	farm, err := alpha.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err != nil {
		return err
	}
	say.Detail("farm alpha-farm-1 is now agrirouterId %s at revision %d",
		short(*farm.AgrirouterID), *farm.Revision)

	say.Step("A second system, Beta FMIS, is connected to the same business.")
	beta, err := w.Join(ctx, "Beta FMIS", "fmis-beta", "beta")
	if err != nil {
		return err
	}
	// One record of its own, which nobody else has heard of.
	if err := beta.AddFarm("beta-farm-1", "Hof West", "Rendsburg"); err != nil {
		return err
	}
	if err := beta.OptIn(ctx, agmasync.TypeFarm, agmasync.TypeField); err != nil {
		return err
	}

	status, err := beta.Status(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(status.State == agmasync.StateLoadingFromAgrirouter,
		"opting in put the endpoint at %s, a state only agrirouter enters",
		status.State); err != nil {
		return err
	}

	say.Step("Beta takes the canonical set and reconciles it.")
	load, err := beta.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(!load.Repeat,
		"previousLoadCompletedAt is absent, so this set is genuinely new to it"); err != nil {
		return err
	}
	if err := say.Check(load.Received == 2 && load.Created == 2,
		"%d objects arrive, none recognised, so %d local records are created",
		load.Received, load.Created); err != nil {
		return err
	}
	if err := say.Check(len(load.Confirmed) == 2 && len(load.Rejected) == 0,
		"reconciliation is confirmed carrying %d bindings, none refused",
		len(load.Confirmed)); err != nil {
		return err
	}
	if err := say.Check(load.Sent == 1,
		"and it offers back the one record the set did not contain, Hof West"); err != nil {
		return err
	}
	if err := say.Check(load.State == agmasync.StateCompleted,
		"the endpoint ends at %s", load.State); err != nil {
		return err
	}

	transitions, err := w.Transitions(ctx, beta)
	if err != nil {
		return err
	}
	say.Detail("the state machine ran: %s", strings.Join(transitions, " -> "))
	say.Detail("two transitions each, and each side drives the ones only it can observe")

	say.Step("What Beta created holds together on its own keys.")
	fields, err := beta.LocalIDs(agmasync.TypeField)
	if err != nil {
		return err
	}
	if len(fields) != 1 {
		return fmt.Errorf("Beta holds %d fields, want the one in the set", len(fields))
	}
	betaField, err := beta.Record(agmasync.TypeField, fields[0])
	if err != nil {
		return err
	}
	betaFarm := ""
	farms, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	for _, id := range farms {
		if beta.Attr(agmasync.TypeFarm, id, "name") == "Hof Nord" {
			betaFarm = id
		}
	}
	if err := say.Check(strings.Contains(string(betaField.Modelled["farm"]), betaFarm),
		"its field %q points at its own farm %q", fields[0], betaFarm); err != nil {
		return err
	}
	say.Detail("nothing in the set carried a localId for Beta — every frame was rendered")
	say.Detail("before it had bound anything — so the link was resolved through")
	say.Detail("agrirouterId, which is what delivery order is for: the farm arrived first")

	say.Step("Steady state: Alpha hears about Hof West on its live stream.")
	caught, err := alpha.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(caught.Created == 1 && caught.Bound == 1,
		"it creates a record for the object it does not hold, and binds it"); err != nil {
		return err
	}
	return say.Check(caught.CaughtUp,
		"and the backlog ends with CAUGHT_UP, which is the only thing that says so")
}
