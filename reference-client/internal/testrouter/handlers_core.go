package testrouter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter/oapi"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// echoContextKey carries the echo context through the strict handler, which
// only passes a context.Context. The bearer token identifies the application
// and is not a parameter of any operation, so the handlers need the request.
type echoContextKey struct{}

// withEchoContext is the middleware that makes the above work.
func withEchoContext(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		req := c.Request()
		c.SetRequest(req.WithContext(context.WithValue(req.Context(), echoContextKey{}, c)))
		return next(c)
	}
}

func echoFrom(ctx context.Context) (echo.Context, bool) {
	c, ok := ctx.Value(echoContextKey{}).(echo.Context)
	return c, ok
}

// fault is a store error reduced to what a response needs.
type fault struct {
	status    int
	message   string
	current   int
	rejection *oapiRejection
}

func (f fault) error() oapi.Error { return oapi.Error{Message: f.message} }

func (f fault) revisionConflict() oapi.RevisionConflictError {
	return oapi.RevisionConflictError{CurrentRevision: f.current, Message: f.message}
}

func (f fault) mappingConflict() oapi.MappingConflictError {
	out := oapi.MappingConflictError{Message: f.message}
	if f.rejection != nil {
		out.Rejection = toWireRejection(*f.rejection)
	}
	return out
}

// faultOf maps a store failure onto the status the specification names for it.
func faultOf(err error) fault {
	var rev *revisionConflict
	if errors.As(err, &rev) {
		return fault{status: http.StatusPreconditionFailed, message: rev.Error(), current: rev.current}
	}

	var mc *mappingConflict
	if errors.As(err, &mc) {
		rejection := oapiRejection{Reason: mc.reason, Existing: mc.existing}
		if mc.existing != nil {
			rejection.LocalID = mc.existing.LocalID
			rejection.AgrirouterID = mc.existing.AgrirouterID
		}
		return fault{
			status:    http.StatusConflict,
			message:   "identifier already bound",
			rejection: &rejection,
		}
	}

	switch {
	case errors.Is(err, errBaseRequired):
		return fault{
			status:  http.StatusPreconditionRequired,
			message: "an update must carry the revision it was made from",
		}
	case errors.Is(err, errForbidden):
		return fault{
			status:  http.StatusForbidden,
			message: "the acting endpoint is not opted into this entity type",
		}
	case errors.Is(err, errNotFound):
		return fault{status: http.StatusNotFound, message: "not found"}
	case errors.Is(err, errUnresolvedRef):
		return fault{
			status: http.StatusBadRequest,
			message: "a reference does not resolve: send the target before " +
				"the object referencing it",
		}
	case errors.Is(err, errInitialLoadConflict):
		return fault{status: http.StatusConflict, message: err.Error()}
	default:
		return fault{status: http.StatusBadRequest, message: err.Error()}
	}
}

func toWireRejection(r oapiRejection) oapi.IdMappingRejection {
	out := oapi.IdMappingRejection{
		LocalId:      r.LocalID,
		AgrirouterId: r.AgrirouterID,
		Reason:       r.Reason,
	}
	if r.Existing != nil {
		out.ExistingMapping = &oapi.IdMappingBinding{
			LocalId:      r.Existing.LocalID,
			AgrirouterId: r.Existing.AgrirouterID,
		}
	}
	return out
}

