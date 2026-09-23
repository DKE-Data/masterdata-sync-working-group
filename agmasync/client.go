package agmasync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// Client is an application's connection to agrirouter.
//
// It is application-scoped because the OAuth token is: one application holds
// many endpoints across many tenants, and the live event stream belongs to the
// application and carries all of them. Operations that act as a particular
// endpoint hang off [Client.For] instead.
type Client struct {
	api *oapi.ClientWithResponses

	// http is kept because [WithBearerToken] wraps its transport, and because
	// the generated client is built over it. Nothing here builds a request
	// with it directly — every call, streams included, goes through api.
	http *http.Client
}

// Option configures a [Client].
type Option func(*Client)

// WithHTTPClient supplies the HTTP client used for both ordinary requests and
// the event streams.
//
// Streams are long-lived, so a client with a request timeout will cut them off
// mid-flight. Where one client cannot serve both, give this a client with no
// timeout and bound ordinary requests with the context instead.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.http = hc }
}

// WithBearerToken authenticates every request with a static bearer token.
//
// A real participant holds a token that expires and renews it; this exists so
// that the sample and its tests can pass a fixed one. See "Security
// considerations" in specification.md.
func WithBearerToken(token string) Option {
	return func(c *Client) {
		base := c.http.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		c.http.Transport = &bearerTransport{base: base, token: token}
	}
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Per RoundTripper's contract the request must not be modified in place.
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

// NewClient builds a client against the given agrirouter base URL.
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	c := &Client{http: &http.Client{}}
	for _, o := range opts {
		o(c)
	}

	api, err := oapi.NewClientWithResponses(baseURL, oapi.WithHTTPClient(c.http))
	if err != nil {
		return nil, fmt.Errorf("agmasync: building client: %w", err)
	}
	c.api = api
	return c, nil
}

// Endpoint is a handle on one of the application's endpoints.
//
// Every master-data operation except the application event stream names the
// acting endpoint, because it decides entitlement and the tenant, and because
// it becomes the sourceEndpointId of any revision the operation produces —
// which is what origin suppression is then decided on.
type Endpoint struct {
	client *Client

	// id is the agrirouter identifier of the endpoint, sent as
	// x-agrirouter-endpoint-id on the entity operations.
	id uuid.UUID

	// externalID is the application's own identifier for the endpoint. The
	// configuration and initial-load resources are addressed by it, as
	// endpoint management is, while the entity operations name the endpoint by
	// its agrirouter id in a header. The two identifier styles are not
	// interchangeable, so both are held here.
	externalID string

	// applicationID, tenantID, softwareVersionID and endpointType are the
	// endpoint-management fields PutEndpoint requires on every call (it is a
	// full create-or-update, not a masterdata-only operation). They are fixed
	// for the lifetime of the endpoint, so they are captured once in [Client.For]
	// rather than threaded through every call that needs them.
	applicationID     uuid.UUID
	tenantID          uuid.UUID
	softwareVersionID uuid.UUID
	endpointType      oapi.EndpointTypeToCreate
}

// For returns a handle on one of the application's endpoints.
//
// applicationID, tenantID, softwareVersionID and endpointType are carried on
// the handle because [Endpoint.Declare] calls PutEndpoint, which upserts the
// whole endpoint rather than just its masterdata configuration.
func (c *Client) For(
	endpointID uuid.UUID, externalEndpointID string,
	applicationID, tenantID, softwareVersionID uuid.UUID,
	endpointType oapi.EndpointTypeToCreate,
) *Endpoint {
	return &Endpoint{
		client:            c,
		id:                endpointID,
		externalID:        externalEndpointID,
		applicationID:     applicationID,
		tenantID:          tenantID,
		softwareVersionID: softwareVersionID,
		endpointType:      endpointType,
	}
}

// ID returns the endpoint's agrirouter identifier.
func (e *Endpoint) ID() uuid.UUID { return e.id }

// ExternalID returns the application's own identifier for the endpoint.
func (e *Endpoint) ExternalID() string { return e.externalID }

// serverAssigned are the entity properties agrirouter owns: `readOnly` in
// openapi.yaml, on every entity type, and the same six for all of them.
//
// They are stripped from a write rather than merely ignored on one. agrirouter
// validates requests against the schema and refuses a body carrying any of
// them — "readOnly property \"type\" in request" — so a participant that sends
// back what it was delivered cannot write at all. Which is the ordinary case
// and not a strange one: every one of these arrives on the object, and a
// platform that stores what it receives and sends what it stores has them to
// hand without ever having set one.
var serverAssigned = []string{
	"agrirouter_id", "modified_at", "revision", "source_endpoint_id",
	"tenant_id", "type",
}

