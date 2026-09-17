package scenarios

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
)

func stricterRecipient() Scenario {
	return Scenario{
		Number: 12,
		Title:  "A recipient stricter than the protocol, and the fallback that must not travel",
		Spec: []string{
			"Differing required/optional attributes", "Reporting that a user is needed",
			"Initial load",
		},
		Run: runStricterRecipient,
	}
}

// runStricterRecipient is the other decision an endpoint cannot make for itself,
// and it is not the one scenario 8 is about.
//
// There the endpoint cannot tell *which* of its records a canonical object is.
// Here it knows exactly, and still cannot store it: the object is valid, the
// sender is within its rights, and the recipient's own software requires an
// attribute the canonical model leaves optional. Nobody is wrong and there is no
// nack, so the whole of the problem lands inside the stricter participant —
// which is the point, because the three obvious ways out all corrupt somebody's
// data.
func runStricterRecipient(ctx context.Context, w *World) error {
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

	say.Step("Alpha shares a field that belongs to no farm.")
	if err := alpha.AddField("alpha-field-1", "Streuobstwiese", 3.2, ""); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-1"); err != nil {
		return fmt.Errorf("a field without a farm is valid and must be accepted: %w", err)
	}
	fieldRow, err := alpha.Row(agmasync.TypeField, "alpha-field-1")
	if err != nil {
		return err
	}
	if err := say.Check(fieldRow.Bound(),
		"which agrirouter accepts: a field's farm is optional in the canonical model"); err != nil {
		return err
	}

	// The stricter side. Everything about it is ordinary except one rule in its
	// own schema, which the exchange knows nothing about and cannot be told.
	beta, err := w.Join(ctx, "Beta FMIS", "fmis-beta", "beta")
	if err != nil {
		return err
	}
	if err := beta.AddFarm("beta-farm-1", "Hof Süd", "Rendsburg"); err != nil {
		return err
	}
	if err := beta.OptIn(ctx, agmasync.TypeField); err != nil {
		return err
	}
	say.Step("Beta requires a farm on every field. Its user opts it in, and it loads.")

	load, err := beta.LoadWith(ctx, requiresFarm{inner: psync.ByName{}})
	if err != nil {
		return err
	}
	if err := say.Check(len(load.Blocked) == 1,
		"the farm arrives and is applied; the field stops the load"); err != nil {
		return err
	}
	if err := say.Check(load.State == agmasync.StateReconciling,
		"which stops at %s rather than completing around it", agmasync.StateReconciling,
	); err != nil {
		return err
	}

	fields, err := beta.LocalIDs(agmasync.TypeField)
	if err != nil {
		return err
	}
	if err := say.Check(len(fields) == 0,
		"Beta creates no field it cannot complete"); err != nil {
		return err
	}
	farms, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}

	status, err := beta.Status(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(status.AwaitingUser != nil && *status.AwaitingUser,
		"agrirouter is told a person is needed"); err != nil {
		return err
	}

	blocked := load.Blocked[0]
	canonical, err := blocked.Entity.AsField()
	if err != nil {
		return err
	}
	options := map[string]string{}
	choices := make([]string, 0, len(farms))
	for _, id := range farms {
		options[id] = farmLabel(beta, id)
		choices = append(choices, options[id])
	}
	chosen, err := say.Ask(
		fmt.Sprintf("Beta needs a farm for %q. Which of its own should it file it under?",
			canonical.Name),
		choices...)
	if err != nil {
		return err
	}
	answer := farms[0]
	for _, id := range farms {
		if options[id] == chosen {
			answer = id
		}
	}

	say.Step("Beta files the field under %s and binds it.", answer)
	if err := beta.AddField("beta-field-1", canonical.Name, areaOf(canonical), answer); err != nil {
		return err
	}
	if err := beta.Adopt(ctx, agmasync.TypeField, "beta-field-1", blocked.AgrirouterID); err != nil {
		return err
	}
	say.Detail("the farm is Beta's answer to its own requirement, not a correction of")
	say.Detail("the canonical object. Whether the world should hear it is a separate")
	say.Detail("decision, and one only the user can make")

	say.Step("With that answered, the load runs again.")
	resumed, err := beta.LoadWith(ctx, requiresFarm{inner: psync.ByName{}})
	if err != nil {
		return err
	}
	if err := say.Check(resumed.State == agmasync.StateCompleted,
		"and reaches %s", agmasync.StateCompleted); err != nil {
		return err
	}
	if err := say.Check(resumed.Sent == 1,
		"offering back the one thing agrirouter did not have: Beta's own farm"); err != nil {
		return err
	}

	// The check the whole scenario is for. Beta's local answer is a row in
	// Beta's database and nothing else — no revision, nothing delivered, and
	// Alpha's meadow still belongs to no farm.
	say.Step("Alpha hears Beta's farm, and nothing about its field.")
	waiting, err := alpha.Deliveries(ctx)
	if err != nil {
		return err
	}
	var aboutTheField int
	for _, ev := range waiting {
		if ev.Envelope.Type == agmasync.TypeField {
			aboutTheField++
		}
	}
	if err := say.Check(aboutTheField == 0,
		"%d of the %d objects waiting for Alpha is a field: the fallback never "+
			"left Beta", aboutTheField, len(waiting)); err != nil {
		return err
	}
	current, err := alpha.Record(agmasync.TypeField, "alpha-field-1")
	if err != nil {
		return err
	}
	if _, hasFarm := current.Modelled["farm"]; hasFarm {
		return errors.New("Alpha's field acquired a farm it never had")
	}
	if err := say.Check(*fieldRow.Revision == 1,
		"the canonical field is still at revision %d", *fieldRow.Revision); err != nil {
		return err
	}
	say.Detail("agrirouter enforces neither system's requirements on the other and")
	say.Detail("drops nothing to satisfy them. The requirement stays where it is felt")

	// And what the same conflict costs once the load is over, which is where
	// most of them actually happen.
	say.Step("Months later Alpha shares a second farmless field.")
	if err := alpha.AddField("alpha-field-2", "Hutung", 1.8, ""); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-2"); err != nil {
		return err
	}
	caught, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(caught.Created == 1,
		"Beta applies it, because there is nothing else it may do with it"); err != nil {
		return err
	}
	say.Detail("there is no recognition step outside a load")

	arrived, err := beta.Record(agmasync.TypeField, caughtFieldOf(beta))
	if err != nil {
		return err
	}
	_, invented := arrived.Modelled["farm"]
	if err := say.Check(!invented,
		"and holds a field its own software says cannot exist, on no farm at all"); err != nil {
		return err
	}
	err = beta.ReportAttention(ctx)
	if err := say.Check(errors.Is(err, agmasync.ErrInitialLoadConflict),
		"and this time it cannot even say a person is needed"); err != nil {
		return err
	}
	say.Detail("awaitingUser belongs to an initial load, and Beta's completed. The")
	say.Detail("conflict is identical and the affordance is gone: from here it is a")
	say.Detail("queue in Beta's own software, and agrirouter is told nothing")
	return nil
}

