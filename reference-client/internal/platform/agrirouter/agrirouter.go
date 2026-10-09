// Package agrirouter is the sample platform's connection to agrirouter: the
// generated client, and a handle on one endpoint that calls the agmasync
// operations as it.
//
// agmasync speaks in the typed models, one per entity type. The platform
// stores and passes around the Entity union instead, since its tables hold all
// four types and its store keeps an entity as the JSON it was written as. The
// two meet here: [Endpoint] takes and returns the union, and [EntityOf] turns
// an object delivered on a stream into one.
package agrirouter

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// Client is the generated client the agmasync operations take.
type Client = oapi.ClientWithResponses

// Option configures a [Client].
type Option = oapi.ClientOption

// WithHTTPClient supplies the HTTP client used for both ordinary requests and
// the event streams. Streams are long-lived, so it must not carry a request
// timeout.
func WithHTTPClient(hc oapi.HttpRequestDoer) Option { return oapi.WithHTTPClient(hc) }

// WithBearerToken authenticates every request with a static bearer token. See
// "Security considerations" in specification.md.
func WithBearerToken(token string) Option {
	return oapi.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	})
}

// NewClient builds a client against the given agrirouter base URL.
func NewClient(baseURL string, opts ...Option) (*Client, error) {
	api, err := oapi.NewClientWithResponses(baseURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("agrirouter: building client: %w", err)
	}
	return api, nil
}

// Endpoint acts as one of the application's endpoints.
type Endpoint struct {
	api *Client

	// id is the agrirouter identifier of the endpoint, which the entity
	// operations name it by.
	id uuid.UUID

	// externalID is the application's own identifier for the endpoint, which
	// the initial-load operations address it by.
	externalID string

	tenantID uuid.UUID
}

// For returns a handle on one of the application's endpoints.
func For(api *Client, endpointID uuid.UUID, externalEndpointID string, tenantID uuid.UUID) *Endpoint {
	return &Endpoint{api: api, id: endpointID, externalID: externalEndpointID, tenantID: tenantID}
}

// ID returns the endpoint's agrirouter identifier.
func (e *Endpoint) ID() uuid.UUID { return e.id }

// TenantID returns the agrirouter tenant the endpoint belongs to.
func (e *Endpoint) TenantID() uuid.UUID { return e.tenantID }

// ExternalID returns the application's own identifier for the endpoint.
func (e *Endpoint) ExternalID() string { return e.externalID }

// Client returns the client the endpoint acts through.
func (e *Endpoint) Client() *Client { return e.api }

// Put sends an entity through the write for its type. See [agmasync.PutParty].
func (e *Endpoint) Put(ctx context.Context, ent oapi.Entity, base *int) (oapi.Entity, error) {
	env, err := EnvelopeOf(ent)
	if err != nil {
		return oapi.Entity{}, err
	}
	var out oapi.Entity
	switch env.Type {
	case agmasync.TypeParty:
		v, err := ent.AsParty()
		if err != nil {
			return oapi.Entity{}, err
		}
		if v, err = agmasync.PutParty(ctx, e.api, e.id, e.tenantID, v, base); err != nil {
			return oapi.Entity{}, err
		}
		err = out.FromParty(v)
		return out, err
	case agmasync.TypeFarm:
		v, err := ent.AsFarm()
		if err != nil {
			return oapi.Entity{}, err
		}
		if v, err = agmasync.PutFarm(ctx, e.api, e.id, e.tenantID, v, base); err != nil {
			return oapi.Entity{}, err
		}
		err = out.FromFarm(v)
		return out, err
	case agmasync.TypeField:
		v, err := ent.AsField()
		if err != nil {
			return oapi.Entity{}, err
		}
		if v, err = agmasync.PutField(ctx, e.api, e.id, e.tenantID, v, base); err != nil {
			return oapi.Entity{}, err
		}
		err = out.FromField(v)
		return out, err
	case agmasync.TypeFieldBoundary:
		v, err := ent.AsFieldBoundary()
		if err != nil {
			return oapi.Entity{}, err
		}
		if v, err = agmasync.PutFieldBoundary(ctx, e.api, e.id, e.tenantID, v, base); err != nil {
			return oapi.Entity{}, err
		}
		err = out.FromFieldBoundary(v)
		return out, err
	default:
		return oapi.Entity{}, fmt.Errorf("agrirouter: %w: %q", agmasync.ErrUnknownEntityType, env.Type)
	}
}

