package scenarios

import (
	"context"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

func splitAndMerge() Scenario {
	return Scenario{
		Number: 14,
		Title:  "A field split in two, and the two merged back, with no lineage on the wire",
		Spec: []string{
			"Split and merge", "Deactivation", "Loop prevention",
		},
		Run: runSplitAndMerge,
	}
}

// runSplitAndMerge is a common agricultural process carried by operations that
// know nothing about it.
//
// This version has no representation of a split or a merge. One is exchanged as
// a deactivation and two creates, the other as two deactivations and a create,
// and participants converge on the right current set from those alone — which
// works because the protocol keeps current state and not history.
//
// What is deliberately absent is the lineage: which entities preceded which.
// Recording it would serve continuity of data derived from a field, and those
// entity types are out of scope here, so it is deferred to the version that
// carries them rather than guessed at now.
func runSplitAndMerge(ctx context.Context, w *World) error {
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
	if err := alpha.AddField("alpha-field-1", "Nordacker", 20.0, "alpha-farm-1"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-1"); err != nil {
		return err
	}
	beta, err := w.Contributor(ctx, "Beta FMIS", "fmis-beta", "beta", agmasync.TypeField)
	if err != nil {
		return err
	}
	// Drained so that what follows is the split and nothing else: Beta took the
	// set through its initial load, and the same objects are still waiting on
	// its live stream, which it has never read.
	if _, err := beta.CatchUp(ctx); err != nil {
		return err
	}
	say.Step("Both systems, Aplha and Beta, hold one field of 20 hectares, Nordacker.")

	// What Beta is actually sent, kept so the scenario can check what is not on
	// it.
	var frames []agmasync.Event
	beta.Receiver.OnApplied = func(ev agmasync.Event, _ psync.Outcome) {
		frames = append(frames, ev)
	}

	say.Step("The farmer splits the field in Alpha: Nordacker West and Ost.")
	if _, err := alpha.Deactivate(ctx, agmasync.TypeField, "alpha-field-1"); err != nil {
		return err
	}
	for _, part := range []struct {
		id, name string
		area     float64
	}{
		{"alpha-field-2", "Nordacker West", 12.0},
		{"alpha-field-3", "Nordacker Ost", 8.0},
	} {
		if err := alpha.AddField(part.id, part.name, part.area, "alpha-farm-1"); err != nil {
			return err
		}
		if _, err := alpha.Send(ctx, agmasync.TypeField, part.id); err != nil {
			return err
		}
	}
	say.Detail("three writes: one deactivation and two creates")

	frames = nil
	split, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(split.Received == 3,
		"Beta is sent three objects and has to make the split out of them"); err != nil {
		return err
	}
	if err := say.Check(split.Created == 2,
		"two it does not hold, created and bound"); err != nil {
		return err
	}

	held, err := beta.Record(agmasync.TypeField, "beta-field-1")
	if err != nil {
		return err
	}
	if err := say.Check(held.Archived,
		"and the one it does hold is marked archived"); err != nil {
		return err
	}
	active, err := activeFields(beta)
	if err != nil {
		return err
	}
	if err := say.Check(len(active) == 2,
		"leaving Beta with the same current set as Alpha: %d active fields",
		len(active)); err != nil {
		return err
	}

	say.Step("Later the two fields are merged back in Alpha.")
	for _, part := range []string{"alpha-field-2", "alpha-field-3"} {
		if _, err := alpha.Deactivate(ctx, agmasync.TypeField, part); err != nil {
			return err
		}
	}
	if err := alpha.AddField("alpha-field-4", "Nordacker", 20.0, "alpha-farm-1"); err != nil {
		return err
	}
	if _, err := alpha.Send(ctx, agmasync.TypeField, "alpha-field-4"); err != nil {
		return err
	}

	frames = nil
	merge, err := beta.CatchUp(ctx)
	if err != nil {
		return err
	}
	if err := say.Check(merge.Received == 3 && merge.Created == 1,
		"the same three writes in the other direction: two deactivations and a "+
			"create"); err != nil {
		return err
	}
	after, err := activeFields(beta)
	if err != nil {
		return err
	}
	if err := say.Check(len(after) == 1,
		"and Beta is back to one active field"); err != nil {
		return err
	}
	return nil
}

// activeFields lists the fields a platform still considers current.
func activeFields(p *Platform) ([]string, error) {
	ids, err := p.LocalIDs(agmasync.TypeField)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		record, err := p.Record(agmasync.TypeField, id)
		if err != nil {
			return nil, err
		}
		if !record.Archived {
			out = append(out, id)
		}
	}
	return out, nil
}

// mentions reports whether a delivered object names a canonical identifier
// anywhere, which is what a lineage attribute would look like if there were one.
func mentions(ev agmasync.Event, id uuid.UUID) (bool, error) {
	raw, err := ev.Entity.MarshalJSON()
	if err != nil {
		return false, err
	}
	return strings.Contains(string(raw), id.String()), nil
}

// mentionsAny is [mentions] over several of a platform's records.
func mentionsAny(ev agmasync.Event, p *Platform, localIDs ...string) (bool, error) {
	for _, localID := range localIDs {
		row, err := p.Row(agmasync.TypeField, localID)
		if err != nil {
			return false, err
		}
		if row.AgrirouterID == nil {
			continue
		}
		if *row.AgrirouterID == *ev.Envelope.AgrirouterId {
			continue
		}
		found, err := mentions(ev, *row.AgrirouterID)
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}
