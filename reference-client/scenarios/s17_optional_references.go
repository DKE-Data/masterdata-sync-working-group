package scenarios

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func optionalReferences() Scenario {
	return Scenario{
		Number: 17,
		Title:  "A reference to a type not exchanged, ignored on delivery and left out of writes",
		Spec: []string{
			"Entity dependencies", "Routing and opt-in", "Writing an entity",
		},
		Run: runOptionalReferences,
	}
}

// runOptionalReferences is two platforms that model different references
// exchanging the one type they share.
//
// Only a boundary's field is a required reference, so an endpoint may select
// fields without farms. A field it receives can still name a farm, and the
// endpoint ignores that reference: the farm is never sent to it and cannot be
// requested. Its own writes leave the attribute out, which keeps the farm for
// the participants that exchange farms (ADR 13).
//
// Alpha exchanges farms and fields. Beta exchanges parties and fields, and its
// own software files fields under farms of its own that it does not exchange.
func runOptionalReferences(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha",
		agmasync.TypeFarm, agmasync.TypeField)
	if err != nil {
		return err
	}
	say.Step("Alpha shares a farm and a field on it.")
	if err := alpha.AddFarm("alpha-farm-1", "Hof Nord", "Kiel"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	if err := alpha.AddField("alpha-field-1", "Nordacker", 12.4, "alpha-farm-1"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-1"); err != nil {
		return err
	}

	say.Step("Beta's user selects parties and fields, without farms, and Beta loads.")
	beta, err := w.Contributor(ctx, "Beta FMIS", "fmis-beta", "beta",
		agmasync.TypeParty, agmasync.TypeField)
	if err != nil {
		return err
	}
	selected, err := beta.Selection(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(names(selected) == "party, field",
		"the selection stays as chosen, fields pulling in no farms: %s", names(selected)); err != nil {
		return err
	}
	// The receive loop records the selection, which is what Beta's writes are
	// checked against.
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}

	fields, err := beta.LocalIDs(agmasync.TypeField)
	if err != nil {
		return err
	}
	if len(fields) != 1 {
		return fmt.Errorf("Beta holds %d fields, want Alpha's", len(fields))
	}
	betaField := fields[0]
	farms, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if err := say.Check(len(farms) == 0 && refLocalID(beta, agmasync.TypeField, betaField, "farm") == "",
		"Beta holds the field and ignores the farm it names, which it was never sent"); err != nil {
		return err
	}

	say.Step("Beta's user files the field under a farm of Beta's own, and renames it.")
	if err := beta.AddFarm("beta-farm-1", "Betrieb Süd", "Husum"); err != nil {
		return err
	}
	if err := beta.Edit(agmasync.TypeField, betaField, map[string]any{
		"name": "Nordacker West",
		"farm": map[string]string{"local_id": "beta-farm-1"},
	}); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeField, betaField); err != nil {
		return err
	}
	say.Detail("the write leaves the farm out. Beta does not exchange farms, so")
	say.Detail("its farm is bound nowhere, and naming it would not resolve")

	say.Step("Alpha hears the rename.")
	if _, err := alpha.CatchUp(ctx); err != nil {
		return err
	}
	if err := say.Check(
		alpha.Attr(agmasync.TypeField, "alpha-field-1", "name") == "Nordacker West",
		"the rename is there"); err != nil {
		return err
	}
	if err := say.Check(
		refLocalID(alpha, agmasync.TypeField, "alpha-field-1", "farm") == "alpha-farm-1",
		"and the field is still on Alpha's farm: what Beta left out, it kept"); err != nil {
		return err
	}

	say.Step("Alpha's user renames the field again, and Beta hears it.")
	if err := alpha.Edit(agmasync.TypeField, "alpha-field-1", map[string]any{
		"name": "Nordacker Ost",
	}); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-1"); err != nil {
		return err
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	if err := say.Check(
		beta.Attr(agmasync.TypeField, betaField, "name") == "Nordacker Ost",
		"the rename is there"); err != nil {
		return err
	}
	if err := say.Check(
		refLocalID(beta, agmasync.TypeField, betaField, "farm") == "beta-farm-1",
		"and Beta's own farm is kept: the delivered farm reference is ignored"); err != nil {
		return err
	}
	return nil
}

// refLocalID reads the platform's own identifier out of a reference attribute
// of one of its records. A reference it does not hold reads as empty.
func refLocalID(p *Platform, typ agmasync.EntityType, localID, key string) string {
	record, err := p.Record(typ, localID)
	if err != nil {
		return ""
	}
	var ref struct {
		LocalID string `json:"local_id"`
	}
	if raw, ok := record.Modelled[key]; ok {
		_ = json.Unmarshal(raw, &ref)
	}
	return ref.LocalID
}