// PutJSON sends an entity's JSON exactly as given, bypassing the typed models.
//
// [Endpoint.Put] goes through the models, which cannot say everything a merge
// patch can: a required attribute left out is sent as its zero value, and one
// the model does not name is dropped. This is for a caller holding the JSON
// another integration would send — the tests and scenarios writing what this
// platform does not originate.
//
// A response other than 200 or 201 is a [*StatusError].
func (e *Endpoint) PutJSON(ctx context.Context, body []byte, base *int) (oapi.Entity, error) {
	env, err := agmasync.EnvelopeOf(body)
	if err != nil {
		return oapi.Entity{}, err
	}
	if env.LocalId == nil || *env.LocalId == "" {
		return oapi.Entity{}, fmt.Errorf("agrirouter: %w: sending a %s", agmasync.ErrLocalIDRequired, env.Type)
	}
	localID := *env.LocalId
	const contentType = "application/json"

	var status int
	var answer []byte
	switch env.Type {
	case agmasync.TypeParty:
		r, err := e.api.PutPartyWithBodyWithResponse(ctx, localID, &oapi.PutPartyParams{
			XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID, XAgrirouterBaseRevision: base,
		}, contentType, bytes.NewReader(body))
		if err != nil {
			return oapi.Entity{}, err
		}
		status, answer = r.StatusCode(), r.Body
	case agmasync.TypeFarm:
		r, err := e.api.PutFarmWithBodyWithResponse(ctx, localID, &oapi.PutFarmParams{
			XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID, XAgrirouterBaseRevision: base,
		}, contentType, bytes.NewReader(body))
		if err != nil {
			return oapi.Entity{}, err
		}
		status, answer = r.StatusCode(), r.Body
	case agmasync.TypeField:
		r, err := e.api.PutFieldWithBodyWithResponse(ctx, localID, &oapi.PutFieldParams{
			XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID, XAgrirouterBaseRevision: base,
		}, contentType, bytes.NewReader(body))
		if err != nil {
			return oapi.Entity{}, err
		}
		status, answer = r.StatusCode(), r.Body
	case agmasync.TypeFieldBoundary:
		r, err := e.api.PutFieldBoundaryWithBodyWithResponse(ctx, localID, &oapi.PutFieldBoundaryParams{
			XAgrirouterEndpointId: e.id, XAgrirouterTenantId: e.tenantID, XAgrirouterBaseRevision: base,
		}, contentType, bytes.NewReader(body))
		if err != nil {
			return oapi.Entity{}, err
		}
		status, answer = r.StatusCode(), r.Body
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return oapi.Entity{}, &StatusError{StatusCode: status, Body: answer}
	}
	var out oapi.Entity
	if err := out.UnmarshalJSON(answer); err != nil {
		return oapi.Entity{}, fmt.Errorf("agrirouter: decoding %s: %w", env.Type, err)
	}
	return out, nil
}

// StatusError is a [Endpoint.PutJSON] that agrirouter refused.
type StatusError struct {
	StatusCode int
	Body       []byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("agrirouter: HTTP %d: %s", e.StatusCode, e.Body)
}

// Deactivate signals that an entity was deactivated. See [agmasync.Deactivate].
func (e *Endpoint) Deactivate(
	ctx context.Context, t agmasync.EntityType, localID string, base *int,
) (oapi.Entity, error) {
	obj, err := agmasync.Deactivate(ctx, e.api, e.id, e.tenantID, t, localID, base)
	if err != nil {
		return oapi.Entity{}, err
	}
	return EntityOf(obj)
}

// Request asks for a single entity. See [agmasync.Request].
func (e *Endpoint) Request(ctx context.Context, t agmasync.EntityType, agrirouterID uuid.UUID) error {
	return agmasync.Request(ctx, e.api, e.id, e.tenantID, t, agrirouterID)
}

// Bind declares that the endpoint holds a canonical object. See [agmasync.Bind].
func (e *Endpoint) Bind(ctx context.Context, t agmasync.EntityType, localID string, agrirouterID uuid.UUID) error {
	return agmasync.Bind(ctx, e.api, e.id, e.tenantID, t, localID, agrirouterID)
}

