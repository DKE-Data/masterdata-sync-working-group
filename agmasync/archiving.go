package agmasync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// Archive signals that an entity was archived in its source system.
//
// The word covers archiving, deletion, or any state in which the source no
// longer considers the entity live. It is a lifecycle transition of the
// canonical object rather than a removal: the object and its identifier
// mapping are retained, so references and synchronization stay intact.
//
// The operation is idempotent, and base means different things on either side
// of that. Archiving an entity that is not archived is an ordinary write and
// is subject to concurrency control: agrirouter compares base against the
// object's current revision and rejects the call where the two cannot be
// reconciled, so an archiving racing a concurrent edit is a conflict of which
// only one side succeeds. Once the object is already archived, base is
// ignored, no new revision is produced, and nothing is forwarded — which is
// what makes it safe to retry a call whose outcome was never observed.
//
// [Unarchive] is the way back. A Put never changes the state: it must carry
// the object's current `archived`.
//
// The result is the canonical object as it now stands, decoded as a delivery
// of type t is.
func Archive(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	t EntityType, localID string, base *int,
) (Object, error) {
	var res writeResult
	switch t {
	case TypeParty:
		r, err := api.ArchivePartyWithResponse(ctx, localID, &oapi.ArchivePartyParams{
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
		r, err := api.ArchiveFarmWithResponse(ctx, localID, &oapi.ArchiveFarmParams{
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
		r, err := api.ArchiveFieldWithResponse(ctx, localID, &oapi.ArchiveFieldParams{
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
		r, err := api.ArchiveFieldBoundaryWithResponse(ctx, localID, &oapi.ArchiveFieldBoundaryParams{
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
