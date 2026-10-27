package scenarios

import (
	"context"
	"errors"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	"github.com/google/uuid"
)

func oneRecordTwoEndpoints() Scenario {
	return Scenario{
		Number: 18,
		Title:  "One record in two tenancies, onboarded as two endpoints into one tenant",
		Spec:   []string{"Identifier mapping", "Common envelope"},
		Run:    runOneRecordTwoEndpoints,
	}
}

// runOneRecordTwoEndpoints is a product with one store behind two of its
// tenancies, each onboarded as its own endpoint into the same agrirouter
// tenant, and one record both of them hold.
//
// agrirouter keys the identifier mapping by the endpoint, so the record is
// bound once per endpoint — here under the same local identifier, because it
// is one record — to one canonical object. What the participant must not do
// is send it through the second endpoint before that endpoint has bound it:
// the send would not resolve, and agrirouter would mint a second canonical
// object for it.
func runOneRecordTwoEndpoints(ctx context.Context, w *World) error {
	say := w.Say

	north, err := w.Contributor(ctx, "Alpha (north)", "fmis-alpha", "alpha-north", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	south, err := w.JoinTenancy(ctx, north, "Alpha (south)", "alpha-south")
	if err != nil {
		return err
	}
	if err := south.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}
	if _, err := south.Load(ctx); err != nil {
		return err
	}
	beta, err := w.Contributor(ctx, "Beta FMIS", "fmis-beta", "beta", agmasync.TypeFarm)
	if err != nil {
		return err
	}
	say.Detail("Alpha keeps one store behind two tenancies, north and south, each")
	say.Detail("onboarded into this tenant as an endpoint of its own.")

	say.Step("Alpha's north tenancy sends a farm.")
	if err := north.AddFarm("alpha-farm-1", "Hof Nord", "Kiel"); err != nil {
		return err
	}
	if _, err := north.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	northRow, err := north.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err != nil {
		return err
	}
	canonical := *northRow.AgrirouterID
	say.Detail("it is canonical object %s, bound by the north endpoint.", short(canonical))

	say.Step("A user adds the same farm to the south tenancy.")
	if err := south.Hold(agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	_, err = south.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err := say.Check(errors.Is(err, store.ErrNotFound),
		"one record, but the south endpoint holds no pair for it yet"); err != nil {
		return err
	}
	say.Detail("the mapping is the endpoint's: north's pair does not resolve south's sends.")

	say.Step("South sends it before anything about it has reached south.")
	if _, err := south.Send(ctx, agmasync.TypeFarm, "alpha-farm-1"); err != nil {
		return err
	}
	southRow, err := south.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err != nil {
		return err
	}
	if err := say.Check(southRow.AgrirouterID != nil && *southRow.AgrirouterID == canonical,
		"south bound %q to %s first, from north's pair, so its send resolved",
		"alpha-farm-1", short(canonical)); err != nil {
		return err
	}
	bound, err := boundBy(ctx, w, south.ExternalID, "alpha-farm-1")
	if err != nil {
		return err
	}
	if err := say.Check(bound,
		"agrirouter recorded the binding from the south endpoint"); err != nil {
		return err
	}
	say.Detail("unbound, the send would have created a second farm in the tenant.")

	if _, err := north.CatchUp(ctx); err != nil {
		return err
	}
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	farms, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if err := say.Check(len(farms) == 1,
		"Beta receives one farm, not two"); err != nil {
		return err
	}

	say.Step("Beta renames the farm.")
	if err := beta.Edit(agmasync.TypeFarm, farms[0], map[string]any{"name": "Hof Nordost"}); err != nil {
		return err
	}
	if _, err := beta.Send(ctx, agmasync.TypeFarm, farms[0]); err != nil {
		return err
	}
	frames, err := north.Deliveries(ctx)
	if err != nil {
		return err
	}
	recipients := map[uuid.UUID]string{}
	for _, ev := range frames {
		if ev.Envelope.RecipientEndpointId != nil && ev.Envelope.LocalID != nil {
			recipients[*ev.Envelope.RecipientEndpointId] = *ev.Envelope.LocalID
		}
	}
	if err := say.Check(len(frames) == 2 &&
		recipients[north.EndpointID] == "alpha-farm-1" && recipients[south.EndpointID] == "alpha-farm-1",
		"Alpha's one stream carries the change twice, once per endpoint, each under %q",
		"alpha-farm-1"); err != nil {
		return err
	}
	say.Detail("each frame is rendered in its recipient's namespace and names it in")
	say.Detail("recipient_endpoint_id; here both namespaces say the same thing.")

	// North and south share one receive loop, as they share one stream.
	received, err := north.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(received.Received == 2 && received.Created == 0 && received.Bound == 0,
		"Alpha applies both to the one record, creating and binding nothing"); err != nil {
		return err
	}
	if err := say.Check(north.Attr(agmasync.TypeFarm, "alpha-farm-1", "name") == "Hof Nordost",
		"and both tenancies show the rename"); err != nil {
		return err
	}
	return nil
}

// boundBy reports whether the router saw an endpoint bind a local identifier.
func boundBy(ctx context.Context, w *World, externalID, localID string) (bool, error) {
	observations, err := w.Observations(ctx)
	if err != nil {
		return false, err
	}
	for _, obs := range observations {
		if obs.Kind == "bind" && obs.Detail["externalEndpointId"] == externalID &&
			obs.Detail["localId"] == localID {
			return true, nil
		}
	}
	return false, nil
}
