// Command agmactl performs single AgmaSync operations from a shell, against
// the test router or a real tenant.
//
// It is deliberately not a participant. It holds no store, no identifier
// mapping and no delivery position, so it cannot bind what it receives or
// resume where it left off — the reference client does that, and everything
// interesting about synchronizing is in there rather than here. What this is
// for is looking: sending one object, asking for one back, watching a stream,
// reading a state before and after something happens to it.
//
// Configuration is by flag or environment, so a shell session against one
// endpoint sets it once:
//
//	export AGMASYNC_URL=http://localhost:8080
//	export AGMASYNC_TOKEN=fmis-alpha
//	export AGMASYNC_ENDPOINT_ID=<uuid>
//	export AGMASYNC_EXTERNAL_ENDPOINT_ID=ep-alpha
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// env is what every command needs: which agrirouter, as which application, and
// acting as which endpoint.
//
// The two identifier styles are both here because the API uses both: the entity
// operations name the acting endpoint by its agrirouter id in a header, while
// the configuration and initial-load resources are addressed by the
// participant's own external identifier.
type env struct {
	baseURL    string
	token      string
	endpointID string
	externalID string

	// instance names a refclient to address instead of spelling out its
	// external identifier. It is a convenience for the demo and nothing more:
	// refclient derives its external id from its tenant and its instance name,
	// so knowing both is knowing the id, and neither has to be read out of a log.
	instance string

	// applicationID, tenantID, softwareVersionID and endpointType are only
	// needed by the "declare" command, which goes through PutEndpoint — a full
	// endpoint upsert rather than a masterdata-only call. They are empty (zero
	// UUID) unless the caller supplies them, which is fine for every other
	// command.
	applicationID     string
	tenantID          string
	softwareVersionID string
	endpointType      string
}

type command struct {
	name    string
	usage   string
	summary string
	run     func(context.Context, *env, []string) error
}

func commands() []command {
	return []command{
		{"status", "status", "read the endpoint's declaration and initial-load state",
			runStatus},
		{"declare", "declare <type>[,<type>...]",
			"declare what this participant's software can exchange", runDeclare},
		{"put", "put <type> <file|->", "send an entity, creating or updating it", runPut},
		{"deactivate", "deactivate <type> <localId>",
			"report that an entity was deactivated here", runDeactivate},
		{"request", "request <type> <agrirouterId>",
			"ask for one object, which arrives on the stream", runRequest},
		{"bind", "bind <type> <localId> <agrirouterId>",
			"declare that a canonical object is one you hold", runBind},
		{"unbind", "unbind <type> <localId> <agrirouterId>",
			"declare that you no longer hold it", runUnbind},
		{"load", "load [-auto] [<localId>=<agrirouterId>...]",
			"take the canonical set an initial load owes this endpoint, and complete it",
			runLoad},
		{"confirm", "confirm [<localId>=<agrirouterId>...]",
			"redo reconciliation by hand, carrying the bindings it produced", runConfirm},
		{"complete", "complete", "redo, by hand, declaring the load finished",
			runComplete},
		{"attention", "attention", "tell agrirouter reconciliation is waiting on a person",
			runAttention},
		{"replay", "replay [-from <position>] [-follow] [-auto]",
			"read the live stream and print what arrives", runReplay},
		{"route", "route [<type>[,<type>...]]",
			"stand in for the user's routing decision (test router only)", runRoute},
	}
}

