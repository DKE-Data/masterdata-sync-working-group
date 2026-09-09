package agmasync

import (
	"context"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/google/uuid"
)

// Bind declares that a canonical object is one this endpoint already holds,
// under its own localID.
//
// agrirouter never infers a mapping from the content of an object, so
// recognising a delivered object is something the participant has to say out
// loud. Binding is not a data write: it creates no revision, does not change
// sourceEndpointId, and is delivered to nobody.
//
// Until it has bound, a participant MUST NOT send that object. An unbound send
// does not resolve against the mapping and therefore creates a second
// canonical object for the same entity — the duplicate this operation exists
// to prevent.
//
// The mapping is keyed by the endpoint, so this binds for this endpoint alone:
// a participant that keeps one store behind several endpoints binds once per
// endpoint, often against the same localID, and a sibling endpoint's bindings
// neither satisfy this one nor conflict with it.
//
// Within this endpoint a local identifier denotes exactly one canonical object,
// so binding a second one is [ErrMappingConflict]. The bindings produced while reconciling a whole
// canonical set travel in bulk on the initial-load confirmation instead; see
// [Endpoint.ConfirmReconciled]. See "Identifier mapping" in specification.md.
func (e *Endpoint) Bind(
	ctx context.Context, t EntityType, localID string, agrirouterID uuid.UUID,
) error {
	switch t {
	case TypeOrganization:
		r, err := e.client.api.BindOrganizationMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindOrganizationMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	case TypePerson:
		r, err := e.client.api.BindPersonMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindPersonMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	case TypeFarm:
		r, err := e.client.api.BindFarmMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindFarmMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	case TypeField:
		r, err := e.client.api.BindFieldMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindFieldMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	case TypeFieldBoundary:
		r, err := e.client.api.BindFieldBoundaryMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.BindFieldBoundaryMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{r.StatusCode(), r.JSON403, r.JSON404, r.JSON409, r.Body}.err()

	default:
		return fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}
}

// Unbind declares that this endpoint no longer holds a canonical object
// under localID — deleted locally, or discarded while it was not a participant.
//
// It is the counterpart of [Endpoint.Bind] and, like it, a claim about this
// endpoint's own store that agrirouter records and never infers. It is not
// the correction of a mistaken binding, and it is not a deactivation: it
// removes no canonical object, touches no other endpoint's mapping — a sibling
// of the same application included — creates no revision, and reaches nobody.
//
// It also does not narrow what the endpoint receives, opt-in being the only
// such filter. The object's next change is delivered again, now carrying no
// localId, and the participant must then treat it as an object it does not
// hold — creating it locally and binding the identifier it issues. A
// participant that wants it back sooner uses [Endpoint.Request] rather than
// waiting for a change.
//
// Without this, a participant whose local copy is gone has no way out:
// recreating the object mints a new local identifier, and binding that
// identifier collides with the stale pair. The response is 204 whether or not
// a mapping existed, so a retry after a lost response is safe.
func (e *Endpoint) Unbind(
	ctx context.Context, t EntityType, localID string, agrirouterID uuid.UUID,
) error {
	switch t {
	case TypeOrganization:
		r, err := e.client.api.UnbindOrganizationMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindOrganizationMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	case TypePerson:
		r, err := e.client.api.UnbindPersonMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindPersonMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	case TypeFarm:
		r, err := e.client.api.UnbindFarmMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindFarmMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	case TypeField:
		r, err := e.client.api.UnbindFieldMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindFieldMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	case TypeFieldBoundary:
		r, err := e.client.api.UnbindFieldBoundaryMappingWithResponse(ctx, localID, agrirouterID,
			&oapi.UnbindFieldBoundaryMappingParams{XAgrirouterEndpointId: e.id})
		if err != nil {
			return transportErr(err)
		}
		return mappingResult{statusCode: r.StatusCode(), forbidden: r.JSON403,
			notFound: r.JSON404, body: r.Body}.err()

	default:
		return fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}
}
