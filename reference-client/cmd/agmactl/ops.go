package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// runStatus reads where the endpoint's initial load stands.
//
// What this participant declared it can exchange is not readable: the
// declaration is written on the endpoint and echoed back by that write, with no
// resource to read it from afterwards. What the *user* selected is not readable
// either — it is announced on the event stream and nowhere else, so
// `agmactl replay` is where to see it.
func runStatus(ctx context.Context, e *env, args []string) error {
	if err := wantArgs(args, 0, "status"); err != nil {
		return err
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}

	status, err := endpoint.InitialLoadStatus(ctx)
	if err != nil {
		// An endpoint with nothing selected has no initial-load state at all,
		// which is an answer rather than a failure.
		if errors.Is(err, agmasync.ErrNotFound) {
			fmt.Println("load:      none — nothing is selected on this endpoint")
			return nil
		}
		return err
	}
	return printLoadStatus(status)
}

func printLoadStatus(status oapi.InitialLoadStatus) error {
	fmt.Printf("load:      %s\n", status.State)
	if status.AwaitingUser != nil && *status.AwaitingUser {
		fmt.Println("           awaiting the user")
	}
	if status.PreviousLoadCompletedAt != nil {
		fmt.Printf("           previous load completed %s — a set now arriving is a repeat\n",
			status.PreviousLoadCompletedAt.Format("2006-01-02 15:04:05Z"))
	}
	if status.RejectedIdMappings != nil {
		for _, r := range *status.RejectedIdMappings {
			fmt.Printf("rejected:  %s -> %s  %s%s\n",
				r.LocalId, r.AgrirouterId, r.Reason, needsUser(r))
		}
	}
	return nil
}