// writable renders an entity as the request body a write may carry.
//
// It goes out as the entity's own JSON rather than as the typed value, and the
// entity is what it is given for that reason. The generated models carry no
// notion of `readOnly`: `type` in particular is a plain required string that
// marshals on every send, so stripping has to happen somewhere, and here —
// under the one operation that sends an entity — is the only place a caller
// cannot forget it.
//
// Marshalling the typed value instead loses on both sides of what the model
// says. A required attribute the sender does not hold is invented: an absent
// owner becomes `"owner":{"type":""}`, since the generated Farm carries a
// PartyReference by value, and agrirouter rejects that reference as naming
// neither an agrirouterId nor a localId — where the body as the sender built
// it would have said plainly that the owner is missing. And an attribute the
// model does not name is dropped, the generated types having no place to put
// it, which would silently undo the relaying of unmodelled attributes that a
// participant is obliged to do.
func writable(v any) (io.Reader, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("agmasync: rendering the entity: %w", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("agmasync: rendering the entity: %w", err)
	}
	for _, name := range serverAssigned {
		delete(fields, name)
	}

	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("agmasync: rendering the entity: %w", err)
	}
	return bytes.NewReader(body), nil
}

// applied reads the canonical object a write answered with, from the response
// body rather than from the generated typed value the client decoded it into.
//
// The typed value loses the same attributes marshalling one would invent, for
// the same reason: a generated struct has nowhere to put an attribute the model
// does not name, so decoding into it drops one. That loss is not confined to
// the response. A participant applies what a write returns exactly as it
// applies a delivery, so an attribute dropped here is absent from the store,
// and absent from the next write of that object — the relaying of unmodelled
// attributes undone a revision later, by the write path that took care to
// preserve it. A merged response, which carries content the sender never sent,
// is where the difference is widest.
//
// This is what the event stream already does with a delivered object, and the
// two paths carry the same canonical objects.
func applied(t EntityType, body []byte) (oapi.Entity, error) {
	var out oapi.Entity
	if err := json.Unmarshal(body, &out); err != nil {
		return oapi.Entity{}, convErr(t, err)
	}
	return out, nil
}

// Put sends an entity — a creation or an update.
//
// base is the revision the participant edited from, and travels in the
// x-agrirouter-base-revision header. It is nil only for a create: on an update
// a missing base is rejected with [ErrBaseRevisionRequired], because omitting
// it would opt the participant out of concurrency control.
//
// The returned entity is the resulting canonical object and MUST be applied
// exactly as an object delivered on the stream is. It is not an
// acknowledgement: origin suppression keeps this revision off the sender's own
// stream, so this response is the only place the sender learns the
// agrirouterId assigned to a newly created object, or sees the merged result
// where agrirouter reconciled the write against a concurrent change instead of
// rejecting it. See "Applying what agrirouter returns" in specification.md.
func (e *Endpoint) Put(ctx context.Context, ent oapi.Entity, base *int) (oapi.Entity, error) {
	env, err := EnvelopeOf(ent)
	if err != nil {
		return oapi.Entity{}, err
	}
	if env.LocalId == nil || *env.LocalId == "" {
		return oapi.Entity{}, fmt.Errorf(
			"agmasync: sending a %s: localId is required on send", env.Type)
	}
	localID := *env.LocalId

	var res writeResult

	switch env.Type {
	case TypeOrganization:
		if _, cErr := ent.AsOrganization(); cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		body, bErr := writable(ent)
		if bErr != nil {
			return oapi.Entity{}, bErr
		}
		r, hErr := e.client.api.PutOrganizationWithBodyWithResponse(ctx, localID,
			&oapi.PutOrganizationParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			}, "application/json", body)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypePerson:
		if _, cErr := ent.AsPerson(); cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		body, bErr := writable(ent)
		if bErr != nil {
			return oapi.Entity{}, bErr
		}
		r, hErr := e.client.api.PutPersonWithBodyWithResponse(ctx, localID,
			&oapi.PutPersonParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			}, "application/json", body)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeFarm:
		if _, cErr := ent.AsFarm(); cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		body, bErr := writable(ent)
		if bErr != nil {
			return oapi.Entity{}, bErr
		}
		r, hErr := e.client.api.PutFarmWithBodyWithResponse(ctx, localID,
			&oapi.PutFarmParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			}, "application/json", body)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeField:
		if _, cErr := ent.AsField(); cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		body, bErr := writable(ent)
		if bErr != nil {
			return oapi.Entity{}, bErr
		}
		r, hErr := e.client.api.PutFieldWithBodyWithResponse(ctx, localID,
			&oapi.PutFieldParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			}, "application/json", body)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeFieldBoundary:
		if _, cErr := ent.AsFieldBoundary(); cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		body, bErr := writable(ent)
		if bErr != nil {
			return oapi.Entity{}, bErr
		}
		r, hErr := e.client.api.PutFieldBoundaryWithBodyWithResponse(ctx, localID,
			&oapi.PutFieldBoundaryParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			}, "application/json", body)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	default:
		return oapi.Entity{}, fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, env.Type)
	}

	if resErr := res.err(); resErr != nil {
		return oapi.Entity{}, resErr
	}
	return applied(env.Type, res.body)
}

