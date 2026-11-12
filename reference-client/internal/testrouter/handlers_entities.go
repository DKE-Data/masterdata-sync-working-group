package testrouter

import (
	"context"
	"encoding/json"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter/oapi"
)

// This file implements oapi.StrictServerInterface. Each entity type has its own
// five operations and its own generated response types, so the twenty entity
// handlers below are the same handler written four times over. The behaviour
// lives in the generic helpers in handlers_core.go; these only carry a
// result into the concrete response type the generated code demands.
//
// It is generated-shaped rather than generated: the specification's decision to
// give every entity type its own paths, rather than one polymorphic resource,
// is what produces the repetition, and it is worth seeing.

// PutParty implements oapi.StrictServerInterface.
func (r *Router) PutParty(
	ctx context.Context, req oapi.PutPartyRequestObject,
) (oapi.PutPartyResponseObject, error) {
	raw, created, err := r.doPut(ctx, agmasync.TypeParty, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 400:
			return oapi.PutParty400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		case 403:
			return oapi.PutParty403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.PutParty409JSONResponse{PutConflictJSONResponse: oapi.PutConflictJSONResponse(f.putConflict())}, nil
		case 428:
			return oapi.PutParty428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.PutParty412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Party
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	if created {
		return oapi.PutParty201JSONResponse(entity), nil
	}
	return oapi.PutParty200JSONResponse(entity), nil
}

// ArchiveParty implements oapi.StrictServerInterface.
func (r *Router) ArchiveParty(
	ctx context.Context, req oapi.ArchivePartyRequestObject,
) (oapi.ArchivePartyResponseObject, error) {
	raw, err := r.doArchive(ctx, agmasync.TypeParty, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.ArchiveParty403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.ArchiveParty404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.ArchiveParty428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.ArchiveParty412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Party
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.ArchiveParty200JSONResponse(entity), nil
}

// UnarchiveParty implements oapi.StrictServerInterface.
func (r *Router) UnarchiveParty(
	ctx context.Context, req oapi.UnarchivePartyRequestObject,
) (oapi.UnarchivePartyResponseObject, error) {
	raw, err := r.doUnarchive(ctx, agmasync.TypeParty, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.UnarchiveParty403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.UnarchiveParty404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.UnarchiveParty428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.UnarchiveParty412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Party
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.UnarchiveParty200JSONResponse(entity), nil
}

// BindPartyMapping implements oapi.StrictServerInterface.
func (r *Router) BindPartyMapping(
	ctx context.Context, req oapi.BindPartyMappingRequestObject,
) (oapi.BindPartyMappingResponseObject, error) {
	err := r.doBind(ctx, agmasync.TypeParty, req.LocalId, req.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.BindPartyMapping403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 409:
			return oapi.BindPartyMapping409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.BindPartyMapping404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		}
	}
	return oapi.BindPartyMapping204Response{}, nil
}

// CorrectPartyMappings implements oapi.StrictServerInterface.
func (r *Router) CorrectPartyMappings(
	ctx context.Context, req oapi.CorrectPartyMappingsRequestObject,
) (oapi.CorrectPartyMappingsResponseObject, error) {
	err := r.doCorrect(ctx, agmasync.TypeParty, req.Body, req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.CorrectPartyMappings403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.CorrectPartyMappings404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 409:
			return oapi.CorrectPartyMappings409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.CorrectPartyMappings400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		}
	}
	return oapi.CorrectPartyMappings204Response{}, nil
}

// RequestParty implements oapi.StrictServerInterface.
func (r *Router) RequestParty(
	ctx context.Context, req oapi.RequestPartyRequestObject,
) (oapi.RequestPartyResponseObject, error) {
	if req.Body == nil {
		return oapi.RequestParty404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(oapi.Error{Message: "no request body"})}, nil
	}
	err := r.doRequest(ctx, agmasync.TypeParty, req.Body.AgrirouterId,
		req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		if f.status == 403 {
			return oapi.RequestParty403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		}
		return oapi.RequestParty404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
	}
	return oapi.RequestParty202Response{}, nil
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
			return oapi.PutFarm409JSONResponse{PutConflictJSONResponse: oapi.PutConflictJSONResponse(f.putConflict())}, nil
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

// ArchiveFarm implements oapi.StrictServerInterface.
func (r *Router) ArchiveFarm(
	ctx context.Context, req oapi.ArchiveFarmRequestObject,
) (oapi.ArchiveFarmResponseObject, error) {
	raw, err := r.doArchive(ctx, agmasync.TypeFarm, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.ArchiveFarm403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.ArchiveFarm404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.ArchiveFarm428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.ArchiveFarm412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Farm
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.ArchiveFarm200JSONResponse(entity), nil
}

// UnarchiveFarm implements oapi.StrictServerInterface.
func (r *Router) UnarchiveFarm(
	ctx context.Context, req oapi.UnarchiveFarmRequestObject,
) (oapi.UnarchiveFarmResponseObject, error) {
	raw, err := r.doUnarchive(ctx, agmasync.TypeFarm, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.UnarchiveFarm403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.UnarchiveFarm404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.UnarchiveFarm428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.UnarchiveFarm412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Farm
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.UnarchiveFarm200JSONResponse(entity), nil
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

// CorrectFarmMappings implements oapi.StrictServerInterface.
func (r *Router) CorrectFarmMappings(
	ctx context.Context, req oapi.CorrectFarmMappingsRequestObject,
) (oapi.CorrectFarmMappingsResponseObject, error) {
	err := r.doCorrect(ctx, agmasync.TypeFarm, req.Body, req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.CorrectFarmMappings403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.CorrectFarmMappings404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 409:
			return oapi.CorrectFarmMappings409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.CorrectFarmMappings400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		}
	}
	return oapi.CorrectFarmMappings204Response{}, nil
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
			return oapi.PutField409JSONResponse{PutConflictJSONResponse: oapi.PutConflictJSONResponse(f.putConflict())}, nil
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

// ArchiveField implements oapi.StrictServerInterface.
func (r *Router) ArchiveField(
	ctx context.Context, req oapi.ArchiveFieldRequestObject,
) (oapi.ArchiveFieldResponseObject, error) {
	raw, err := r.doArchive(ctx, agmasync.TypeField, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.ArchiveField403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.ArchiveField404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.ArchiveField428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.ArchiveField412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Field
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.ArchiveField200JSONResponse(entity), nil
}

// UnarchiveField implements oapi.StrictServerInterface.
func (r *Router) UnarchiveField(
	ctx context.Context, req oapi.UnarchiveFieldRequestObject,
) (oapi.UnarchiveFieldResponseObject, error) {
	raw, err := r.doUnarchive(ctx, agmasync.TypeField, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.UnarchiveField403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.UnarchiveField404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.UnarchiveField428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.UnarchiveField412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.Field
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.UnarchiveField200JSONResponse(entity), nil
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

// CorrectFieldMappings implements oapi.StrictServerInterface.
func (r *Router) CorrectFieldMappings(
	ctx context.Context, req oapi.CorrectFieldMappingsRequestObject,
) (oapi.CorrectFieldMappingsResponseObject, error) {
	err := r.doCorrect(ctx, agmasync.TypeField, req.Body, req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.CorrectFieldMappings403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.CorrectFieldMappings404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 409:
			return oapi.CorrectFieldMappings409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.CorrectFieldMappings400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		}
	}
	return oapi.CorrectFieldMappings204Response{}, nil
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
			return oapi.PutFieldBoundary409JSONResponse{PutConflictJSONResponse: oapi.PutConflictJSONResponse(f.putConflict())}, nil
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

// ArchiveFieldBoundary implements oapi.StrictServerInterface.
func (r *Router) ArchiveFieldBoundary(
	ctx context.Context, req oapi.ArchiveFieldBoundaryRequestObject,
) (oapi.ArchiveFieldBoundaryResponseObject, error) {
	raw, err := r.doArchive(ctx, agmasync.TypeFieldBoundary, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.ArchiveFieldBoundary403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.ArchiveFieldBoundary404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.ArchiveFieldBoundary428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.ArchiveFieldBoundary412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.FieldBoundary
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.ArchiveFieldBoundary200JSONResponse(entity), nil
}

// UnarchiveFieldBoundary implements oapi.StrictServerInterface.
func (r *Router) UnarchiveFieldBoundary(
	ctx context.Context, req oapi.UnarchiveFieldBoundaryRequestObject,
) (oapi.UnarchiveFieldBoundaryResponseObject, error) {
	raw, err := r.doUnarchive(ctx, agmasync.TypeFieldBoundary, req.LocalId,
		req.Params.XAgrirouterEndpointId, req.Params.XAgrirouterBaseRevision)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.UnarchiveFieldBoundary403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.UnarchiveFieldBoundary404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 428:
			return oapi.UnarchiveFieldBoundary428JSONResponse{BaseRevisionRequiredJSONResponse: oapi.BaseRevisionRequiredJSONResponse(f.revisionConflict())}, nil
		default:
			return oapi.UnarchiveFieldBoundary412JSONResponse{RevisionConflictJSONResponse: oapi.RevisionConflictJSONResponse(f.revisionConflict())}, nil
		}
	}

	var entity oapi.FieldBoundary
	if err := json.Unmarshal(raw, &entity); err != nil {
		return nil, err
	}
	return oapi.UnarchiveFieldBoundary200JSONResponse(entity), nil
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

// CorrectFieldBoundaryMappings implements oapi.StrictServerInterface.
func (r *Router) CorrectFieldBoundaryMappings(
	ctx context.Context, req oapi.CorrectFieldBoundaryMappingsRequestObject,
) (oapi.CorrectFieldBoundaryMappingsResponseObject, error) {
	err := r.doCorrect(ctx, agmasync.TypeFieldBoundary, req.Body, req.Params.XAgrirouterEndpointId)
	if err != nil {
		f := faultOf(err)
		switch f.status {
		case 403:
			return oapi.CorrectFieldBoundaryMappings403JSONResponse{ForbiddenJSONResponse: oapi.ForbiddenJSONResponse(f.error())}, nil
		case 404:
			return oapi.CorrectFieldBoundaryMappings404JSONResponse{NotFoundJSONResponse: oapi.NotFoundJSONResponse(f.error())}, nil
		case 409:
			return oapi.CorrectFieldBoundaryMappings409JSONResponse{MappingConflictJSONResponse: oapi.MappingConflictJSONResponse(f.mappingConflict())}, nil
		default:
			return oapi.CorrectFieldBoundaryMappings400JSONResponse{ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(f.error())}, nil
		}
	}
	return oapi.CorrectFieldBoundaryMappings204Response{}, nil
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