func main() {
	var e env
	flag.StringVar(&e.baseURL, "url", os.Getenv("AGMASYNC_URL"),
		"agrirouter base URL (AGMASYNC_URL)")
	flag.StringVar(&e.token, "token", os.Getenv("AGMASYNC_TOKEN"),
		"bearer token identifying the application (AGMASYNC_TOKEN)")
	flag.StringVar(&e.endpointID, "endpoint", os.Getenv("AGMASYNC_ENDPOINT_ID"),
		"the acting endpoint's agrirouter id (AGMASYNC_ENDPOINT_ID)")
	flag.StringVar(&e.externalID, "external", os.Getenv("AGMASYNC_EXTERNAL_ENDPOINT_ID"),
		"the acting endpoint's own id (AGMASYNC_EXTERNAL_ENDPOINT_ID)")
	flag.StringVar(&e.instance, "instance", os.Getenv("REFCLIENT_INSTANCE"),
		"address a refclient by name instead of by -external; needs -tenant (REFCLIENT_INSTANCE)")
	flag.StringVar(&e.applicationID, "application", os.Getenv("AGMASYNC_APPLICATION_ID"),
		"the application id PutEndpoint requires (AGMASYNC_APPLICATION_ID); only needed by \"declare\"")
	flag.StringVar(&e.tenantID, "tenant", os.Getenv("AGMASYNC_TENANT_ID"),
		"the tenant id PutEndpoint requires (AGMASYNC_TENANT_ID); only needed by \"declare\"")
	flag.StringVar(&e.softwareVersionID, "software-version", os.Getenv("AGMASYNC_SOFTWARE_VERSION_ID"),
		"the software version id PutEndpoint requires (AGMASYNC_SOFTWARE_VERSION_ID); only needed by \"declare\"")
	flag.StringVar(&e.endpointType, "endpoint-type", os.Getenv("AGMASYNC_ENDPOINT_TYPE"),
		"the endpoint type PutEndpoint requires, e.g. cloud_software (AGMASYNC_ENDPOINT_TYPE); only needed by \"declare\"")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}
	if e.baseURL == "" {
		e.baseURL = "http://localhost:8080"
	}
	if e.externalID == "" && e.instance != "" {
		if e.tenantID == "" {
			fmt.Fprintln(os.Stderr, "agmactl: -instance needs -tenant")
			os.Exit(2)
		}
		// The same shape refclient builds for itself. An external identifier is
		// agrirouter-wide rather than per tenant, which is why the tenant is in
		// it: two people demonstrating this against one shared agrirouter would
		// otherwise be naming, and taking, one endpoint.
		e.externalID = fmt.Sprintf("refclient:tenant:%s:%s", e.tenantID, e.instance)
	}

	name, args := flag.Arg(0), flag.Args()[1:]
	for _, c := range commands() {
		if c.name != name {
			continue
		}
		// Ctrl-C ends a stream cleanly rather than killing it mid-frame, which
		// matters for replay and for nothing else.
		ctx, stop := signal.NotifyContext(
			context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		if err := c.run(ctx, &e, args); err != nil {
			fmt.Fprintln(os.Stderr, "agmactl: "+err.Error())
			os.Exit(1)
		}
		return
	}

	fmt.Fprintf(os.Stderr, "agmactl: unknown command %q\n\n", name)
	usage()
	os.Exit(2)
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintln(out, "agmactl performs single AgmaSync operations.")
	fmt.Fprintln(out, "\nUsage:\n\n  agmactl [flags] <command> [arguments]\n\nCommands:")
	for _, c := range commands() {
		fmt.Fprintf(out, "  %-46s %s\n", c.usage, c.summary)
	}
	fmt.Fprintln(out, "\nFlags:")
	flag.PrintDefaults()
	fmt.Fprintln(out, "\nAn entity type is named as it is on the wire: `farm`, `fieldBoundary`.")
}

// endpoint builds the handle every operation but replay acts through.
func (e *env) endpoint() (*agmasync.Endpoint, error) {
	client, err := e.client()
	if err != nil {
		return nil, err
	}
	if e.endpointID == "" || e.externalID == "" {
		return nil, errors.New(
			"an acting endpoint is required: pass -endpoint and -external")
	}
	id, err := uuid.Parse(e.endpointID)
	if err != nil {
		return nil, fmt.Errorf("-endpoint is not a uuid: %w", err)
	}
	applicationID, err := parseUUIDOrZero(e.applicationID)
	if err != nil {
		return nil, fmt.Errorf("-application is not a uuid: %w", err)
	}
	tenantID, err := parseUUIDOrZero(e.tenantID)
	if err != nil {
		return nil, fmt.Errorf("-tenant is not a uuid: %w", err)
	}
	softwareVersionID, err := parseUUIDOrZero(e.softwareVersionID)
	if err != nil {
		return nil, fmt.Errorf("-software-version is not a uuid: %w", err)
	}
	return client.For(id, e.externalID,
		applicationID, tenantID, softwareVersionID,
		oapi.EndpointTypeToCreate(e.endpointType)), nil
}

func parseUUIDOrZero(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.UUID{}, nil
	}
	return uuid.Parse(s)
}

func (e *env) client() (*agmasync.Client, error) {
	if e.token == "" {
		return nil, errors.New("a bearer token is required: pass -token")
	}
	// No timeout on the client: the streams are long-lived by design, and an
	// ordinary request is bounded by the context instead.
	return agmasync.NewClient(e.baseURL,
		agmasync.WithHTTPClient(&http.Client{}),
		agmasync.WithBearerToken(e.token))
}

// parseType reads the wire name of an entity type.
//
// Only that spelling: `farm`, never the `farms` path segment. The CLI is what a
// person learns the protocol's vocabulary from, so accepting both here would
// teach that either works.
func parseType(name string) (agmasync.EntityType, error) {
	typ := agmasync.EntityType(name)
	if !typ.Valid() {
		return "", fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, name)
	}
	return typ, nil
}

func parseTypes(list string) ([]agmasync.EntityType, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	var out []agmasync.EntityType
	for _, name := range strings.Split(list, ",") {
		typ, err := parseType(strings.TrimSpace(name))
		if err != nil {
			return nil, err
		}
		out = append(out, typ)
	}
	return out, nil
}

func wantArgs(args []string, n int, usage string) error {
	if len(args) != n {
		return fmt.Errorf("usage: agmactl %s", usage)
	}
	return nil
}
