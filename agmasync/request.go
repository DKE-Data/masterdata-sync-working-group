package agmasync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

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
