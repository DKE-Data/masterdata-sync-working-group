package agmasync

import (
	"context"
	"fmt"
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
	api     *oapi.ClientWithResponses
	http    *http.Client
	baseURL string
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
	c := &Client{
		http:    &http.Client{},
		baseURL: baseURL,
	}
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
}

// For returns a handle on one of the application's endpoints.
func (c *Client) For(endpointID uuid.UUID, externalEndpointID string) *Endpoint {
	return &Endpoint{client: c, id: endpointID, externalID: externalEndpointID}
}

// ID returns the endpoint's agrirouter identifier.
func (e *Endpoint) ID() uuid.UUID { return e.id }

// ExternalID returns the application's own identifier for the endpoint.
func (e *Endpoint) ExternalID() string { return e.externalID }

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
	var out oapi.Entity

	switch env.Type {
	case TypeOrganization:
		v, cErr := ent.AsOrganization()
		if cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		r, hErr := e.client.api.PutOrganizationWithResponse(ctx, localID,
			&oapi.PutOrganizationParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			}, v)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if v := firstNonNil(r.JSON200, r.JSON201); v != nil {
			out, err = FromOrganization(*v)
		}

	case TypePerson:
		v, cErr := ent.AsPerson()
		if cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		r, hErr := e.client.api.PutPersonWithResponse(ctx, localID,
			&oapi.PutPersonParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			}, v)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if v := firstNonNil(r.JSON200, r.JSON201); v != nil {
			out, err = FromPerson(*v)
		}

	case TypeFarm:
		v, cErr := ent.AsFarm()
		if cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		r, hErr := e.client.api.PutFarmWithResponse(ctx, localID,
			&oapi.PutFarmParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			}, v)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if v := firstNonNil(r.JSON200, r.JSON201); v != nil {
			out, err = FromFarm(*v)
		}

	case TypeField:
		v, cErr := ent.AsField()
		if cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		r, hErr := e.client.api.PutFieldWithResponse(ctx, localID,
			&oapi.PutFieldParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			}, v)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if v := firstNonNil(r.JSON200, r.JSON201); v != nil {
			out, err = FromField(*v)
		}

	case TypeFieldBoundary:
		v, cErr := ent.AsFieldBoundary()
		if cErr != nil {
			return oapi.Entity{}, convErr(env.Type, cErr)
		}
		r, hErr := e.client.api.PutFieldBoundaryWithResponse(ctx, localID,
			&oapi.PutFieldBoundaryParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			}, v)
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
			conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if v := firstNonNil(r.JSON200, r.JSON201); v != nil {
			out, err = FromFieldBoundary(*v)
		}

	default:
		return oapi.Entity{}, fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, env.Type)
	}

	if resErr := res.err(); resErr != nil {
		return oapi.Entity{}, resErr
	}
	if err != nil {
		return oapi.Entity{}, convErr(env.Type, err)
	}
	return out, nil
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
	var out oapi.Entity
	var err error

	switch t {
	case TypeOrganization:
		r, hErr := e.client.api.DeactivateOrganizationWithResponse(ctx, localID,
			&oapi.DeactivateOrganizationParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if r.JSON200 != nil {
			out, err = FromOrganization(*r.JSON200)
		}

	case TypePerson:
		r, hErr := e.client.api.DeactivatePersonWithResponse(ctx, localID,
			&oapi.DeactivatePersonParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if r.JSON200 != nil {
			out, err = FromPerson(*r.JSON200)
		}

	case TypeFarm:
		r, hErr := e.client.api.DeactivateFarmWithResponse(ctx, localID,
			&oapi.DeactivateFarmParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if r.JSON200 != nil {
			out, err = FromFarm(*r.JSON200)
		}

	case TypeField:
		r, hErr := e.client.api.DeactivateFieldWithResponse(ctx, localID,
			&oapi.DeactivateFieldParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if r.JSON200 != nil {
			out, err = FromField(*r.JSON200)
		}

	case TypeFieldBoundary:
		r, hErr := e.client.api.DeactivateFieldBoundaryWithResponse(ctx, localID,
			&oapi.DeactivateFieldBoundaryParams{
				XAgrirouterEndpointId:   e.id,
				XAgrirouterBaseRevision: base,
			})
		if hErr != nil {
			return oapi.Entity{}, transportErr(hErr)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
		if r.JSON200 != nil {
			out, err = FromFieldBoundary(*r.JSON200)
		}

	default:
		return oapi.Entity{}, fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}

	if resErr := res.err(); resErr != nil {
		return oapi.Entity{}, resErr
	}
	if err != nil {
		return oapi.Entity{}, convErr(t, err)
	}
	return out, nil
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
			&oapi.RequestOrganizationParams{XAgrirouterEndpointId: e.id}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypePerson:
		r, err := e.client.api.RequestPersonWithResponse(ctx,
			&oapi.RequestPersonParams{XAgrirouterEndpointId: e.id}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeFarm:
		r, err := e.client.api.RequestFarmWithResponse(ctx,
			&oapi.RequestFarmParams{XAgrirouterEndpointId: e.id}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeField:
		r, err := e.client.api.RequestFieldWithResponse(ctx,
			&oapi.RequestFieldParams{XAgrirouterEndpointId: e.id}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeFieldBoundary:
		r, err := e.client.api.RequestFieldBoundaryWithResponse(ctx,
			&oapi.RequestFieldBoundaryParams{XAgrirouterEndpointId: e.id}, body)
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
