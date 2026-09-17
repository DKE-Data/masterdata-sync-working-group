package scenarios

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

func referenceAheadOfTheSet() Scenario {
	return Scenario{
		Number: 10,
		Title:  "A live change referencing what the set has not delivered, held rather than asked for",
		Spec: []string{
			"References", "Initial load", "Requesting objects (lazy loading)",
		},
		Run: runReferenceAheadOfTheSet,
	}
}

// runReferenceAheadOfTheSet is an object arriving on the live stream naming a
// target the initial-load stream has not reached, the two being ordered within
// themselves and not against each other.
//
// It looks like the case for a request and is not one. The target is in the set
// and arrives before RECONCILING, so an endpoint holds the referencing object
// and waits; only a target still missing once the set is complete is genuinely
// absent, and that is what a request is for — see [lostOwnObject].
func runReferenceAheadOfTheSet(ctx context.Context, w *World) error {
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

	say.Step("Alpha records that the farm belongs to a person, Jens Petersen.")
	if err := alpha.AddPerson("alpha-person-1", "Petersen", "Jens"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypePerson, "alpha-person-1"); err != nil {
		return err
	}

	beta, err := w.Join(ctx, "Beta FMIS", "fmis-beta", "beta")
	if err != nil {
		return err
	}
	if err := beta.OptIn(ctx, agmasync.TypeOrganization); err != nil {
		return err
	}
	if _, err := beta.Load(ctx); err != nil {
		return err
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	say.Step("Beta is opted into organizations only and does not receive the person.")

	say.Step("The user opts into farms for Beta's endpoint, which starts a load.")
	if err := beta.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}
	selected, err := beta.Selection(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(selected) == 3,
		"the frame states what that came to: %s", names(selected)); err != nil {
		return err
	}
	say.Detail("Beta keeps it, the frame being the only place a selection is stated:")
	say.Detail("the set fixed for that load holds the farm and the person both")

	// Beta keeps what it is applied rather than only what it could store, which
	// is the platform's own business and not the protocol's. A reference this
	// store cannot resolve leaves no trace in its tables, so the scenario reads
	// the frame as it goes past.
	var delivered oapi.Entity
	beta.Receiver.OnApplied = func(ev agmasync.Event, _ psync.Outcome) {
		if ev.Envelope.Type == agmasync.TypeFarm {
			delivered = ev.Entity
		}
	}

	say.Step("Before Beta takes that set, Alpha assigns the farm to Jens Petersen.")
	if err := alpha.Owned("alpha-farm-1", agmasync.TypePerson, "alpha-person-1"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	live, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(live.Received == 1,
		"the changed farm reaches Beta on the live stream, and the person does not"); err != nil {
		return err
	}

	betaFarms, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if len(betaFarms) != 1 {
		return fmt.Errorf("Beta holds %d farms, want the one delivered", len(betaFarms))
	}
	betaFarm := betaFarms[0]

	record, err := beta.Record(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	_, resolvedOwner := record.Modelled["owner"]
	if err := say.Check(!resolvedOwner,
		"Beta holds the farm and cannot resolve its owner: it holds no such party"); err != nil {
		return err
	}
	say.Detail("the reference is left unresolved.")

	owner, err := referenceOn(delivered, "owner")
	if err != nil {
		return err
	}
	if err := say.Check(owner.AgrirouterID != nil,
		"the farm named the target entity, so Beta knows what it is waiting for"); err != nil {
		return err
	}
	if err := say.Check(owner.Type != nil && *owner.Type == string(agmasync.TypePerson),
		"and it names the kind of party, %q", derefOr(owner.Type)); err != nil {
		return err
	}

	say.Step("Beta does not ask for it, because the set has not finished arriving.")
	status, err := beta.Status(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(status.State == oapi.LOADINGFROMAGRIROUTER,
		"the endpoint is still in %s", string(status.State)); err != nil {
		return err
	}

	say.Step("Beta takes the set, and the reference closes on its own.")
	load, err := beta.LoadSelected(ctx, selected)
	if err != nil {
		return err
	}
	if err := say.Check(load.Created == 1,
		"the person was in the canonical set: %d created", load.Created); err != nil {
		return err
	}
	linked, err := beta.Record(agmasync.TypeFarm, betaFarm)
	if err != nil {
		return err
	}
	betaPeople, err := beta.LocalIDs(agmasync.TypePerson)
	if err != nil {
		return err
	}
	if len(betaPeople) != 1 {
		return fmt.Errorf("Beta holds %d people, want the one the set carried", len(betaPeople))
	}
	if err := say.Check(
		containsLocalID(linked.Modelled["owner"], betaPeople[0]),
		"and the farm now names Beta's own %q as its owner", betaPeople[0]); err != nil {
		return err
	}
	say.Detail("had the set ended without it, absence would be real — the set is")
	say.Detail("complete at RECONCILING — and Beta would ask by the identifier the")
	say.Detail("farm carried")
	return nil
}

// reference is a reference as it arrives, which is the only place Beta can read
// one this platform has no column for.
type reference struct {
	AgrirouterID *uuid.UUID `json:"agrirouter_id"`
	LocalID      *string    `json:"local_id"`
	Type         *string    `json:"type"`
}

// referenceOn reads one reference slot off a delivered entity.
func referenceOn(entity oapi.Entity, slot string) (reference, error) {
	raw, err := entity.MarshalJSON()
	if err != nil {
		return reference{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return reference{}, err
	}
	value, ok := fields[slot]
	if !ok {
		return reference{}, fmt.Errorf("the delivered object carries no %q", slot)
	}
	var ref reference
	if err := json.Unmarshal(value, &ref); err != nil {
		return reference{}, err
	}
	return ref, nil
}

// containsLocalID reports whether a reference names this local identifier.
func containsLocalID(raw json.RawMessage, localID string) bool {
	var ref reference
	if err := json.Unmarshal(raw, &ref); err != nil {
		return false
	}
	return ref.LocalID != nil && *ref.LocalID == localID
}

func derefOr(s *string) string {
	if s == nil {
		return "none"
	}
	return *s
}
