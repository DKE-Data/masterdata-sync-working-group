package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
	psync "github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/sync"
	"github.com/google/uuid"
)

// inbox is this platform's recogniser: the part of an initial load the protocol
// cannot specify, answered by the person running the demo instead of by a rule.
//
// agrirouter provides the canonical set and adjudicates nothing, so whether the
// farm called "Hof Nord" in the set is the farm called "Hof Nord" in this
// platform's tables is a judgement only its user can make. That is the whole
// reason this exists, and the reason it blocks: a load with a question
// outstanding is stopped, exactly as it is in scenario 8.
//
// It asks only where there is something to decide. An object arriving for a type
// this platform holds no unbound record of has no candidate to be confused with,
// so it is created without troubling anybody — which is what makes an empty
// participant's first load run start to finish on its own.
type inbox struct {
	ctx     context.Context
	timeout time.Duration
	log     *eventLog

	mu      sync.Mutex
	pending map[string]*decision
	order   []string

	// answered keeps what was decided, for the screen that shows it afterwards.
	// Nothing reads it back into the protocol.
	answered []answered
}

// decision is one canonical object waiting on a person, and the channel the
// load goroutine is parked on.
type decision struct {
	ID           string
	Type         agmasync.EntityType
	AgrirouterID uuid.UUID
	Attributes   []attribute
	Candidates   []candidate
	Asked        time.Time

	answer chan answer
}

// candidate is one of this platform's own records the object might be.
type candidate struct {
	LocalID    string
	Attributes []attribute
}

type attribute struct {
	Name  string
	Value string
}

type answer struct {
	// LocalID names the record the object was recognised as. Empty with kind
	// "create".
	LocalID string
	Kind    string // "match", "create" or "block"
}

type answered struct {
	Type         agmasync.EntityType
	AgrirouterID uuid.UUID
	Kind         string
	LocalID      string
	When         time.Time
}

func newInbox(ctx context.Context, timeout time.Duration, log *eventLog) *inbox {
	return &inbox{
		ctx: ctx, timeout: timeout, log: log,
		pending: map[string]*decision{},
	}
}

// Recognise implements [psync.Reconciler].
//
// It reads through the transaction the object is being applied in, so the
// candidates it offers include records this same load has already created, and
// so the answer commits with the object or not at all.
func (i *inbox) Recognise(
	tx *store.Tx, env agmasync.Envelope, entity oapi.Entity,
) (psync.Recognition, error) {
	candidates, err := unboundRecords(tx, env.Type)
	if err != nil {
		return psync.Recognition{}, err
	}
	if len(candidates) == 0 {
		// Nothing to confuse it with. The platform does not hold this object, and
		// saying so costs nobody's attention.
		return psync.Recognition{}, nil
	}

	attrs, err := attributesOf(env.Type, entity)
	if err != nil {
		return psync.Recognition{}, err
	}

	d := &decision{
		ID:           uuid.NewString(),
		Type:         env.Type,
		AgrirouterID: *env.AgrirouterId,
		Attributes:   attrs,
		Candidates:   candidates,
		Asked:        time.Now(),
		answer:       make(chan answer, 1),
	}
	i.park(d)
	i.log.say("reconcile", fmt.Sprintf(
		"%s %s needs a person: %d record(s) it could be",
		d.Type, d.AgrirouterID, len(d.Candidates)))

	select {
	case a := <-d.answer:
		i.settled(d, a)
		switch a.Kind {
		case "match":
			// A person said this canonical object is that record. It still needs
			// binding, for the same reason a created one does: agrirouter holds no
			// identifier of ours for it yet.
			return psync.Recognition{LocalID: a.LocalID, AwaitingUser: true}, nil
		case "block":
			return psync.Recognition{Blocked: true, AwaitingUser: true}, nil
		default:
			return psync.Recognition{AwaitingUser: true}, nil
		}

	case <-time.After(i.timeout):
		// Nobody is looking. Guessing would be the one thing worse than waiting,
		// so the object is left undecided: the load stops short of reconciled,
		// agrirouter is told a person is needed, and the object is asked for again
		// once there is one.
		i.settled(d, answer{Kind: "timed out"})
		i.log.say("reconcile", fmt.Sprintf(
			"%s %s went unanswered for %s and was left for a person",
			d.Type, d.AgrirouterID, i.timeout))
		return psync.Recognition{Blocked: true, AwaitingUser: true}, nil

	case <-i.ctx.Done():
		// Shutting down mid-question. Blocked rather than guessed, so the load
		// resumes as a load rather than as a set of records nobody chose.
		i.settled(d, answer{Kind: "abandoned"})
		return psync.Recognition{Blocked: true, AwaitingUser: true}, nil
	}
}