// Unbind declares that the endpoint no longer holds a canonical object. See
// [agmasync.Unbind].
func (e *Endpoint) Unbind(ctx context.Context, t agmasync.EntityType, localID string, agrirouterID uuid.UUID) error {
	return agmasync.Unbind(ctx, e.api, e.id, e.tenantID, t, localID, agrirouterID)
}

// InitialLoadEvents opens the endpoint's initial-load stream. See
// [agmasync.InitialLoadEvents].
func (e *Endpoint) InitialLoadEvents(ctx context.Context) (*agmasync.Stream, error) {
	return agmasync.InitialLoadEvents(ctx, e.api, e.externalID, e.tenantID)
}

// InitialLoadStatus reads the endpoint's initial-load status. See
// [agmasync.GetInitialLoadStatus].
func (e *Endpoint) InitialLoadStatus(ctx context.Context) (oapi.InitialLoadStatus, error) {
	return agmasync.GetInitialLoadStatus(ctx, e.api, e.externalID, e.tenantID)
}

// SetInitialLoadState moves the endpoint's initial load. See
// [agmasync.SetInitialLoadState].
func (e *Endpoint) SetInitialLoadState(
	ctx context.Context, update oapi.InitialLoadStateUpdate,
) (oapi.InitialLoadStatus, error) {
	return agmasync.SetInitialLoadState(ctx, e.api, e.externalID, e.tenantID, update)
}

// ConfirmReconciled confirms reconciliation. See [agmasync.ConfirmReconciled].
func (e *Endpoint) ConfirmReconciled(
	ctx context.Context, bindings []oapi.IdMappingBinding,
) (oapi.InitialLoadStatus, error) {
	return agmasync.ConfirmReconciled(ctx, e.api, e.externalID, e.tenantID, bindings)
}

// CompleteInitialLoad declares the endpoint has sent everything. See
// [agmasync.CompleteInitialLoad].
func (e *Endpoint) CompleteInitialLoad(ctx context.Context) (oapi.InitialLoadStatus, error) {
	return agmasync.CompleteInitialLoad(ctx, e.api, e.externalID, e.tenantID)
}

// ReportUserAttention reports that reconciliation waits on a person. See
// [agmasync.ReportUserAttention].
func (e *Endpoint) ReportUserAttention(ctx context.Context) (oapi.InitialLoadStatus, error) {
	return agmasync.ReportUserAttention(ctx, e.api, e.externalID, e.tenantID)
}

// EntityOf wraps a decoded object as the union, as an [agmasync.Event] or
// [agmasync.Deactivate] returns it.
func EntityOf(o agmasync.Object) (oapi.Entity, error) {
	var out oapi.Entity
	var err error
	switch {
	case o.Party != nil:
		err = out.FromParty(*o.Party)
	case o.Farm != nil:
		err = out.FromFarm(*o.Farm)
	case o.Field != nil:
		err = out.FromField(*o.Field)
	case o.FieldBoundary != nil:
		err = out.FromFieldBoundary(*o.FieldBoundary)
	default:
		return oapi.Entity{}, fmt.Errorf("agrirouter: %w: %q", agmasync.ErrUnknownEntityType, o.Envelope.Type)
	}
	return out, err
}

// EnvelopeOf reads the common fields of an entity of any type. See
// [agmasync.EnvelopeOf].
func EnvelopeOf(e oapi.Entity) (agmasync.Envelope, error) {
	raw, err := e.MarshalJSON()
	if err != nil {
		return agmasync.Envelope{}, fmt.Errorf("agrirouter: reading entity envelope: %w", err)
	}
	return agmasync.EnvelopeOf(raw)
}

// FromParty wraps a party as an entity.
func FromParty(v oapi.Party) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromParty(v)
}

// FromFarm wraps a farm as an entity.
func FromFarm(v oapi.Farm) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromFarm(v)
}

// FromField wraps a field as an entity.
func FromField(v oapi.Field) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromField(v)
}

// FromFieldBoundary wraps a field boundary as an entity.
func FromFieldBoundary(v oapi.FieldBoundary) (oapi.Entity, error) {
	var e oapi.Entity
	return e, e.FromFieldBoundary(v)
}