// runLoad reads the canonical set an initial load owes this endpoint, prints
// what arrives, and — having no conflicts of its own to raise, agmactl
// reconciling against no store at all — carries the load on through to
// COMPLETED on its own.
//
// Unlike the live stream `replay` reads, this one carries a fixed set and no
// position: it ends once agrirouter has sent everything, which is also when
// the endpoint moves to RECONCILING. The response ending is not proof the set
// arrived — a dropped connection ends it exactly as an orderly completion
// does — so an endpoint still at LOADING_FROM_AGRIROUTER after this exits
// takes the set again from the beginning by running it again.
//
// From RECONCILING it confirms immediately, carrying whatever bindings were
// given on the command line in place of a store to read them from — see
// [runConfirm] for their syntax — plus, with -auto, one minted for every
// object that arrived with no local_id at all: an object agmactl "does not
// hold" in the sense the specification means (see [printFrame]), which a real
// participant creates a local record for and binds. agmactl has no record to
// create, so -auto stands in for that decision by binding the object under
// its own agrirouter_id, deterministically and collision-free by construction
// — it is only ever a local id agmactl already knows agrirouter has assigned
// to exactly this object.
//
// Since confirming always advances to LOADING_TO_AGRIROUTER regardless of
// what it rejects, completing happens right behind it. A binding agrirouter
// rejects because it needs a person (rather than one this participant's own
// bookkeeping could resolve) is printed rather than silently completed past:
// run `agmactl confirm`/`complete` by hand once it is resolved.
func runLoad(ctx context.Context, e *env, args []string) error {
	fs := flag.NewFlagSet("load", flag.ContinueOnError)
	auto := fs.Bool("auto", false,
		"mint and bind a local id, from the agrirouter id, for every object with none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bindings, err := parseBindings(fs.Args())
	if err != nil {
		return fmt.Errorf("usage: agmactl load [-auto] [<localId>=<agrirouterId>...]: %w", err)
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}
	stream, err := endpoint.InitialLoadEvents(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	for ev, err := range stream.Events() {
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		printFrame(ev)
		if *auto && ev.HasEntity() && ev.Envelope.LocalId == nil && ev.Envelope.AgrirouterId != nil {
			id := *ev.Envelope.AgrirouterId
			bindings = append(bindings, agmasync.Binding(id.String(), id))
		}
	}
	fmt.Println("stream ended")

	status, err := endpoint.ConfirmReconciled(ctx, bindings)
	if err != nil {
		return err
	}
	if err := printLoadStatus(status); err != nil {
		return err
	}
	if status.RejectedIdMappings != nil {
		for _, r := range *status.RejectedIdMappings {
			if agmasync.NeedsUser(r) {
				fmt.Println("waiting on a person: resolve the rejection above, " +
					"then run `agmactl confirm`/`complete` by hand")
				return nil
			}
		}
	}

	status, err = endpoint.CompleteInitialLoad(ctx)
	if err != nil {
		return err
	}
	return printLoadStatus(status)
}

func parseBindings(args []string) ([]oapi.IdMappingBinding, error) {
	bindings := make([]oapi.IdMappingBinding, 0, len(args))
	for _, arg := range args {
		localID, rawID, ok := strings.Cut(arg, "=")
		if !ok {
			return nil, fmt.Errorf("%q is not localId=agrirouterId", arg)
		}
		id, err := uuid.Parse(rawID)
		if err != nil {
			return nil, fmt.Errorf("%q is not a uuid: %w", rawID, err)
		}
		bindings = append(bindings, agmasync.Binding(localID, id))
	}
	return bindings, nil
}

// runConfirm declares that reconciliation is finished, carrying the bindings
// it produced.
//
// agmactl holds no store, so a pair is named on the command line rather than
// read from one: `localId=agrirouterId` matches one of this participant's own
// identifiers to the canonical object `load` printed. Given none, it asserts
// that nothing in the set needed matching — everything in it is new to this
// endpoint. `load` already does this step on its own; use this to redo it by
// hand, such as after resolving a rejection that needed a person.
func runConfirm(ctx context.Context, e *env, args []string) error {
	bindings, err := parseBindings(args)
	if err != nil {
		return fmt.Errorf("usage: agmactl confirm [<localId>=<agrirouterId>...]: %w", err)
	}

	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}
	status, err := endpoint.ConfirmReconciled(ctx, bindings)
	if err != nil {
		return err
	}
	return printLoadStatus(status)
}

// runComplete declares that the endpoint has sent everything it holds,
// finishing the initial load.
func runComplete(ctx context.Context, e *env, args []string) error {
	if err := wantArgs(args, 0, "complete"); err != nil {
		return err
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}
	status, err := endpoint.CompleteInitialLoad(ctx)
	if err != nil {
		return err
	}
	return printLoadStatus(status)
}

// runAttention tells agrirouter that this endpoint's reconciliation is
// waiting on a person.
func runAttention(ctx context.Context, e *env, args []string) error {
	if err := wantArgs(args, 0, "attention"); err != nil {
		return err
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}
	status, err := endpoint.ReportUserAttention(ctx)
	if err != nil {
		return err
	}
	return printLoadStatus(status)
}

func needsUser(r oapi.IdMappingRejection) string {
	if agmasync.NeedsUser(r) {
		return " (needs a person)"
	}
	return ""
}

// runDeclare states what this participant's software can exchange.
//
// Declaring enables nothing on its own and starts no load: it is the list the
// user is offered a choice from, and the choice is theirs to make in
// agrirouter.
func runDeclare(ctx context.Context, e *env, args []string) error {
	if err := wantArgs(args, 1, "declare <type>[,<type>...]"); err != nil {
		return err
	}
	types, err := parseTypes(args[0])
	if err != nil {
		return err
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}

	config, err := endpoint.Declare(ctx, agmasync.Declaration(types...))
	if err != nil {
		return err
	}
	declared := make([]string, 0, len(config.Capabilities))
	for _, toggle := range config.Capabilities {
		declared = append(declared, toggle.EntityType)
	}
	fmt.Printf("declared: %s\n", orNone(strings.Join(declared, ", ")))
	return nil
}

// runPut sends an entity read from a file or from stdin.
//
// The payload is sent as given, which is the point of the command: it is how a
// vendor tries their own JSON against the validation and the merge. It must
// carry `type` and `localId`, and on an update it must carry the base revision
// — passed here as a flag, since it travels in a header rather than in the
// body.
func runPut(ctx context.Context, e *env, args []string) error {
	fs := flag.NewFlagSet("put", flag.ContinueOnError)
	base := fs.Int("base", -1,
		"the revision this edit was made from; omit only when creating")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs.Args(), 2, "put [-base <n>] <type> <file|->"); err != nil {
		return err
	}

	if _, err := parseType(fs.Arg(0)); err != nil {
		return err
	}
	raw, err := readInput(fs.Arg(1))
	if err != nil {
		return err
	}
	var entity oapi.Entity
	if err := entity.UnmarshalJSON(raw); err != nil {
		return fmt.Errorf("reading the entity: %w", err)
	}

	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}

	var from *int
	if *base >= 0 {
		from = base
	}
	result, err := endpoint.Put(ctx, entity, from)
	if err != nil {
		return err
	}

	// Printed in full because it is not an acknowledgement: it carries the
	// assigned agrirouterId, the revision that resulted, and — where agrirouter
	// merged this write against a concurrent one — content that was never sent.
	// A participant applies it exactly as it applies a delivered object.
	return printEntity(result)
}

