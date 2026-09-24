package testrouter

import (
	"context"
	"encoding/json"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter/oapi"
)

// This file implements oapi.StrictServerInterface. Each entity type has its own
// four operations and its own generated response types, so the twenty entity
// handlers below are the same handler written five times over. The behaviour
// lives in the generic helpers in handlers_core.go; these only carry a
// result into the concrete response type the generated code demands.
//
// It is generated-shaped rather than generated: the specification's decision to
// give every entity type its own paths, rather than one polymorphic resource,
// is what produces the repetition, and it is worth seeing.

// PutOrganization implements oapi.StrictServerInterface.
func (r *Router) PutOrganization(
	ctx context.Context, req oapi.PutOrganizationRequestObject,
) (oapi.PutOrganizationResponseObject, error) {
	raw, created, err := r.doPut(ctx, agmasync.TypeOrganization, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 400:
			return oapi.PutOrganization400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		case 403:
			return oapi.PutOrganization403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.PutOrganization409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		case 428:
			return oapi.PutOrganization428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.PutOrganization412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Organization
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	if created {
		return oapi.PutOrganization201JSONResponse(entity), nil
	}
	return oapi.PutOrganization200JSONResponse(entity), nil
}

// DeactivateOrganization implements oapi.StrictServerInterface.
func (r *Router) DeactivateOrganization(
	ctx context.Context, req oapi.DeactivateOrganizationRequestObject,
) (oapi.DeactivateOrganizationResponseObject, error) {
	raw, err := r.doDeactivate(ctx, agmasync.TypeOrganization, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.DeactivateOrganization403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.DeactivateOrganization404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.DeactivateOrganization428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.DeactivateOrganization412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Organization
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.DeactivateOrganization200JSONResponse(entity), nil
}

// BindOrganizationMapping implements oapi.StrictServerInterface.
func (r *Router) BindOrganizationMapping(
	ctx context.Context, req oapi.BindOrganizationMappingRequestObject,
) (oapi.BindOrganizationMappingResponseObject, error) {
	err := r.doBind(ctx, agmasync.TypeOrganization, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.BindOrganizationMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.BindOrganizationMapping409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.BindOrganizationMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		}
	}
	return oapi.BindOrganizationMapping204Response{}, nil
}

// UnbindOrganizationMapping implements oapi.StrictServerInterface.
func (r *Router) UnbindOrganizationMapping(
	ctx context.Context, req oapi.UnbindOrganizationMappingRequestObject,
) (oapi.UnbindOrganizationMappingResponseObject, error) {
	err := r.doUnbind(ctx, agmasync.TypeOrganization, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.UnbindOrganizationMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.UnbindOrganizationMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.UnbindOrganizationMapping204Response{}, nil
}

// RequestOrganization implements oapi.StrictServerInterface.
func (r *Router) RequestOrganization(
	ctx context.Context, req oapi.RequestOrganizationRequestObject,
) (oapi.RequestOrganizationResponseObject, error) {
	if req.Body == nil {
		return oapi.RequestOrganization404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(oapi.Error{Message: "no request body"})}, nil
	}
	err := r.doRequest(ctx, agmasync.TypeOrganization, req.Body.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.RequestOrganization403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.RequestOrganization404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.RequestOrganization202Response{}, nil
}

// PutPerson implements oapi.StrictServerInterface.
func (r *Router) PutPerson(
	ctx context.Context, req oapi.PutPersonRequestObject,
) (oapi.PutPersonResponseObject, error) {
	raw, created, err := r.doPut(ctx, agmasync.TypePerson, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 400:
			return oapi.PutPerson400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		case 403:
			return oapi.PutPerson403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.PutPerson409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		case 428:
			return oapi.PutPerson428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.PutPerson412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Person
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	if created {
		return oapi.PutPerson201JSONResponse(entity), nil
	}
	return oapi.PutPerson200JSONResponse(entity), nil
}

// DeactivatePerson implements oapi.StrictServerInterface.
func (r *Router) DeactivatePerson(
	ctx context.Context, req oapi.DeactivatePersonRequestObject,
) (oapi.DeactivatePersonResponseObject, error) {
	raw, err := r.doDeactivate(ctx, agmasync.TypePerson, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.DeactivatePerson403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.DeactivatePerson404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.DeactivatePerson428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.DeactivatePerson412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Person
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.DeactivatePerson200JSONResponse(entity), nil
}

// BindPersonMapping implements oapi.StrictServerInterface.
func (r *Router) BindPersonMapping(
	ctx context.Context, req oapi.BindPersonMappingRequestObject,
) (oapi.BindPersonMappingResponseObject, error) {
	err := r.doBind(ctx, agmasync.TypePerson, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.BindPersonMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.BindPersonMapping409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.BindPersonMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		}
	}
	return oapi.BindPersonMapping204Response{}, nil
}

// UnbindPersonMapping implements oapi.StrictServerInterface.
func (r *Router) UnbindPersonMapping(
	ctx context.Context, req oapi.UnbindPersonMappingRequestObject,
) (oapi.UnbindPersonMappingResponseObject, error) {
	err := r.doUnbind(ctx, agmasync.TypePerson, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.UnbindPersonMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.UnbindPersonMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.UnbindPersonMapping204Response{}, nil
}

// RequestPerson implements oapi.StrictServerInterface.
func (r *Router) RequestPerson(
	ctx context.Context, req oapi.RequestPersonRequestObject,
) (oapi.RequestPersonResponseObject, error) {
	if req.Body == nil {
		return oapi.RequestPerson404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(oapi.Error{Message: "no request body"})}, nil
	}
	err := r.doRequest(ctx, agmasync.TypePerson, req.Body.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.RequestPerson403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.RequestPerson404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.RequestPerson202Response{}, nil
}

// PutFarm implements oapi.StrictServerInterface.
func (r *Router) PutFarm(
	ctx context.Context, req oapi.PutFarmRequestObject,
) (oapi.PutFarmResponseObject, error) {
	raw, created, err := r.doPut(ctx, agmasync.TypeFarm, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 400:
			return oapi.PutFarm400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		case 403:
			return oapi.PutFarm403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.PutFarm409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		case 428:
			return oapi.PutFarm428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.PutFarm412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Farm
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	if created {
		return oapi.PutFarm201JSONResponse(entity), nil
	}
	return oapi.PutFarm200JSONResponse(entity), nil
}

// DeactivateFarm implements oapi.StrictServerInterface.
func (r *Router) DeactivateFarm(
	ctx context.Context, req oapi.DeactivateFarmRequestObject,
) (oapi.DeactivateFarmResponseObject, error) {
	raw, err := r.doDeactivate(ctx, agmasync.TypeFarm, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.DeactivateFarm403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.DeactivateFarm404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.DeactivateFarm428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.DeactivateFarm412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Farm
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.DeactivateFarm200JSONResponse(entity), nil
}

// BindFarmMapping implements oapi.StrictServerInterface.
func (r *Router) BindFarmMapping(
	ctx context.Context, req oapi.BindFarmMappingRequestObject,
) (oapi.BindFarmMappingResponseObject, error) {
	err := r.doBind(ctx, agmasync.TypeFarm, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.BindFarmMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.BindFarmMapping409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.BindFarmMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		}
	}
	return oapi.BindFarmMapping204Response{}, nil
}

// UnbindFarmMapping implements oapi.StrictServerInterface.
func (r *Router) UnbindFarmMapping(
	ctx context.Context, req oapi.UnbindFarmMappingRequestObject,
) (oapi.UnbindFarmMappingResponseObject, error) {
	err := r.doUnbind(ctx, agmasync.TypeFarm, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.UnbindFarmMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.UnbindFarmMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.UnbindFarmMapping204Response{}, nil
}

// RequestFarm implements oapi.StrictServerInterface.
func (r *Router) RequestFarm(
	ctx context.Context, req oapi.RequestFarmRequestObject,
) (oapi.RequestFarmResponseObject, error) {
	if req.Body == nil {
		return oapi.RequestFarm404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(oapi.Error{Message: "no request body"})}, nil
	}
	err := r.doRequest(ctx, agmasync.TypeFarm, req.Body.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.RequestFarm403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.RequestFarm404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.RequestFarm202Response{}, nil
}

// PutField implements oapi.StrictServerInterface.
func (r *Router) PutField(
	ctx context.Context, req oapi.PutFieldRequestObject,
) (oapi.PutFieldResponseObject, error) {
	raw, created, err := r.doPut(ctx, agmasync.TypeField, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 400:
			return oapi.PutField400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		case 403:
			return oapi.PutField403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.PutField409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		case 428:
			return oapi.PutField428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.PutField412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Field
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	if created {
		return oapi.PutField201JSONResponse(entity), nil
	}
	return oapi.PutField200JSONResponse(entity), nil
}

// DeactivateField implements oapi.StrictServerInterface.
func (r *Router) DeactivateField(
	ctx context.Context, req oapi.DeactivateFieldRequestObject,
) (oapi.DeactivateFieldResponseObject, error) {
	raw, err := r.doDeactivate(ctx, agmasync.TypeField, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.DeactivateField403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.DeactivateField404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.DeactivateField428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.DeactivateField412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Field
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.DeactivateField200JSONResponse(entity), nil
}

// BindFieldMapping implements oapi.StrictServerInterface.
func (r *Router) BindFieldMapping(
	ctx context.Context, req oapi.BindFieldMappingRequestObject,
) (oapi.BindFieldMappingResponseObject, error) {
	err := r.doBind(ctx, agmasync.TypeField, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.BindFieldMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.BindFieldMapping409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.BindFieldMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		}
	}
	return oapi.BindFieldMapping204Response{}, nil
}

// UnbindFieldMapping implements oapi.StrictServerInterface.
func (r *Router) UnbindFieldMapping(
	ctx context.Context, req oapi.UnbindFieldMappingRequestObject,
) (oapi.UnbindFieldMappingResponseObject, error) {
	err := r.doUnbind(ctx, agmasync.TypeField, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.UnbindFieldMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.UnbindFieldMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.UnbindFieldMapping204Response{}, nil
}

// RequestField implements oapi.StrictServerInterface.
func (r *Router) RequestField(
	ctx context.Context, req oapi.RequestFieldRequestObject,
) (oapi.RequestFieldResponseObject, error) {
	if req.Body == nil {
		return oapi.RequestField404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(oapi.Error{Message: "no request body"})}, nil
	}
	err := r.doRequest(ctx, agmasync.TypeField, req.Body.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.RequestField403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.RequestField404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.RequestField202Response{}, nil
}

// PutFieldBoundary implements oapi.StrictServerInterface.
func (r *Router) PutFieldBoundary(
	ctx context.Context, req oapi.PutFieldBoundaryRequestObject,
) (oapi.PutFieldBoundaryResponseObject, error) {
	raw, created, err := r.doPut(ctx, agmasync.TypeFieldBoundary, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 400:
			return oapi.PutFieldBoundary400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		case 403:
			return oapi.PutFieldBoundary403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.PutFieldBoundary409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		case 428:
			return oapi.PutFieldBoundary428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.PutFieldBoundary412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.FieldBoundary
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	if created {
		return oapi.PutFieldBoundary201JSONResponse(entity), nil
	}
	return oapi.PutFieldBoundary200JSONResponse(entity), nil
}

// DeactivateFieldBoundary implements oapi.StrictServerInterface.
func (r *Router) DeactivateFieldBoundary(
	ctx context.Context, req oapi.DeactivateFieldBoundaryRequestObject,
) (oapi.DeactivateFieldBoundaryResponseObject, error) {
	raw, err := r.doDeactivate(ctx, agmasync.TypeFieldBoundary, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.DeactivateFieldBoundary403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.DeactivateFieldBoundary404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.DeactivateFieldBoundary428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.DeactivateFieldBoundary412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.FieldBoundary
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.DeactivateFieldBoundary200JSONResponse(entity), nil
}

// BindFieldBoundaryMapping implements oapi.StrictServerInterface.
func (r *Router) BindFieldBoundaryMapping(
	ctx context.Context, req oapi.BindFieldBoundaryMappingRequestObject,
) (oapi.BindFieldBoundaryMappingResponseObject, error) {
	err := r.doBind(ctx, agmasync.TypeFieldBoundary, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.BindFieldBoundaryMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.BindFieldBoundaryMapping409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.BindFieldBoundaryMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		}
	}
	return oapi.BindFieldBoundaryMapping204Response{}, nil
}

// UnbindFieldBoundaryMapping implements oapi.StrictServerInterface.
func (r *Router) UnbindFieldBoundaryMapping(
	ctx context.Context, req oapi.UnbindFieldBoundaryMappingRequestObject,
) (oapi.UnbindFieldBoundaryMappingResponseObject, error) {
	err := r.doUnbind(ctx, agmasync.TypeFieldBoundary, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.UnbindFieldBoundaryMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.UnbindFieldBoundaryMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.UnbindFieldBoundaryMapping204Response{}, nil
}

// RequestFieldBoundary implements oapi.StrictServerInterface.
func (r *Router) RequestFieldBoundary(
	ctx context.Context, req oapi.RequestFieldBoundaryRequestObject,
) (oapi.RequestFieldBoundaryResponseObject, error) {
	if req.Body == nil {
		return oapi.RequestFieldBoundary404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(oapi.Error{Message: "no request body"})}, nil
	}
	err := r.doRequest(ctx, agmasync.TypeFieldBoundary, req.Body.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.RequestFieldBoundary403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.RequestFieldBoundary404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.RequestFieldBoundary202Response{}, nil
}
