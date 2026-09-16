package scenarios

import (
	"context"
	"fmt"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

func unbindAndRecreate() Scenario {
	return Scenario{
		Number: 4,
		Title:  "Unbind, receive the object again with no localId, recreate and rebind",
		Spec:   []string{"Identifier mapping", "Disconnection and re-connection"},
		Run:    runUnbindAndRecreate,
	}
}

// runUnbindAndRecreate is the way out for a participant whose local copy is
// gone.
//
// Without unbinding there is none: recreating the record mints a new local
// identifier, and binding that identifier collides with the pair agrirouter
// still holds. Unbinding is the participant saying "I no longer hold this",
// after which the object's next change arrives as something it does not hold.
func runUnbindAndRecreate(ctx context.Context, w *World) error {
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
	original := held[0]
	row, err := beta.Row(agmasync.TypeFarm, original)
	if err != nil {
		return err
	}
	canonical := *row.AgrirouterID

	say.Step("Beta holds the farm as %q, bound to %s.", original, short(canonical))

	say.Step("A user of Beta deletes the farm from their system.")
	if err := beta.Unbind(ctx, agmasync.TypeFarm, original); err != nil {
		return err
	}
	say.Detail("agrirouter is told first. Dropping the mapping locally and failing to say")
	say.Detail("so would leave Beta believing it holds an object agrirouter still maps")
	say.Detail("to it, and its next send would resolve to the old pair")

	unbound, err := beta.Row(agmasync.TypeFarm, original)
	if err != nil {
		return err
	}
	if err := say.Check(!unbound.Bound() && unbound.Unbound,
		"the record is no longer bound to anything"); err != nil {
		return err
	}

	// Before the record goes, what it would now cost to send it.
	if _, err := beta.Send(ctx, agmasync.TypeFarm, original); err == nil {
		return fmt.Errorf("an unbound record must not be sendable")
	} else if err := say.Check(strings.Contains(err.Error(), "re-created"),
		"and sending it is refused locally: %v", err); err != nil {
		return err
	}
	say.Detail("an unbound send does not resolve, so agrirouter would create a second")
	say.Detail("canonical object for a farm that already has one")

	if err := beta.Delete(agmasync.TypeFarm, original); err != nil {
		return err
	}
	say.Detail("unbinding narrows nothing, though: opt-in is the only filter on delivery,")
	say.Detail("so Beta goes on being sent this farm")

	say.Step("Alpha renames the farm.")
	if err := alpha.Edit(agmasync.TypeFarm, "alpha-farm-1", map[string]any{
		"name": "Hof Nord GbR",
	}); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}

	waiting, err := beta.Deliveries(ctx)
	if err != nil {
		return err
	}
	if len(waiting) != 1 {
		return fmt.Errorf("Beta was sent %d objects, want the renamed farm", len(waiting))
	}
	if err := say.Check(waiting[0].Envelope.LocalId == nil,
		"it reaches Beta carrying no localId at all"); err != nil {
		return err
	}
	say.Detail("which is the whole message: agrirouter does not believe Beta holds this")
	say.Detail("object. Create it locally, and bind the identifier you issue")

	say.Step("Beta applies it as something new.")
	caught, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(caught.Created == 1 && caught.Bound == 1,
		"one record created, one binding sent"); err != nil {
		return err
	}

	after, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if len(after) != 1 {
		return fmt.Errorf("Beta holds %d farms, want the one it just recreated", len(after))
	}
	recreated := after[0]
	if err := say.Check(recreated != original,
		"under a new identifier %q — a system cannot usually choose its own keys, "+
			"which is why binding exists", recreated); err != nil {
		return err
	}

	newRow, err := beta.Row(agmasync.TypeFarm, recreated)
	if err != nil {
		return err
	}
	if err := say.Check(*newRow.AgrirouterID == canonical,
		"and bound to the same canonical object %s, so nothing was duplicated",
		short(canonical)); err != nil {
		return err
	}

	if _, err := beta.Send(ctx, agmasync.TypeFarm, recreated); err != nil {
		return fmt.Errorf("a rebound record should be sendable again: %w", err)
	}
	return say.Check(true, "and Beta may send that farm again")
}
