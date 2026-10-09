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
//
//nolint:dupl // One readable function per entity type is preferred over a generic helper.
func PutParty(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.Party, base *int,
) (oapi.Party, error) {
	if v.LocalId == nil || *v.LocalId == "" {
		return oapi.Party{}, localIDRequired(TypeParty)
	}
	v.Type = string(TypeParty)
	r, err := api.PutPartyWithResponse(ctx, *v.LocalId, &oapi.PutPartyParams{
		XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
	}, v)
	if err != nil {
		return oapi.Party{}, transportErr(err)
	}
	if err := (writeResult{
		statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
		conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
	}).err(); err != nil {
		return oapi.Party{}, err
	}
	if r.JSON200 != nil {
		return *r.JSON200, nil
	}
	if r.JSON201 != nil {
		return *r.JSON201, nil
	}
	return oapi.Party{}, emptyWriteResponse(TypeParty, r.StatusCode())
}

// PutFarm sends a farm. See [PutParty].
//
//nolint:dupl // One readable function per entity type is preferred over a generic helper.
func PutFarm(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.Farm, base *int,
) (oapi.Farm, error) {
	if v.LocalId == nil || *v.LocalId == "" {
		return oapi.Farm{}, localIDRequired(TypeFarm)
	}
	v.Type = string(TypeFarm)
	r, err := api.PutFarmWithResponse(ctx, *v.LocalId, &oapi.PutFarmParams{
		XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
	}, v)
	if err != nil {
		return oapi.Farm{}, transportErr(err)
	}
	if err := (writeResult{
		statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
		conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
	}).err(); err != nil {
		return oapi.Farm{}, err
	}
	if r.JSON200 != nil {
		return *r.JSON200, nil
	}
	if r.JSON201 != nil {
		return *r.JSON201, nil
	}
	return oapi.Farm{}, emptyWriteResponse(TypeFarm, r.StatusCode())
}

// PutField sends a field. See [PutParty].
//
//nolint:dupl // One readable function per entity type is preferred over a generic helper.
func PutField(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.Field, base *int,
) (oapi.Field, error) {
	if v.LocalId == nil || *v.LocalId == "" {
		return oapi.Field{}, localIDRequired(TypeField)
	}
	v.Type = string(TypeField)
	r, err := api.PutFieldWithResponse(ctx, *v.LocalId, &oapi.PutFieldParams{
		XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
	}, v)
	if err != nil {
		return oapi.Field{}, transportErr(err)
	}
	if err := (writeResult{
		statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
		conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
	}).err(); err != nil {
		return oapi.Field{}, err
	}
	if r.JSON200 != nil {
		return *r.JSON200, nil
	}
	if r.JSON201 != nil {
		return *r.JSON201, nil
	}
	return oapi.Field{}, emptyWriteResponse(TypeField, r.StatusCode())
}

// PutFieldBoundary sends a field boundary. See [PutParty].
//
//nolint:dupl // One readable function per entity type is preferred over a generic helper.
func PutFieldBoundary(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	v oapi.FieldBoundary, base *int,
) (oapi.FieldBoundary, error) {
	if v.LocalId == nil || *v.LocalId == "" {
		return oapi.FieldBoundary{}, localIDRequired(TypeFieldBoundary)
	}
	v.Type = string(TypeFieldBoundary)
	r, err := api.PutFieldBoundaryWithResponse(ctx, *v.LocalId, &oapi.PutFieldBoundaryParams{
		XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID, XAgrirouterBaseRevision: base,
	}, v)
	if err != nil {
		return oapi.FieldBoundary{}, transportErr(err)
	}
	if err := (writeResult{
		statusCode: r.StatusCode(), validation: r.JSON400, forbidden: r.JSON403,
		conflict: r.JSON409, precond: r.JSON412, required: r.JSON428, body: r.Body,
	}).err(); err != nil {
		return oapi.FieldBoundary{}, err
	}
	if r.JSON200 != nil {
		return *r.JSON200, nil
	}
	if r.JSON201 != nil {
		return *r.JSON201, nil
	}
	return oapi.FieldBoundary{}, emptyWriteResponse(TypeFieldBoundary, r.StatusCode())
}

func localIDRequired(t EntityType) error {
	return fmt.Errorf("%w: sending a %s", ErrLocalIDRequired, t)
}

// emptyWriteResponse is a successful write that carried no object to apply.
func emptyWriteResponse(t EntityType, statusCode int) error {
	return fmt.Errorf("%w: HTTP %d carried no %s", ErrEmptyResponse, statusCode, t)
}