// runDeactivate reports that an entity was deactivated in its source system.
func runDeactivate(ctx context.Context, e *env, args []string) error {
	fs := flag.NewFlagSet("deactivate", flag.ContinueOnError)
	base := fs.Int("base", -1, "the revision held; ignored once the object is inactive")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs.Args(), 2, "deactivate [-base <n>] <type> <localId>"); err != nil {
		return err
	}
	typ, err := parseType(fs.Arg(0))
	if err != nil {
		return err
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}

	var from *int
	if *base >= 0 {
		from = base
	}
	result, err := endpoint.Deactivate(ctx, typ, fs.Arg(1), from)
	if err != nil {
		return err
	}
	return printEntity(result)
}

// runRequest asks for one object by its canonical identifier.
func runRequest(ctx context.Context, e *env, args []string) error {
	if err := wantArgs(args, 2, "request <type> <agrirouterId>"); err != nil {
		return err
	}
	typ, err := parseType(args[0])
	if err != nil {
		return err
	}
	id, err := uuid.Parse(args[1])
	if err != nil {
		return fmt.Errorf("not a uuid: %w", err)
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}
	if err := endpoint.Request(ctx, typ, id); err != nil {
		return err
	}
	fmt.Println("accepted: the object arrives on the event stream, not here")
	fmt.Println("          it is delivered even to the endpoint that last wrote it, which")
	fmt.Println("          is what a request is for — but it is a delivery and not a")
	fmt.Println("          revision, so have `agmactl replay -follow` connected before")
	fmt.Println("          asking: a later catch-up suppresses your own writes again")
	return nil
}

// runBind declares that a canonical object is one this endpoint already holds.
func runBind(ctx context.Context, e *env, args []string) error {
	if err := wantArgs(args, 3, "bind <type> <localId> <agrirouterId>"); err != nil {
		return err
	}
	typ, id, err := typeAndID(args[0], args[2])
	if err != nil {
		return err
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}

	if err := endpoint.Bind(ctx, typ, args[1], id); err != nil {
		var conflict *agmasync.MappingConflict
		if errors.As(err, &conflict) {
			// The cause decides what happens next, which is why it is
			// machine-readable rather than a message.
			fmt.Printf("refused:  %s%s\n", conflict.Rejection.Reason,
				needsUser(conflict.Rejection))
			if existing := conflict.Rejection.ExistingMapping; existing != nil {
				fmt.Printf("standing: %s -> %s\n", existing.LocalId, existing.AgrirouterId)
			}
		}
		return err
	}
	fmt.Printf("bound:    %s %s -> %s\n", typ, args[1], id)
	return nil
}

// runUnbind declares that this endpoint no longer holds a canonical object.
func runUnbind(ctx context.Context, e *env, args []string) error {
	if err := wantArgs(args, 3, "unbind <type> <localId> <agrirouterId>"); err != nil {
		return err
	}
	typ, id, err := typeAndID(args[0], args[2])
	if err != nil {
		return err
	}
	endpoint, err := e.endpoint()
	if err != nil {
		return err
	}
	if err := endpoint.Unbind(ctx, typ, args[1], id); err != nil {
		return err
	}
	fmt.Printf("unbound:  %s %s\n", typ, args[1])
	fmt.Println("          the object's next change is delivered carrying no localId")
	return nil
}