// requiresFarm is Beta's recognition step: the sample's own, plus the one rule
// its schema will not bend on.
//
// It refuses rather than guesses, and refusing is the whole of what the protocol
// leaves it. Creating the field with a fallback farm would be the guess, and the
// damage is not local: an unbound local record is what gets offered back in the
// push, so Beta's requirement would arrive at every other participant as a
// canonical change to somebody else's field.
type requiresFarm struct{ inner psync.Reconciler }

func (r requiresFarm) Recognise(
	tx *store.Tx, env agmasync.Envelope, entity oapi.Entity,
) (psync.Recognition, error) {
	if env.Type == agmasync.TypeField {
		field, err := entity.AsField()
		if err != nil {
			return psync.Recognition{}, err
		}
		if field.Farm == nil || (field.Farm.AgrirouterId == nil && field.Farm.LocalId == nil) {
			return psync.Recognition{Blocked: true, AwaitingUser: true}, nil
		}
	}
	return r.inner.Recognise(tx, env, entity)
}

// areaOf reads a canonical field's area, which Beta copies into its own record
// along with everything else it can store.
func areaOf(field oapi.Field) float64 {
	if field.Area == nil {
		return 0
	}
	return float64(*field.Area)
}

// caughtFieldOf names the field Beta has just been delivered: the one it holds
// that is not the one it filed by hand.
func caughtFieldOf(p *Platform) string {
	ids, err := p.LocalIDs(agmasync.TypeField)
	if err != nil {
		return ""
	}
	for _, id := range ids {
		if id != "beta-field-1" {
			return id
		}
	}
	return ""
}
