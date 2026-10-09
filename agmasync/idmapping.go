package agmasync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// Bind declares that this endpoint already holds a canonical object under its
// own localID.
//
// A participant MUST bind before sending the object; an unbound send creates a
// duplicate canonical object.
//
// The mapping is keyed by application and tenant, so the binding covers every
// endpoint of the application in this tenant. A localID denotes one canonical
// object per tenant; binding it to a second is [ErrMappingConflict]. Bindings
// from a whole initial load go in bulk via [ConfirmReconciled] instead. See
// "Identifier mapping" in specification.md.
func Bind(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	t EntityType, localID string, agrirouterID uuid.UUID,
) error {
	switch t {
	case TypeParty:
		r, err := api.BindPartyMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindPartyMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	case TypeFarm:
		r, err := api.BindFarmMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindFarmMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	case TypeField:
		r, err := api.BindFieldMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindFieldMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	case TypeFieldBoundary:
		r, err := api.BindFieldBoundaryMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindFieldBoundaryMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	default:
		return fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}
}

// Unbind declares that this endpoint no longer holds the canonical object
// under localID, e.g. it was deleted locally. Without it, recreating the object
// locally mints a new localID that collides with the stale pair on [Bind].
//
// It is not a deactivation: it removes no canonical object, creates no revision,
// and reaches nobody. Nor does it filter delivery: the object's next change
// arrives again without a localId, to be created locally and bound; [Request]
// fetches it sooner.
//
// Idempotent: the response is 204 whether or not a mapping existed.
func Unbind(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	t EntityType, localID string, agrirouterID uuid.UUID,
) error {
	switch t {
	case TypeParty:
		r, err := api.UnbindPartyMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindPartyMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	case TypeFarm:
		r, err := api.UnbindFarmMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindFarmMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	case TypeField:
		r, err := api.UnbindFieldMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindFieldMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	case TypeFieldBoundary:
		r, err := api.UnbindFieldBoundaryMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindFieldBoundaryMappingParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	default:
		return fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}
}