// runReplay reads the application's live stream and prints what arrives.
//
// The position is passed in rather than remembered: this command has no store,
// so there is nothing here that has durably applied anything, and a position
// derived from what was merely printed would be exactly the mistake the
// specification warns against. A participant keeps it beside the data it
// applied.
func runReplay(ctx context.Context, e *env, args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	from := fs.String("from", "",
		"resume from this position; empty asks for everything")
	follow := fs.Bool("follow", false,
		"keep the stream open after CAUGHT_UP instead of exiting")
	auto := fs.Bool("auto", false,
		"mint and bind a local id, from the agrirouter id, for every arriving object with none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := wantArgs(fs.Args(), 0, "replay [-from <position>] [-follow] [-auto]"); err != nil {
		return err
	}

	client, err := e.client()
	if err != nil {
		return err
	}
	// Only needed for -auto: Bind names the acting endpoint in a header, while
	// the stream itself is the application's and names none.
	var endpoint *agmasync.Endpoint
	if *auto {
		if endpoint, err = e.endpoint(); err != nil {
			return err
		}
	}
	stream, err := client.Events(ctx, *from)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	for ev, err := range stream.Events() {
		if err != nil {
			// Ctrl-C is how -follow ends, so a cancelled context is the
			// ordinary exit rather than a failure.
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		printFrame(ev)
		if *auto && ev.HasEntity() && ev.Envelope.LocalId == nil && ev.Envelope.AgrirouterId != nil {
			autoBind(ctx, endpoint, ev.Envelope)
		}
		if ev.Type == agmasync.EventCaughtUp && !*follow {
			return nil
		}
	}
	// The stream ending proves nothing beyond the connection being gone: a
	// participant reconnects from its last durably applied position.
	fmt.Println("stream ended")
	return nil
}

// autoBind binds an arriving object under its own agrirouter id, standing in
// for the local record a real participant would create and bind instead. A
// refusal is printed rather than fatal: one object a person needs to resolve
// (see [agmasync.NeedsUser]) must not end a `replay -follow` watching for
// everything else.
func autoBind(ctx context.Context, endpoint *agmasync.Endpoint, env agmasync.Envelope) {
	id := *env.AgrirouterId
	if err := endpoint.Bind(ctx, env.Type, id.String(), id); err != nil {
		var conflict *agmasync.MappingConflict
		if errors.As(err, &conflict) {
			fmt.Printf("refused:  %s%s\n", conflict.Rejection.Reason,
				needsUser(conflict.Rejection))
			return
		}
		fmt.Printf("auto-bind failed: %s\n", err)
		return
	}
	fmt.Printf("bound:    %s %s -> %s\n", env.Type, id, id)
}

func printFrame(ev agmasync.Event) {
	switch {
	case ev.Type == agmasync.EventCaughtUp:
		fmt.Printf("%-26s %s\n", ev.Type, ev.ID)

	case ev.Selection != nil:
		// The one frame that carries no entity and is not a position marker. It
		// states the endpoint's whole selection, so there is nothing to read
		// behind it and this can print the answer itself. An empty list is the
		// statement that the endpoint exchanges nothing.
		types := agmasync.SelectedTypes(*ev.Selection)
		names := make([]string, 0, len(types))
		for _, typ := range types {
			names = append(names, string(typ))
		}
		fmt.Printf("%-26s %s=[%s]\n",
			ev.Type, ev.Selection.ExternalId, strings.Join(names, " "))

	case ev.HasEntity():
		// The tenant is on the frame because one application stream carries
		// every tenant it is routed to; a receiver holding several partitions
		// on this rather than on the connection.
		env := ev.Envelope
		fmt.Printf("%-26s %s revision=%s agrirouterId=%s localId=%s tenant=%s endpoint=%s\n",
			ev.Type, env.Type, intOr(env.Revision, "?"), uuidOr(env.AgrirouterId),
			stringOr(env.LocalId, "-"), uuidOr(env.TenantId), uuidOr(env.SourceEndpointId))
		if env.LocalId == nil {
			fmt.Println("                           " +
				"no localId: agrirouter does not believe you hold this object")
		}

	default:
		// A frame type this version does not know is printed and ignored, which
		// is what a participant must do with it too.
		fmt.Printf("%-26s (not understood, and tolerated)\n", ev.Type)
	}
}

// runRoute stands in for the user's routing decision, which has no
// participant-facing operation at all.
//
// It exists on the test router's control plane and nowhere else. Against a real
// agrirouter there is nothing for this command to call: a user routes an
// endpoint in agrirouter, and the participant learns of it on the stream.
//
// Which is why it is a command here rather than a button in the reference
// client. A participant that could route itself would be a participant nobody
// had to trust, and a sample offering the affordance only against the test
// router would teach a shape that does not exist in production.
func runRoute(ctx context.Context, e *env, args []string) error {
	if len(args) > 1 {
		return errors.New("usage: agmactl route [<type>[,<type>...]]")
	}
	list := ""
	if len(args) == 1 {
		list = args[0]
	}
	types, err := parseTypes(list)
	if err != nil {
		return err
	}
	if e.externalID == "" {
		return errors.New("an endpoint is required: pass -external")
	}

	names := []string{}
	for _, typ := range types {
		names = append(names, string(typ))
	}
	body, err := json.Marshal(map[string]any{"entityTypes": names})
	if err != nil {
		return err
	}

	url := strings.TrimSuffix(e.baseURL, "/") + "/_test/endpoints/" + e.externalID + "/opt-in"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the control plane is a test-router affordance: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("routing refused: HTTP %d", resp.StatusCode)
	}

	fmt.Printf("routed: %s\n", orNone(strings.Join(names, ", ")))
	fmt.Println("        a widened routing starts an initial load; watch `agmactl status`")
	return nil
}

func typeAndID(typeName, id string) (agmasync.EntityType, uuid.UUID, error) {
	typ, err := parseType(typeName)
	if err != nil {
		return "", uuid.Nil, err
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("not a uuid: %w", err)
	}
	return typ, parsed, nil
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func printEntity(entity oapi.Entity) error {
	raw, err := entity.MarshalJSON()
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return err
	}
	fmt.Println(pretty.String())
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func stringOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

func intOr(n *int, fallback string) string {
	if n == nil {
		return fallback
	}
	return fmt.Sprint(*n)
}

func uuidOr(id *uuid.UUID) string {
	if id == nil {
		return "-"
	}
	return id.String()
}
