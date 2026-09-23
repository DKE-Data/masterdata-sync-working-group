package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// endpointType is what this sample is: software running somewhere, rather than
// a machine or a telemetry unit.
const endpointType = oapi.EndpointTypeToCreate("cloud_software")

// onboardPatiently onboards, retrying until it works or the process is asked to
// stop.
//
// agrirouter being unreachable at the moment this starts is not a reason to
// give up: a participant is a long-running thing, and the first call it makes
// deserves the same patience as the stream it will spend the rest of its life
// reconnecting. Starting alongside an agrirouter that is not listening yet is
// the ordinary case under a process supervisor, and exiting would turn a wait
// of a second into a restart loop.
//
// What it will not retry past is being told no. A refused declaration or a
// tenant this application may not act in is an answer, not an outage, and
// trying it again changes nothing.
func onboardPatiently(
	ctx context.Context, c config, hc *http.Client, log *eventLog,
	types []agmasync.EntityType,
) (uuid.UUID, bool, error) {
	const first, longest = time.Second, 30 * time.Second
	wait := first

	for {
		id, created, err := onboard(ctx, c, hc, types)
		switch {
		case err == nil:
			return id, created, nil
		case ctx.Err() != nil:
			return uuid.Nil, false, ctx.Err()
		case !worthRetrying(err):
			return uuid.Nil, false, err
		}

		log.say("onboard", fmt.Sprintf("%v — trying again in %s", err, wait))
		select {
		case <-ctx.Done():
			return uuid.Nil, false, ctx.Err()
		case <-time.After(wait):
		}
		if wait *= 2; wait > longest {
			wait = longest
		}
	}
}

// errRefused is agrirouter having answered, and having said no.
var errRefused = errors.New("refused")

// errUnauthorized is the one refusal with something to do about it: this
// application holds no authorization for this tenant, which a person grants in
// agrirouter and no retry produces. See [awaitAuthorization].
var errUnauthorized = errors.New(
	"this application is not authorized for this tenant")

// worthRetrying separates an agrirouter that is not there from one that has
// refused. Anything the far end answered is an answer.
func worthRetrying(err error) bool { return !errors.Is(err, errRefused) }

// onboard creates this instance's endpoint, declaring in the same call what its
// software can exchange, and returns the id agrirouter knows it by.
//
// This is the one call that needs no identifier from agrirouter beforehand: the
// endpoint is addressed by the participant's own external id, and agrirouter
// answers with the id everything else names it by. A tenant is all the
// configuration an instance needs, which is why there is nothing here to paste
// between two screens.
//
// It is a create-or-update on the whole endpoint resource, so it is also what a
// restart does: 201 the first time, 200 every time after, and the same endpoint
// either way. That is not the scenarios' reading of the same call, where a 200
// would mean two participants had joined under one name — a container coming
// back up is the ordinary case here, not a collision.
//
// It does not go through agmasync because it is endpoint management rather than
// master-data sync: a participant already consuming the g4 API has this call
// already, and what agmasync adds is everything downstream of an endpoint
// existing.
func onboard(
	ctx context.Context, c config, hc *http.Client, types []agmasync.EntityType,
) (uuid.UUID, bool, error) {
	opts := []oapi.ClientOption{oapi.WithHTTPClient(hc)}
	if c.token != "" {
		opts = append(opts, oapi.WithRequestEditorFn(
			func(_ context.Context, req *http.Request) error {
				req.Header.Set("Authorization", "Bearer "+c.token)
				return nil
			}))
	}
	// The tenant header is the generated client's now: openapi.yaml names it
	// x-agrirouter-tenant-id, which is what a deployed agrirouter reads, so
	// PutEndpointParams below carries it and nothing has to be added here.

	api, err := oapi.NewClientWithResponses(c.baseURL, opts...)
	if err != nil {
		return uuid.Nil, false, err
	}

	// What this endpoint is able to exchange. Declaring enables nothing: it is
	// the list the user is later offered a choice from, and until they choose,
	// the endpoint exchanges nothing.
	//
	// [agmasync.Declaration] closes the set over entity dependencies, so a
	// declaration naming fields names the farms they hang off whether or not
	// the caller thought to. An endpoint able to receive one and not the other
	// could not resolve the references it was sent.
	declaration := agmasync.Declaration(types...)

	// Where a user is sent to answer what an initial load stopped for, which for
	// this participant is the decisions screen. agrirouter renders it as a link
	// while the endpoint has awaiting_user set, so an endpoint that reports
	// needing a person and offers nowhere to go is a dead end on somebody else's
	// screen.
	if base := c.callbackURL(); base != "" {
		resolution := base + "/decisions"
		declaration.ResolutionUrl = &resolution
	}

	res, err := api.PutEndpointWithResponse(ctx, c.externalID(),
		&oapi.PutEndpointParams{XAgrirouterTenantId: c.tenantID},
		oapi.PutEndpointJSONRequestBody{
			ApplicationId:     c.applicationID,
			SoftwareVersionId: c.softwareVersionID,
			EndpointType:      endpointType,
			// Empty, both of them, and empty rather than absent: the fields are
			// required, so a nil slice goes out as null and is not the same
			// thing as a participant saying it has none.
			//
			// This sample exchanges master data and nothing else. Capabilities
			// and subscriptions are the message-based side of agrirouter — what
			// a participant can send and what it wants delivered — and declaring
			// none is the accurate statement, not a placeholder.
			Capabilities:  []oapi.EndpointCapability{},
			Subscriptions: []oapi.EndpointSubscription{},
			Masterdata:    &declaration,
		})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("onboarding %s: %w", c.externalID(), err)
	}

	switch {
	case res.JSON201 != nil:
		return res.JSON201.Id, true, nil
	case res.JSON200 != nil:
		return res.JSON200.Id, false, nil
	case res.HTTPResponse.StatusCode == http.StatusUnauthorized:
		// Told apart from the other refusals because it is the one a person can
		// act on: the credentials are this application's own and were accepted,
		// and what is missing is a user having authorized the application for
		// this farming business.
		return uuid.Nil, false, fmt.Errorf("onboarding %s: %s: %w: %w",
			c.externalID(), strings.TrimSpace(string(res.Body)),
			errUnauthorized, errRefused)
	default:
		// A status rather than a transport failure: agrirouter is there and has
		// declined. Retrying it would change nothing, so this says so — and says
		// what it was told, since this is the call a participant gets wrong
		// first and a bare status number sends nobody anywhere.
		return uuid.Nil, false, fmt.Errorf("onboarding %s: HTTP %d: %s: %w",
			c.externalID(), res.HTTPResponse.StatusCode,
			strings.TrimSpace(string(res.Body)), errRefused)
	}
}

