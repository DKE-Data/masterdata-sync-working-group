package scenarios

import (
	"context"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

func correctCrosswise() Scenario {
	return Scenario{
		Number: 4,
		Title:  "Two farms matched crosswise, corrected as one set",
		Spec:   []string{"Identifier mapping", "Correcting a binding"},
		Run:    runCorrectCrosswise,
	}
}

// runCorrectCrosswise is the wrong binding a user most often makes: two similar
// objects matched the wrong way round.
//
// Each half is a valid binding, so nothing at bind time catches it, and no
// single re-pointing fixes it: one record cannot take the other's object while
// the other still holds it. A correction lists the target state for both and
// is applied whole.
func runCorrectCrosswise(ctx context.Context, w *World) error {
	say := w.Say

	alpha, err := w.Contributor(ctx, "Alpha FMIS", "fmis-alpha", "alpha", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	ids := map[string]uuid.UUID{}
	for _, farm := range []struct{ id, name string }{
		{"alpha-north", "Hof Nord"},
		{"alpha-south", "Hof Süd"},
	} {
		if err := alpha.AddFarm(farm.id, farm.name, "Kiel"); err != nil {
			return err
		}
		if _, err := alpha.Send(ctx, agmasync.TypeFarm, farm.id); err != nil {
			return err
		}
		row, err := alpha.Row(agmasync.TypeFarm, farm.id)
		if err != nil {
			return err
		}
		ids[farm.id] = *row.AgrirouterID
	}
	north, south := ids["alpha-north"], ids["alpha-south"]

	beta, err := w.Join(ctx, "Beta FMIS", "fmis-beta", "beta")
	if err != nil {
		return err
	}
	if err := beta.AddFarm("beta-north", "Nord", "Kiel"); err != nil {
		return err
	}
	if err := beta.AddFarm("beta-south", "Süd", "Kiel"); err != nil {
		return err
	}
	say.Step("Beta joins holding \"Nord\" and \"Süd\", and its user matches them to the set crosswise.")
	if err := beta.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}
	load, err := beta.LoadWith(ctx, userMatches{south: "beta-north", north: "beta-south"})
	if err != nil {
		return err
	}
	if err := say.Check(load.Matched == 2 && load.Created == 0,
		"both farms matched, nothing created"); err != nil {
		return err
	}
	if err := say.Check(beta.Attr(agmasync.TypeFarm, "beta-north", "name") == "Hof Süd",
		"and beta-north now reads \"Hof Süd\": the set was applied through the wrong pairs"); err != nil {
		return err
	}

	say.Step("Beta's user corrects beta-north alone.")
	_, err = beta.Correct(ctx, agmasync.TypeFarm, []oapi.IdMappingCorrectionEntry{
		agmasync.Correction("beta-north", &north),
	})
	var conflict *agmasync.MappingConflict
	if !errors.As(err, &conflict) {
		return fmt.Errorf("half a swap: error = %v, want a mapping conflict", err)
	}
	standing := conflict.Rejection.ExistingMapping
	if err := say.Check(standing != nil && standing.LocalId == "beta-south",
		"refused: Hof Nord is held by beta-south, which the request does not list"); err != nil {
		return err
	}
	say.Detail("a correction never takes an object from a localId it does not name,")
	say.Detail("so it is never half a swap")

	say.Step("Beta lists both.")
	if _, err := beta.Correct(ctx, agmasync.TypeFarm, []oapi.IdMappingCorrectionEntry{
		agmasync.Correction("beta-north", &north),
		agmasync.Correction("beta-south", &south),
	}); err != nil {
		return err
	}
	for localID, want := range map[string]uuid.UUID{"beta-north": north, "beta-south": south} {
		row, err := beta.Row(agmasync.TypeFarm, localID)
		if err != nil {
			return err
		}
		if row.AgrirouterID == nil || *row.AgrirouterID != want {
			return fmt.Errorf("%s bound to %v, want %s", localID, row.AgrirouterID, short(want))
		}
	}
	if err := say.Check(beta.Attr(agmasync.TypeFarm, "beta-north", "name") == "Hof Nord" &&
		beta.Attr(agmasync.TypeFarm, "beta-south", "name") == "Hof Süd",
		"both rebound in one call, and the requested objects put each record right"); err != nil {
		return err
	}

	waiting, err := alpha.Deliveries(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(waiting) == 0,
		"Alpha is sent nothing: a correction creates no revision"); err != nil {
		return err
	}

	if err := beta.Edit(agmasync.TypeFarm, "beta-north", map[string]any{"name": "Hof Nord GbR"}); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeFarm, "beta-north"); err != nil {
		return fmt.Errorf("a corrected record should be sendable: %w", err)
	}
	return say.Check(true, "and Beta's next write lands on the farm it means")
}

// userMatches is a reconciliation in which the user picked the record for each
// canonical object, rightly or not.
type userMatches map[uuid.UUID]string

func (m userMatches) Recognise(
	_ *store.Tx, env agmasync.Envelope, _ oapi.Entity,
) (psync.Recognition, error) {
	return psync.Recognition{LocalID: m[*env.AgrirouterID], AwaitingUser: true}, nil
}
