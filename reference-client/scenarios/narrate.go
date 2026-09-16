// Package scenarios runs the parts of AgmaSync a participant is most likely to
// get wrong, end to end and narrated, against a working implementation of the
// other side.
//
// Each scenario is a story with claims in it. The story is what a reader
// follows; the claims are what makes it a check rather than a demonstration —
// every one of them is asserted as it is printed, so a scenario that would
// mislead fails instead. They run identically in three places: `go run
// ./cmd/scenarios` prints them, `go test ./scenarios` runs them against the
// in-process test router, and `go test --tags=it ./scenarios` runs the same code
// against the containerised one.
package scenarios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Scenario is one numbered, runnable story.
type Scenario struct {
	// Number is the scenario's place in the sequence. They are independent —
	// each builds its own tenant and participants — so any one can be run
	// alone.
	Number int

	Title string

	// Spec names the sections of specification.md the scenario is about, so a
	// reader can put the two side by side.
	Spec []string

	Run func(context.Context, *World) error
}

// Asker puts a question to whoever is running the scenarios and returns one of
// the choices.
//
// It exists because one thing in this protocol genuinely cannot be automated:
// reconciliation asks which of a platform's records is the same real-world thing
// as a canonical object, and where the platform cannot tell, a person answers.
// A scenario that decided it silently would be demonstrating the opposite of
// what the specification says.
type Asker func(question string, choices []string) (string, error)

// Execute builds a world against baseURL and runs the scenario in it, printing
// the story line by line.
//
// Nobody is asked anything: questions take their default answer, which is what
// lets the same scenarios run under `go test`. Use [Scenario.ExecuteWith] to put
// them to a person.
func (s Scenario) Execute(ctx context.Context, baseURL string, print func(string)) error {
	return s.ExecuteWith(ctx, baseURL, print, nil)
}

// ExecuteWith is [Scenario.Execute] with somebody to answer its questions. A nil
// ask takes the default answer to each.
func (s Scenario) ExecuteWith(
	ctx context.Context, baseURL string, print func(string), ask Asker,
) error {
	n := &Narrator{print: print, ask: ask}
	n.title(s)

	world, err := NewWorld(ctx, baseURL, n)
	if err != nil {
		return fmt.Errorf("scenario %d: %w", s.Number, err)
	}
	defer world.Close()

	started := time.Now()
	if err := s.Run(ctx, world); err != nil {
		print(fmt.Sprintf("      FAILED: %v", err))
		return fmt.Errorf("scenario %d (%s): %w", s.Number, s.Title, err)
	}
	print(fmt.Sprintf("      passed in %s", time.Since(started).Round(time.Millisecond)))
	return nil
}

// Narrator writes a scenario's story.
//
// It is not part of the protocol or of the sample platform: a scenario narrates
// because its audience is a person reading the specification beside it.
type Narrator struct {
	print func(string)
	ask   Asker
	step  int
}

// Ask puts a decision to a person, and narrates both the question and what came
// back. The first choice is the default, taken when there is nobody to ask.
//
// A scenario asks where the protocol has run out of things it can settle for
// itself. That is not a hole in the specification: agrirouter provides the set
// to reconcile against and adjudicates nothing, so whether two of a platform's
// records are the farm in that set is a judgement only its user can make.
func (n *Narrator) Ask(question string, choices ...string) (string, error) {
	if len(choices) == 0 {
		return "", errors.New("a question with no answers")
	}
	n.step++
	n.print(fmt.Sprintf("  %2d  %s", n.step, question))

	if n.ask == nil {
		n.print(fmt.Sprintf("      -> %s (nobody at the keyboard, so the default stands)",
			choices[0]))
		return choices[0], nil
	}

	answer, err := n.ask(question, choices)
	if err != nil {
		return "", err
	}
	n.print(fmt.Sprintf("      -> %s", answer))
	return answer, nil
}

func (n *Narrator) title(s Scenario) {
	quoted := make([]string, 0, len(s.Spec))
	for _, section := range s.Spec {
		quoted = append(quoted, `"`+section+`"`)
	}

	n.print("")
	n.print(fmt.Sprintf("Scenario %d  %s", s.Number, s.Title))
	n.print("            specification.md: " + strings.Join(quoted, ", "))
	n.print("")
}

// Step announces something one of the participants does.
func (n *Narrator) Step(format string, args ...any) {
	n.step++
	n.print(fmt.Sprintf("  %2d  %s", n.step, fmt.Sprintf(format, args...)))
}

// Detail reports what came of it, or why it matters.
func (n *Narrator) Detail(format string, args ...any) {
	n.print("      " + fmt.Sprintf(format, args...))
}

// Check states a claim the scenario makes about the protocol and asserts it in
// the same breath. A claim that does not hold ends the scenario.
func (n *Narrator) Check(ok bool, format string, args ...any) error {
	claim := fmt.Sprintf(format, args...)
	if !ok {
		n.print("      [x]  " + claim)
		return errors.New("this did not hold: " + claim)
	}
	n.print("      [ok]  " + claim)
	return nil
}
