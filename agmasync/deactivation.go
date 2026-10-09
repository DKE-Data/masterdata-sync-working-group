package agmasync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

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
