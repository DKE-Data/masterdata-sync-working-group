package agmasync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// PutParty sends a party. PutFarm, PutField, and PutFieldBoundary likewise
// send an entity (a creation or an update).
//
// base is the revision the participant edited from, and travels in the
// x-agrirouter-base-revision header. It is nil only for a create: on an update
// a missing base is rejected with [ErrBaseRevisionRequired], because omitting
// it would opt the participant out of concurrency control.
//
// The entity is a merge patch: an attribute it leaves out is left as it is,
// and one sent as null is removed — the models' nullable fields say which. It
// must carry its local_id; its type is set from the model. Required attributes are
// required on every write. See "Writing an entity" in specification.md.
//
// The returned model is the resulting canonical object and MUST be applied
// exactly as an object delivered on the stream is. It is not an
// acknowledgement: origin suppression keeps this revision off the sender's own
// stream, so this response is the only place the sender learns the
// agrirouterId assigned to a newly created object, or sees the merged result
// where agrirouter reconciled the write against a concurrent change instead of
// rejecting it. See "Applying what agrirouter returns" in specification.md.
func PutParty(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.Party, base *int,
) (oapi.Party, error) {
	v.Type = string(TypeParty)
	return put(TypeParty, v.LocalId, func(localID string) (*oapi.PutPartyResponse, error) {
		return api.PutPartyWithResponse(ctx, localID, &oapi.PutPartyParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		}, v)
	})
}

// PutFarm sends a farm. See [PutParty].
func PutFarm(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.Farm, base *int,
) (oapi.Farm, error) {
	v.Type = string(TypeFarm)
	return put(TypeFarm, v.LocalId, func(localID string) (*oapi.PutFarmResponse, error) {
		return api.PutFarmWithResponse(ctx, localID, &oapi.PutFarmParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		}, v)
	})
}

// PutField sends a field. See [PutParty].
func PutField(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.Field, base *int,
) (oapi.Field, error) {
	v.Type = string(TypeField)
	return put(TypeField, v.LocalId, func(localID string) (*oapi.PutFieldResponse, error) {
		return api.PutFieldWithResponse(ctx, localID, &oapi.PutFieldParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		}, v)
	})
}

// PutFieldBoundary sends a field boundary. See [PutParty].
func PutFieldBoundary(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.FieldBoundary, base *int,
) (oapi.FieldBoundary, error) {
	v.Type = string(TypeFieldBoundary)
	return put(TypeFieldBoundary, v.LocalId, func(localID string) (*oapi.PutFieldBoundaryResponse, error) {
		return api.PutFieldBoundaryWithResponse(ctx, localID, &oapi.PutFieldBoundaryParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		}, v)
	})
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
//
// The result is the canonical object as it now stands, decoded as a delivery
// of type t is.
func Deactivate(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	t EntityType, localID string, base *int,
) (Object, error) {
	var res writeResult
	switch t {
	case TypeParty:
		r, err := api.DeactivatePartyWithResponse(ctx, localID, &oapi.DeactivatePartyParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		})
		if err != nil {
			return Object{}, transportErr(err)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeFarm:
		r, err := api.DeactivateFarmWithResponse(ctx, localID, &oapi.DeactivateFarmParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		})
		if err != nil {
			return Object{}, transportErr(err)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeField:
		r, err := api.DeactivateFieldWithResponse(ctx, localID, &oapi.DeactivateFieldParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		})
		if err != nil {
			return Object{}, transportErr(err)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	case TypeFieldBoundary:
		r, err := api.DeactivateFieldBoundaryWithResponse(ctx, localID, &oapi.DeactivateFieldBoundaryParams{
			XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
		})
		if err != nil {
			return Object{}, transportErr(err)
		}
		res = writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403, notFound: r.JSON404,
			precond: r.JSON412, required: r.JSON428, body: r.Body,
		}
	default:
		return Object{}, fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}
	if err := res.err(); err != nil {
		return Object{}, err
	}
	return objectAs(t, res.body)
}

// putResponse is what the generated put operations answer with, whichever
// entity type they write.
type putResponse[M any] interface {
	StatusCode() int
	GetBody() []byte
	GetJSON200() *M
	GetJSON201() *M
	GetJSON400() *oapi.Error
	GetJSON403() *oapi.Error
	GetJSON409() *oapi.MappingConflictError
	GetJSON412() *oapi.RevisionConflictError
	GetJSON428() *oapi.RevisionConflictError
}

// put sends an entity under its localID through send, the generated operation
// for its type, and reads the canonical object it answers with.
func put[M any, R putResponse[M]](t EntityType, localID *string, send func(localID string) (R, error)) (M, error) {
	var zero M
	if localID == nil || *localID == "" {
		return zero, localIDRequired(t)
	}
	r, err := send(*localID)
	if err != nil {
		return zero, transportErr(err)
	}
	return written(writeResult{
		statusCode: r.StatusCode(), validation: r.GetJSON400(), forbidden: r.GetJSON403(),
		conflict: r.GetJSON409(), precond: r.GetJSON412(), required: r.GetJSON428(), body: r.GetBody(),
	}, r.GetJSON200(), r.GetJSON201())
}

// written turns a write's outcome into the canonical object it returned, as
// the generated client decoded it: a 200 for an update, a 201 for a create.
func written[M any](res writeResult, ok, created *M) (M, error) {
	var zero M
	if err := res.err(); err != nil {
		return zero, err
	}
	switch {
	case ok != nil:
		return *ok, nil
	case created != nil:
		return *created, nil
	default:
		return zero, fmt.Errorf("%w: HTTP %d carried no %T", ErrEmptyResponse, res.statusCode, zero)
	}
}

func localIDRequired(t EntityType) error {
	return fmt.Errorf("%w: sending a %s", ErrLocalIDRequired, t)
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
//
// It is also the way out of holding an object whose reference target it does
// not hold — a farm delivered with an owner reference carrying no localId.
// Neither identifier names that target on a send, so the object stays
// unwritable until the target is requested, created locally, and bound with
// [Bind]. See "References" in specification.md.
func Request(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	t EntityType, agrirouterID uuid.UUID,
) error {
	body := oapi.EntityRequest{AgrirouterId: agrirouterID}

	switch t {
	case TypeParty:
		r, err := api.RequestPartyWithResponse(ctx,
			&oapi.RequestPartyParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeFarm:
		r, err := api.RequestFarmWithResponse(ctx,
			&oapi.RequestFarmParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeField:
		r, err := api.RequestFieldWithResponse(ctx,
			&oapi.RequestFieldParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return writeResult{
			statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body,
		}.err()

	case TypeFieldBoundary:
		r, err := api.RequestFieldBoundaryWithResponse(ctx,
			&oapi.RequestFieldBoundaryParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
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

func transportErr(err error) error {
	return fmt.Errorf("agmasync: request failed: %w", err)
}
