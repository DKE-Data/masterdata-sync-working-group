package scenarios

import (
	"context"
	"fmt"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func customerFarm() Scenario {
	return Scenario{
		Number: 19,
		Title:  "A system without farms links fields to a customer through a farm per customer",
		Spec: []string{
			"Party", "Field", "Declaration: what an endpoint can exchange",
		},
		Run: runCustomerFarm,
	}
}

// runCustomerFarm is a billing system that knows customers and their fields,
// and no farms.
//
// A field names its farm only, so parties and fields are linked through farms
// alone, and an endpoint exchanging both must exchange farms too. agrirouter
// refuses a selection without them rather than adding them. A system without
// farms writes one per customer: on its first field of that customer, named
// after the customer once, with only the name and the grower, under an
// identifier of its own (ADR 19).
func runCustomerFarm(ctx context.Context, w *World) error {
	say := w.Say

	fmis, err := w.Contributor(ctx, "FMIS", "fmis", "fmis",
		agmasync.TypeParty, agmasync.TypeFarm, agmasync.TypeField)
	if err != nil {
		return err
	}

	say.Step("Billing's user selects parties and fields. Billing has no farms.")
	billing, err := w.Join(ctx, "Billing", "billing", "billing")
	if err != nil {
		return err
	}
	refused := billing.OptIn(ctx, agmasync.TypeParty, agmasync.TypeField)
	if err := say.Check(refused != nil && strings.Contains(refused.Error(), `requires "farm"`),
		"refused, not widened: farms are the only link between parties and fields"); err != nil {
		return err
	}
	say.Detail("%s", refused.Error()[strings.Index(refused.Error(), "enabling"):])

	say.Step("The user adds farms.")
	if err := billing.OptIn(ctx, agmasync.TypeParty, agmasync.TypeFarm, agmasync.TypeField); err != nil {
		return err
	}
	if _, err := billing.Load(ctx); err != nil {
		return err
	}
	selected, err := billing.Selection(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(names(selected) == "party, farm, field",
		"the selection is what the user chose: %s", names(selected)); err != nil {
		return err
	}

	say.Step("Billing takes on customer Meier and his field Nordacker.")
	billing.ids.reserve("billing-meier")
	if err := billing.Edit(agmasync.TypeParty, "billing-meier", map[string]any{"name": "Meier"}); err != nil {
		return err
	}
	if _, err := billing.Send(ctx, agmasync.TypeParty, "billing-meier"); err != nil {
		return err
	}
	billing.ids.reserve("farm-of-billing-meier")
	if err := billing.Edit(agmasync.TypeFarm, "farm-of-billing-meier", map[string]any{
		"name":                "Meier",
		"_agmasync_growerRef": agmasync.LocalRef("billing-meier"),
	}); err != nil {
		return err
	}
	if _, err := billing.Send(ctx, agmasync.TypeFarm, "farm-of-billing-meier"); err != nil {
		return err
	}
	if err := billing.AddField("billing-nordacker", "Nordacker", 12.4, "farm-of-billing-meier"); err != nil {
		return err
	}
	if _, err := billing.Send(ctx, agmasync.TypeField, "billing-nordacker"); err != nil {
		return err
	}
	say.Detail("first the customer, then a farm named after it, with only the name")
	say.Detail("and the grower, then the field on that farm")

	say.Step("The FMIS receives it.")
	if _, err := fmis.CatchUp(ctx); err != nil {
		return err
	}
	party, err := only(fmis, agmasync.TypeParty)
	if err != nil {
		return err
	}
	farm, err := only(fmis, agmasync.TypeFarm)
	if err != nil {
		return err
	}
	field, err := only(fmis, agmasync.TypeField)
	if err != nil {
		return err
	}
	if err := say.Check(
		refLocalID(fmis, agmasync.TypeField, field, "_agmasync_farmRef") == farm &&
			refLocalID(fmis, agmasync.TypeFarm, farm, "_agmasync_growerRef") == party &&
			fmis.Attr(agmasync.TypeParty, party, "name") == "Meier",
		"Nordacker is on farm Meier, held by party Meier"); err != nil {
		return err
	}

	say.Step("The farmer renames the farm in the FMIS.")
	if err := fmis.Edit(agmasync.TypeFarm, farm, map[string]any{"name": "Hof Meier"}); err != nil {
		return err
	}
	if _, err := fmis.Send(ctx, agmasync.TypeFarm, farm); err != nil {
		return err
	}
	if _, err := billing.CatchUp(ctx); err != nil {
		return err
	}
	say.Detail("from now on someone maintains it: it is an ordinary farm")

	say.Step("Billing renames the customer.")
	if err := billing.Edit(agmasync.TypeParty, "billing-meier", map[string]any{"name": "Meier GbR"}); err != nil {
		return err
	}
	if _, err := billing.Send(ctx, agmasync.TypeParty, "billing-meier"); err != nil {
		return err
	}
	if _, err := fmis.CatchUp(ctx); err != nil {
		return err
	}
	say.Detail("Billing sends the party only. It named the farm once, when it")
	say.Detail("created it, and leaves its name to the systems that show farms")
	return say.Check(
		fmis.Attr(agmasync.TypeParty, party, "name") == "Meier GbR" &&
			fmis.Attr(agmasync.TypeFarm, farm, "name") == "Hof Meier",
		"the party is renamed, and the farmer's farm name survives")
}

// only is the one record of a type the platform holds.
func only(p *Platform, typ agmasync.EntityType) (string, error) {
	ids, err := p.LocalIDs(typ)
	if err != nil {
		return "", err
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("%s holds %d of type %s, want 1", p.Name, len(ids), typ)
	}
	return ids[0], nil
}
