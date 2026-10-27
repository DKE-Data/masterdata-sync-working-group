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
// The mapping is keyed by the endpoint, so the binding covers this endpoint
// only: a participant with one store behind several endpoints binds once per
// endpoint, often under the same localID. A localID denotes one canonical
// object per endpoint; binding it to a second is [ErrMappingConflict], and a
// sibling endpoint's bindings never are. Bindings
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

// Correction is the entry of a [CorrectMappings] request: localID ends mapped to
// agrirouterID, or to nothing where agrirouterID is nil.
func Correction(localID string, agrirouterID *uuid.UUID) oapi.IdMappingCorrectionEntry {
	e := oapi.IdMappingCorrectionEntry{LocalId: localID}
	if agrirouterID != nil {
		e.AgrirouterId.Set(*agrirouterID)
	} else {
		e.AgrirouterId.SetNull()
	}
	return e
}

// CorrectMappings replaces bindings this endpoint's user found wrong. Each
// listed localID ends mapped to the given canonical object, or to none; build
// the entries with [Correction].
//
// Uniqueness is checked on the result, so a crosswise swap — the usual wrong
// binding, each half valid on its own — is one call, applied whole or not at
// all. A target bound to a localID the request does not list is not taken
// from it: that is [ErrMappingConflict], naming that mapping. A localID or
// target listed twice is [ErrValidation].
//
// It corrects identity, not content: no revision, delivered to nobody, and
// revisions written through the wrong binding stand. Repair them with ordinary
// writes, and the local records with [Request]. It is not for a local deletion,
// which is a deactivation. See "Correcting a binding" in specification.md.
func CorrectMappings(
	ctx context.Context, api *oapi.ClientWithResponses,
	endpointID, tenantID uuid.UUID,
	t EntityType, entries []oapi.IdMappingCorrectionEntry,
) error {
	body := oapi.IdMappingCorrection{IdMappings: entries}
	switch t {
	case TypeParty:
		r, err := api.CorrectPartyMappingsWithResponse(ctx,
			&oapi.CorrectPartyMappingsParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return correctionResult(r.StatusCode(), r.JSON400, r.JSON403, r.JSON404, r.JSON409, r.Body)

	case TypeFarm:
		r, err := api.CorrectFarmMappingsWithResponse(ctx,
			&oapi.CorrectFarmMappingsParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return correctionResult(r.StatusCode(), r.JSON400, r.JSON403, r.JSON404, r.JSON409, r.Body)

	case TypeField:
		r, err := api.CorrectFieldMappingsWithResponse(ctx,
			&oapi.CorrectFieldMappingsParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return correctionResult(r.StatusCode(), r.JSON400, r.JSON403, r.JSON404, r.JSON409, r.Body)

	case TypeFieldBoundary:
		r, err := api.CorrectFieldBoundaryMappingsWithResponse(ctx,
			&oapi.CorrectFieldBoundaryMappingsParams{XAgrirouterEndpointId: endpointID, XAgrirouterTenantId: tenantID}, body)
		if err != nil {
			return transportErr(err)
		}
		return correctionResult(r.StatusCode(), r.JSON400, r.JSON403, r.JSON404, r.JSON409, r.Body)

	default:
		return fmt.Errorf("agmasync: %w: %q", ErrUnknownEntityType, t)
	}
}

func correctionResult(
	status int, validation, forbidden, notFound *oapi.Error, conflict *oapi.MappingConflictError, body []byte,
) error {
	return writeResult{
		statusCode: status,
		validation: validation,
		forbidden:  forbidden,
		notFound:   notFound,
		conflict:   conflict,
		body:       body,
	}.err()
}
