package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
)

func blockedReconciliation() Scenario {
	return Scenario{
		Number: 8,
		Title:  "Reconciliation that needs a person stops the load until it has an answer",
		Spec: []string{
			"Initial load", "Identifier mapping", "Asymmetric and non-unique mappings",
		},
		Run: runBlockedReconciliation,
	}
}

// runBlockedReconciliation is the one step in an initial load that the protocol
// cannot specify, and what an endpoint owes the exchange while it is waiting.
//
// agrirouter provides the canonical set and adjudicates nothing. Which of a
// platform's records is the farm in that set is a judgement about the world, and
// where the platform cannot make it, a person must — so the interesting question
// is not how to recognise objects but what to do when you cannot. The answer the
// endpoint must not give is a guess: an object it cannot identify, answered with
// a new local record, is a duplicate, and offering that record back in the push
// turns one farm into three.
func runBlockedReconciliation(ctx context.Context, w *World) error {
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
	say.Step("Alpha contributes one farm, %q, so the canonical set holds exactly one.",
		"Hof Nord")

	// Beta is the case reconciliation exists for: a platform that already holds
	// the data, under identifiers agrirouter has never seen, and does not know
	// which of its records the set is about.
	beta, err := w.Join(ctx, "Beta FMIS", "fmis-beta", "beta")
	if err != nil {
		return err
	}
	for _, farm := range []struct{ id, city string }{
		{"beta-farm-1", "Kiel"},
		{"beta-farm-2", "Rendsburg"},
	} {
		if err := beta.AddFarm(farm.id, "Hof Nord", farm.city); err != nil {
			return err
		}
	}
	say.Step("Beta already holds two farms of its own, both called %q.", "Hof Nord")
	say.Detail("one of them is that farm and one is a different business of the same")
	say.Detail("name — which is a question about the world, not about the protocol")

	if err := beta.OptIn(ctx, agmasync.TypeFarm); err != nil {
		return err
	}
	say.Step("Beta's user opts it into farms, and Beta takes the set.")

	load, err := beta.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(load.Blocked) == 1,
		"Beta will not guess: one object arrives that it cannot decide"); err != nil {
		return err
	}
	if err := say.Check(load.State == agmasync.StateReconciling,
		"so the load stops at %s instead of completing around it",
		agmasync.StateReconciling); err != nil {
		return err
	}

	held, err := beta.LocalIDs(agmasync.TypeFarm)
	if err != nil {
		return err
	}
	if err := say.Check(len(held) == 2,
		"and Beta invents nothing: still its own two records, no third"); err != nil {
		return err
	}
	say.Detail("a third record here is the failure this whole step exists to prevent.")
	say.Detail("it would be pushed back as new in LOADING_TO_AGRIROUTER, and one farm")
	say.Detail("would end up as two canonical objects nobody can tell apart")

	status, err := beta.Status(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(status.AwaitingUser != nil && *status.AwaitingUser,
		"agrirouter is told a person is needed, and shows %q for this endpoint",
		"waiting for you in Beta FMIS"); err != nil {
		return err
	}
	say.Detail("one bit, and never what it is about: the conflict is on a screen")
	say.Detail("agrirouter cannot see, while the user who connected the endpoint may")
	say.Detail("well be looking at agrirouter")

	// The question, and it is asked here for a reason: everything after it is a
	// transition the endpoint drives, and the first of them says reconciliation
	// is finished. Asking afterwards would be asking after the answer stopped
	// mattering.
	blocked := load.Blocked[0]
	options := map[string]string{
		"beta-farm-1": farmLabel(beta, "beta-farm-1"),
		"beta-farm-2": farmLabel(beta, "beta-farm-2"),
	}
	chosen, err := say.Ask(
		fmt.Sprintf(
			"Which of Beta's farms is the canonical farm %s?",
			canonicalFarmLabel(blocked.Entity)),
		options["beta-farm-1"], options["beta-farm-2"],
	)
	if err != nil {
		return err
	}
	answer := "beta-farm-1"
	if chosen == options["beta-farm-2"] {
		answer = "beta-farm-2"
	}
	merged := "beta-farm-2"
	if answer == "beta-farm-2" {
		merged = "beta-farm-1"
	}

	say.Step("Beta records the answer: %s is that farm, %s was a duplicate of it.",
		answer, merged)
	if err := beta.Adopt(ctx, agmasync.TypeFarm, answer, blocked.AgrirouterID); err != nil {
		return err
	}
	if err := beta.Delete(agmasync.TypeFarm, merged); err != nil {
		return err
	}

	say.Step("With the decision made, Beta runs the load again.")
	resumed, err := beta.Load(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(resumed.Blocked) == 0,
		"nothing is outstanding now, so it goes on through the confirmation",
	); err != nil {
		return err
	}
	if err := say.Check(resumed.State == agmasync.StateCompleted,
		"and reaches %s", agmasync.StateCompleted); err != nil {
		return err
	}
	say.Detail("the set is not sent a second time: the endpoint is long past")
	say.Detail("LOADING_FROM_AGRIROUTER, and the load picks up where it stopped")

	if err := say.Check(resumed.Sent == 0,
		"Beta offers nothing back: the one farm it still holds is the one it just "+
			"bound, and agrirouter already has it"); err != nil {
		return err
	}

	row, err := beta.Row(agmasync.TypeFarm, answer)
	if err != nil {
		return err
	}
	if err := say.Check(row.Bound() && *row.AgrirouterID == blocked.AgrirouterID,
		"%s is bound to the object it was blocked on, so Beta can now send it", answer,
	); err != nil {
		return err
	}

	final, err := beta.Status(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(final.AwaitingUser == nil || !*final.AwaitingUser,
		"and the flag is cleared: the endpoint raises it, agrirouter clears it on the "+
			"transitions the endpoint drives"); err != nil {
		return err
	}

	// What refusing to guess bought, checked from the other side of the exchange
	// rather than from Beta's own books.
	//
	// Alpha is never sent its own farm back — origin suppression withholds an
	// object from the endpoint whose change produced its current revision — so
	// anything waiting on Alpha's stream is an object somebody else created. Had
	// Beta answered an object it could not identify with a record of its own,
	// that record would have been offered back as new in LOADING_TO_AGRIROUTER,
	// and it would be sitting here now: a second canonical object for a farm that
	// already had one, delivered to every other participant, with nothing in the
	// data to say which of the two is the real one.
	say.Step("Nothing of Beta's reaches Alpha, because Beta minted nothing.")
	waiting, err := alpha.Deliveries(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(len(waiting) == 0,
		"Alpha is sent no farm it does not already know: one real farm is still "+
			"one canonical object"); err != nil {
		return err
	}
	say.Detail("this is the check that would have caught the guess. A third record")
	say.Detail("pushed back as new arrives here, and by then it is everybody's")
	say.Detail("problem rather than Beta's")

	alphaRow, err := alpha.Row(agmasync.TypeFarm, "alpha-farm-1")
	if err != nil {
		return err
	}
	if !alphaRow.Bound() {
		return errors.New("Alpha's farm lost its binding")
	}
	return say.Check(*alphaRow.AgrirouterID == blocked.AgrirouterID,
		"and it is the object Beta was blocked on, so the two participants hold one "+
			"farm under their own identifiers and agree what it is")
}

// farmLabel describes one of the platform's own farms by name and city, so the
// user answering a reconciliation question sees more than a bare local
// identifier — the same "Hof Nord" that makes the two records ambiguous by
// name alone.
func farmLabel(p *Platform, localID string) string {
	record, err := p.Record(agmasync.TypeFarm, localID)
	if err != nil {
		return localID
	}
	var name string
	_ = json.Unmarshal(record.Modelled["name"], &name)

	var address struct {
		City string `json:"city"`
	}
	_ = json.Unmarshal(record.Modelled["address"], &address)

	return fmt.Sprintf("%s (%s, %s)", localID, name, address.City)
}

// canonicalFarmLabel describes the canonical object a load is blocked on by
// its own name and city — the data a person actually needs to answer the
// question — rather than the agrirouter identifier, which says nothing about
// which of the platform's farms it is.
func canonicalFarmLabel(entity oapi.Entity) string {
	farm, err := entity.AsFarm()
	if err != nil {
		return "unknown"
	}
	city := ""
	if farm.Address != nil && farm.Address.City != nil {
		city = *farm.Address.City
	}
	return fmt.Sprintf("%q in %s", farm.Name, city)
}