// Deactivate signals that an entity was deactivated in its source system.
//
// The word covers archival, deletion, or any state in which the source no
// longer considers the entity active. It is a lifecycle transition of the
// canonical object rather than a removal: the object and its identifier
// mapping are retained, so references and synchronization stay intact.
//
// The operation is idempotent, and base means different things on either side
// of that. Deactivating an entity that is still active is an ordinary write
// and is subject to concurrency control: agrirouter compares base against the
// object's current revision and rejects the call where the two cannot be
// reconciled, so a deactivation racing a concurrent edit is a conflict of
// which only one side succeeds. Once the object is already inactive, base is
// ignored, no new revision is produced, and nothing is forwarded — which is
// what makes it safe to retry a call whose outcome was never observed.
//
// Reactivation is an ordinary Put with active set to true.
func (e *Endpoint) Deactivate(
	ctx context.Context, t EntityType, localID string, base *int,
) (oapi.Entity, error) {
	var res writeResult

	switch t {
	case TypeOrganization:
		r, hErr := e.client.api.DeactivateOrganizationWithResponse(ctx, localID,
			&oapi.DeactivateOrganizationParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypePerson:
		r, hErr := e.client.api.DeactivatePersonWithResponse(ctx, localID,
			&oapi.DeactivatePersonParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeFarm:
		r, hErr := e.client.api.DeactivateFarmWithResponse(ctx, localID,
			&oapi.DeactivateFarmParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeField:
		r, hErr := e.client.api.DeactivateFieldWithResponse(ctx, localID,
			&oapi.DeactivateFieldParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeFieldBoundary:
		r, hErr := e.client.api.DeactivateFieldBoundaryWithResponse(ctx, localID,
			&oapi.DeactivateFieldBoundaryParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterTenantId:     e.tenantID,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	default:
		return oapi.Entity{}, fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}

	if resErr := res.err(); resErr != nil {
		return oapi.Entity{}, resErr
	}
	return applied(t, res.body)
}

// Request asks for a single entity by its canonical identifier.
//
// It answers 202 and nothing else: the object itself arrives asynchronously on
// the application's event stream. It is delivered even when this endpoint was
// the object's last writer — origin suppression exists to avoid handing an
// endpoint a revision it already holds, and a request states the opposite.
//
// A request never widens what a participant can see, opt-in being the only
// filter on delivery. It exists because delivered is not held: an object lost
// locally, or one referenced by a live-stream object that the initial-load
// stream has not reached yet. A participant that has lost too many objects to
// name asks for the whole canonical set instead. See "Requesting objects (lazy
// loading)" in specification.md.
func (e *Endpoint) Request(ctx context.Context, t EntityType, agrirouterID uuid.UUID) error {
	body := oapi.EntityRequest{AgrirouterId: agrirouterID}

	switch t {
	case TypeOrganization:
		r, err := e.client.api.RequestOrganizationWithResponse(ctx,
			&oapi.RequestOrganizationParams{XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypePerson:
		r, err := e.client.api.RequestPersonWithResponse(ctx,
			&oapi.RequestPersonParams{XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeFarm:
		r, err := e.client.api.RequestFarmWithResponse(ctx,
			&oapi.RequestFarmParams{XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeField:
		r, err := e.client.api.RequestFieldWithResponse(ctx,
			&oapi.RequestFieldParams{XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeFieldBoundary:
		r, err := e.client.api.RequestFieldBoundaryWithResponse(ctx,
			&oapi.RequestFieldBoundaryParams{XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	default:
		return fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}
}

func firstNonNil[T any](a, b *T) *T {
	if a != nil {
		return a
	}
	return b
}

func convErr(t EntityType, err error) error {
	return fmt.Errorf("agmasync: converting %s entity: %w", t, err)
}

func transportErr(err error) error {
	return fmt.Errorf("agmasync: request failed: %w", err)
}

// errorFrom adapts an ErrorResponse (used by the endpoint-management
// operations) to the Error shape [writeResult] branches on.
func errorFrom(e *oapi.ErrorResponse) *oapi.Error {
	if e == nil {
		return nil
	}
	return &oapi.Error{Message: e.Message}
}