func (i *inbox) park(d *decision) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.pending[d.ID] = d
	i.order = append(i.order, d.ID)
}

func (i *inbox) settled(d *decision, a answer) {
	i.mu.Lock()
	defer i.mu.Unlock()
	delete(i.pending, d.ID)
	for n, id := range i.order {
		if id == d.ID {
			i.order = append(i.order[:n], i.order[n+1:]...)
			break
		}
	}
	i.answered = append(i.answered, answered{
		Type: d.Type, AgrirouterID: d.AgrirouterID,
		Kind: a.Kind, LocalID: a.LocalID, When: time.Now(),
	})
}

// Answer hands a decision back to the load that is waiting for it.
func (i *inbox) Answer(id string, a answer) error {
	i.mu.Lock()
	d, ok := i.pending[id]
	i.mu.Unlock()
	if !ok {
		return errors.New("that question is no longer waiting for an answer")
	}
	select {
	case d.answer <- a:
		return nil
	default:
		return errors.New("that question has already been answered")
	}
}

// Pending lists what is waiting, oldest first.
func (i *inbox) Pending() []*decision {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]*decision, 0, len(i.order))
	for _, id := range i.order {
		out = append(out, i.pending[id])
	}
	return out
}

// Answered lists what has been decided, most recent first.
func (i *inbox) Answered() []answered {
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]answered, len(i.answered))
	for n := range i.answered {
		out[len(out)-1-n] = i.answered[n]
	}
	return out
}

// unboundRecords lists the records of one type this tenant holds that no
// canonical object is mapped to.
//
// Bound records are left out because they are already accounted for: agrirouter
// knows what this platform calls them, so an object arriving without a localId
// is by definition not one of them. So are records this platform has unbound,
// which is a stronger statement than never having been bound — it told
// agrirouter it no longer holds that object, and the object's next delivery is
// meant to be created afresh rather than quietly reattached to what was let go.
func unboundRecords(tx *store.Tx, typ agmasync.EntityType) ([]candidate, error) {
	localIDs, err := tx.LocalIDs(typ)
	if err != nil {
		return nil, err
	}

	var out []candidate
	for _, localID := range localIDs {
		row, err := tx.SyncRow(typ, localID)
		switch {
		case err == nil && (row.Bound() || row.Unbound):
			continue
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return nil, err
		}

		record, err := tx.LoadRecord(typ, localID)
		if err != nil {
			return nil, err
		}
		out = append(out, candidate{
			LocalID:    localID,
			Attributes: attributesOfRecord(record),
		})
	}
	return out, nil
}

// attributesOf renders a delivered object for a person to look at.
func attributesOf(typ agmasync.EntityType, entity oapi.Entity) ([]attribute, error) {
	record, err := store.FromEntity(typ, entity)
	if err != nil {
		return nil, err
	}
	return attributesOfRecord(record), nil
}

func attributesOfRecord(r store.Record) []attribute {
	var out []attribute
	for _, set := range []map[string]json.RawMessage{r.Modelled, r.Unmodelled} {
		names := make([]string, 0, len(set))
		for name := range set {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			out = append(out, attribute{Name: name, Value: compact(set[name])})
		}
	}
	return out
}

// compact renders a value on one line, short enough to sit in a table.
func compact(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	if s, ok := v.(string); ok {
		return s
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	if text := string(out); len(text) <= 120 {
		return text
	} else {
		return strings.TrimSpace(text[:117]) + "..."
	}
}
