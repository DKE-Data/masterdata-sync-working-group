// Command refclient runs one AgmaSync participant, with screens to drive it
// from.
//
// It is the sample platform in internal/platform — the same store and the same
// sync the scenarios use — kept running instead of scripted, so that the parts
// of the protocol that need watching can be watched: two participants
// exchanging objects through agrirouter, an initial load stopping for a person,
// a participant going down and resuming from where it durably got to.
//
// It is not a library and not a starting point. Everything it drives is under
// internal/ so that nothing can depend on this sample's design choices, and a
// vendor wanting code imports the agmasync module instead. What this is for is
// being the other end of a conversation: run it, point your own implementation
// at the same agrirouter and tenant, and send something.
//
// Two of these are two participants only if they are two applications. The live
// stream is per application and so is the identifier mapping, so instances
// sharing a token would share both and demonstrate something the protocol does
// not do.
//
//	refclient -instance alpha -addr :8081 \
//	  -tenant <uuid> -application <uuid> -token alpha
//
// Against a real agrirouter, credentials are OAuth client credentials rather
// than a static token, and nothing else changes: there is no test-router
// affordance anywhere in this program. The routing an initial load follows from
// is the user's decision, made in agrirouter, and this participant only ever
// learns of it on the stream.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "refclient: "+err.Error())
		os.Exit(1)
	}
}

// start brings a participant up, stopping for the one thing it cannot do for
// itself.
//
// Onboarding is the first call, and the first that can be refused. Most
// refusals are the end of it — a declaration agrirouter will not take is not
// going to be taken on the second attempt — but one is not: an application no
// user has authorized for a farming business is a participant waiting on a
// person, not a broken one. So that answer sends the person to agrirouter
// rather than the process to its exit code, and onboarding is tried again with
// what they came back with.
//
// Once only. A second refusal after a grant is not the grant being missing, and
// bouncing the browser through agrirouter again would hide whatever it actually
// is.
func start(ctx context.Context, cfg config) (*instance, error) {
	// Configured with no tenant at all: there is nothing to try, since every
	// call names one. Straight to the person, who is the only source of it.
	if cfg.tenantID != uuid.Nil {
		in, err := newInstance(ctx, cfg)
		if !errors.Is(err, errUnauthorized) {
			return in, err
		}
		// Nowhere to send anybody. What agrirouter said is more use than an
		// invitation to a screen this instance cannot serve.
		if cfg.authorizeURL == "" {
			return nil, err
		}
	}

	slog.Info("not authorized for a tenant yet; waiting at the consent screen",
		"at", "http://localhost"+cfg.addr, "callback", cfg.callbackURL())
	tenant, err := awaitAuthorization(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// A configured tenant is a statement about whose data this instance holds,
	// and its database is named for it. Authorizing a different one is a person
	// having logged in as somebody else, and adopting it silently would mix two
	// businesses in one store.
	if cfg.tenantID != uuid.Nil && cfg.tenantID != tenant {
		return nil, fmt.Errorf(
			"authorized for tenant %s, but this instance is configured for %s",
			tenant, cfg.tenantID)
	}
	cfg.tenantID = tenant
	slog.Info("authorized", "tenant", tenant)

	return newInstance(ctx, cfg)
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(
		context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).
		With("instance", cfg.instance))

	in, err := start(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	// The receive loop and the screens are independent: a participant that
	// cannot reach agrirouter still has a database worth looking at, and that is
	// most of what its screens are for when something has gone wrong.
	go in.run(ctx)

	if err := in.serve(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