// doPut is the shared body of the five Put handlers.
func (r *Router) doPut(
	ctx context.Context, typ agmasync.EntityType, localID string,
	endpointID uuid.UUID, base *int, body any,
) (json.RawMessage, bool, error) {
	ep, err := r.endpointFrom(ctx, endpointID)
	if err != nil {
		return nil, false, err
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, false, err
	}
	if err := rejectServerAssigned(raw); err != nil {
		return nil, false, err
	}

	r.observer.record("put", map[string]any{
		"externalEndpointId": ep.externalID,
		"entityType":         string(typ),
		"localId":            localID,
		"baseRevision":       base,
	})

	obj, created, err := r.store.put(ep, typ, localID, raw, base)
	if err != nil {
		return nil, false, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return r.store.renderLocked(obj, ep), created, nil
}

// doDeactivate is the shared body of the five Deactivate handlers.
func (r *Router) doDeactivate(
	ctx context.Context, typ agmasync.EntityType, localID string,
	endpointID uuid.UUID, base *int,
) (json.RawMessage, error) {
	ep, err := r.endpointFrom(ctx, endpointID)
	if err != nil {
		return nil, err
	}

	r.observer.record("deactivate", map[string]any{
		"externalEndpointId": ep.externalID,
		"entityType":         string(typ),
		"localId":            localID,
		"baseRevision":       base,
	})

	obj, err := r.store.deactivate(ep, typ, localID, base)
	if err != nil {
		return nil, err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return r.store.renderLocked(obj, ep), nil
}

// doBind is the shared body of the five bind handlers.
func (r *Router) doBind(
	ctx context.Context, typ agmasync.EntityType, localID string,
	agrirouterID uuid.UUID, endpointID uuid.UUID,
) error {
	ep, err := r.endpointFrom(ctx, endpointID)
	if err != nil {
		return err
	}
	r.observer.record("bind", map[string]any{
		"externalEndpointId": ep.externalID,
		"entityType":         string(typ),
		"localId":            localID,
		"agrirouterId":       agrirouterID.String(),
	})
	return r.store.bind(ep, typ, localID, agrirouterID)
}

// doUnbind is the shared body of the five unbind handlers.
func (r *Router) doUnbind(
	ctx context.Context, typ agmasync.EntityType, localID string,
	agrirouterID uuid.UUID, endpointID uuid.UUID,
) error {
	ep, err := r.endpointFrom(ctx, endpointID)
	if err != nil {
		return err
	}
	r.observer.record("unbind", map[string]any{
		"externalEndpointId": ep.externalID,
		"entityType":         string(typ),
		"localId":            localID,
		"agrirouterId":       agrirouterID.String(),
	})
	return r.store.unbind(ep, typ, localID, agrirouterID)
}

// doRequest is the shared body of the five request handlers.
//
// It answers 202 and delivers the object on the application's event stream,
// bypassing origin suppression: a request states that the participant does not
// hold the object, and refetching something it wrote itself is the case the
// operation exists for.
func (r *Router) doRequest(
	ctx context.Context, typ agmasync.EntityType, agrirouterID uuid.UUID, endpointID uuid.UUID,
) error {
	ep, err := r.endpointFrom(ctx, endpointID)
	if err != nil {
		return err
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	obj, ok := r.store.objects[agrirouterID]
	if !ok || obj.typ != typ {
		return errNotFound
	}
	if !r.store.entitled(ep, obj) {
		return errForbidden
	}

	r.observer.record("request", map[string]any{
		"externalEndpointId": ep.externalID,
		"entityType":         string(typ),
		"agrirouterId":       agrirouterID.String(),
	})
	r.store.deliverTo(ep, obj)
	return nil
}

func (r *Router) endpointFrom(ctx context.Context, endpointID uuid.UUID) (*endpoint, error) {
	c, ok := echoFrom(ctx)
	if !ok {
		return nil, errForbidden
	}
	return r.actingEndpoint(c, endpointID)
}

// --- the operations that are not per entity type -------------------------

// There is no handler for reading a configuration or a selection. The
// declaration is not a resource of its own — it is written on the endpoint and
// echoed back by the write — and the user's selection reaches a participant on
// the ROUTE_CHANGED frame and nowhere else, so there is nothing here to serve
// and nothing for a participant to poll.

// PutEndpoint implements oapi.StrictServerInterface.
//
// The participant's step, and a create-or-update: an external identifier the
// router has not heard of is the participant's first call, and it answers 201.
//
// It enables nothing either way: no route is created, no load starts, and an
// endpoint declared for everything exchanges nothing until a user selects from
// it in agrirouter.
func (r *Router) PutEndpoint(
	ctx context.Context, req oapi.PutEndpointRequestObject,
) (oapi.PutEndpointResponseObject, error) {
	c, ok := echoFrom(ctx)
	if !ok {
		return oapi.PutEndpoint403JSONResponse{}, nil
	}
	if req.Body == nil || req.Body.Masterdata == nil {
		return oapi.PutEndpoint400JSONResponse{Message: "missing configuration"}, nil
	}
	masterdata := *req.Body.Masterdata

	types := make([]agmasync.EntityType, 0, len(masterdata.Capabilities))
	for _, toggle := range masterdata.Capabilities {
		// A toggle carries the entity `type` value, not the collection segment.
		// Taken strictly: a router that also accepted `field-boundaries` would
		// let a participant sending the wrong vocabulary pass here and fail
		// against a conforming implementation.
		typ := agmasync.EntityType(toggle.EntityType)
		if !typ.Valid() {
			return oapi.PutEndpoint400JSONResponse{
				Message: "unknown entity type " + toggle.EntityType,
			}, nil
		}
		types = append(types, typ)
	}
	// Closure is checked before anything is created, so a rejected declaration
	// leaves no endpoint behind. r.declare checks it again, on the update path.
	if len(agmasync.DependencyClosure(types)) != len(types) {
		return oapi.PutEndpoint400JSONResponse{Message: errNotClosed.Error()}, nil
	}

	created := false
	ep, err := r.endpointByExternalID(c, req.ExternalId)
	switch {
	case errors.Is(err, errNotFound):
		// Not an error: this is the create half. The endpoint belongs to the
		// application the token names, in the tenant the header names.
		appID, ok := application(c)
		if !ok {
			return oapi.PutEndpoint403JSONResponse{}, nil
		}
		ep = r.insertEndpoint(req.ExternalId, appID, req.Params.XAgrirouterTenantId)
		created = true
	case err != nil:
		// The identifier is taken by another application. Saying so would tell
		// an application about endpoints that are none of its business, so this
		// is a 403 rather than a 404.
		return oapi.PutEndpoint403JSONResponse{}, nil
	}

	if err := r.declare(ep, types); err != nil {
		return oapi.PutEndpoint400JSONResponse{Message: err.Error()}, nil
	}
	declared := r.declarationFor(ep)
	body := oapi.Endpoint{
		Id:                ep.id,
		ExternalId:        ep.externalID,
		ApplicationId:     req.Body.ApplicationId,
		SoftwareVersionId: req.Body.SoftwareVersionId,
		EndpointType:      oapi.EndpointType(req.Body.EndpointType),
		TenantId:          ep.tenantID.String(),
		Capabilities:      req.Body.Capabilities,
		Masterdata:        &declared,
	}
	if created {
		return oapi.PutEndpoint201JSONResponse(body), nil
	}
	return oapi.PutEndpoint200JSONResponse(body), nil
}

// GetInitialLoadStatus implements oapi.StrictServerInterface.
func (r *Router) GetInitialLoadStatus(
	ctx context.Context, req oapi.GetInitialLoadStatusRequestObject,
) (oapi.GetInitialLoadStatusResponseObject, error) {
	c, ok := echoFrom(ctx)
	if !ok {
		return oapi.GetInitialLoadStatus403JSONResponse{}, nil
	}
	ep, err := r.endpointByExternalID(c, req.ExternalId)
	if err != nil {
		if errors.Is(err, errForbidden) {
			return oapi.GetInitialLoadStatus403JSONResponse{}, nil
		}
		return oapi.GetInitialLoadStatus404JSONResponse{}, nil
	}

	status, ok := r.statusFor(ep)
	if !ok {
		// An endpoint opted into no entity type has no initial-load state; the
		// absence of any toggle already says it does not participate.
		return oapi.GetInitialLoadStatus404JSONResponse{}, nil
	}
	return oapi.GetInitialLoadStatus200JSONResponse(status), nil
}

// SetInitialLoadState implements oapi.StrictServerInterface.
func (r *Router) SetInitialLoadState(
	ctx context.Context, req oapi.SetInitialLoadStateRequestObject,
) (oapi.SetInitialLoadStateResponseObject, error) {
	c, ok := echoFrom(ctx)
	if !ok {
		return oapi.SetInitialLoadState403JSONResponse{}, nil
	}
	ep, err := r.endpointByExternalID(c, req.ExternalId)
	if err != nil {
		if errors.Is(err, errForbidden) {
			return oapi.SetInitialLoadState403JSONResponse{}, nil
		}
		return oapi.SetInitialLoadState404JSONResponse{}, nil
	}
	if req.Body == nil {
		return oapi.SetInitialLoadState400JSONResponse{
			ValidationErrorJSONResponse: oapi.ValidationErrorJSONResponse(
				oapi.Error{Message: "no body"}),
		}, nil
	}

	bindings := []binding{}
	if req.Body.IdMappings != nil {
		for _, b := range *req.Body.IdMappings {
			bindings = append(bindings, binding{LocalID: b.LocalId, AgrirouterID: b.AgrirouterId})
		}
	}

	if err := r.advanceLoadState(ep, string(req.Body.State), bindings); err != nil {
		f := faultOf(err)
		if f.status == http.StatusConflict {
			return oapi.SetInitialLoadState409JSONResponse{
				InitialLoadConflictJSONResponse: oapi.InitialLoadConflictJSONResponse(f.error()),
			}, nil
		}
		return oapi.SetInitialLoadState404JSONResponse{}, nil
	}

	status, _ := r.statusFor(ep)
	return oapi.SetInitialLoadState200JSONResponse(status), nil
}

// ReportUserAttention implements oapi.StrictServerInterface.
func (r *Router) ReportUserAttention(
	ctx context.Context, req oapi.ReportUserAttentionRequestObject,
) (oapi.ReportUserAttentionResponseObject, error) {
	c, ok := echoFrom(ctx)
	if !ok {
		return oapi.ReportUserAttention403JSONResponse{}, nil
	}
	ep, err := r.endpointByExternalID(c, req.ExternalId)
	if err != nil {
		if errors.Is(err, errForbidden) {
			return oapi.ReportUserAttention403JSONResponse{}, nil
		}
		return oapi.ReportUserAttention404JSONResponse{}, nil
	}

	if err := r.raiseAwaitingUser(ep); err != nil {
		if errors.Is(err, errInitialLoadConflict) {
			return oapi.ReportUserAttention409JSONResponse(faultOf(err).error()), nil
		}
		return oapi.ReportUserAttention404JSONResponse{}, nil
	}

	status, _ := r.statusFor(ep)
	return oapi.ReportUserAttention200JSONResponse(status), nil
}

// StreamMasterdataEvents implements oapi.StrictServerInterface.
func (r *Router) StreamMasterdataEvents(
	ctx context.Context, req oapi.StreamMasterdataEventsRequestObject,
) (oapi.StreamMasterdataEventsResponseObject, error) {
	c, ok := echoFrom(ctx)
	if !ok {
		return nil, errForbidden
	}
	appID, ok := application(c)
	if !ok {
		return nil, errForbidden
	}

	lastEventID := ""
	if req.Params.LastEventID != nil {
		lastEventID = *req.Params.LastEventID
	}
	r.observer.record("streamOpened", map[string]any{
		"applicationId": appID,
		"lastEventId":   lastEventID,
		"resumed":       lastEventID != "",
	})

	return oapi.StreamMasterdataEvents200TexteventStreamResponse{
		Body: r.liveStream(ctx, appID, lastEventID),
	}, nil
}

// StreamInitialLoadEvents implements oapi.StrictServerInterface.
func (r *Router) StreamInitialLoadEvents(
	ctx context.Context, req oapi.StreamInitialLoadEventsRequestObject,
) (oapi.StreamInitialLoadEventsResponseObject, error) {
	c, ok := echoFrom(ctx)
	if !ok {
		return oapi.StreamInitialLoadEvents403JSONResponse{}, nil
	}
	ep, err := r.endpointByExternalID(c, req.ExternalId)
	if err != nil {
		if errors.Is(err, errForbidden) {
			return oapi.StreamInitialLoadEvents403JSONResponse{}, nil
		}
		return oapi.StreamInitialLoadEvents404JSONResponse{}, nil
	}

	r.observer.record("initialLoadStreamOpened", map[string]any{
		"externalEndpointId": ep.externalID,
	})

	return oapi.StreamInitialLoadEvents200TexteventStreamResponse{
		Body: r.initialLoadStream(ctx, ep),
	}, nil
}

// statusFor renders an endpoint's initial-load status.
func (r *Router) statusFor(ep *endpoint) (oapi.InitialLoadStatus, bool) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if ep.load == nil {
		return oapi.InitialLoadStatus{}, false
	}

	id := ep.id
	awaiting := ep.load.awaitingUser
	updated := ep.load.updatedAt
	status := oapi.InitialLoadStatus{
		EndpointId:              &id,
		State:                   oapi.InitialLoadState(ep.load.state),
		AwaitingUser:            &awaiting,
		PreviousLoadCompletedAt: ep.previousLoadCompletedAt,
		UpdatedAt:               &updated,
	}
	if len(ep.load.rejected) > 0 {
		rejections := make([]oapi.IdMappingRejection, 0, len(ep.load.rejected))
		for _, r := range ep.load.rejected {
			rejections = append(rejections, toWireRejection(r))
		}
		status.RejectedIdMappings = &rejections
	}
	return status, true
}
