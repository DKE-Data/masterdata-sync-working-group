package testrouter

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter/oapi"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

var errInitialLoadConflict = errors.New("initial load transition out of order")

// errNotClosed is a declaration or a selection that names a type without naming
// what that type references.
var errNotClosed = errors.New("entity types are not dependency-closed")

// errNotDeclared is a user's selection reaching past what the participant said
// its endpoint can exchange. The declaration bounds the selection, which is what
// keeps the two steps from collapsing into one.
var errNotDeclared = errors.New("entity type is not declared for this endpoint")

// Router is the agrirouter side of AgmaSync, in memory.
//
// Mount it in process with [Router.Handler] for a scenario or a test, or run it
// over HTTP with cmd/testrouter for the containerised integration tests. Both
// are the same implementation.
type Router struct {
	store    *store
	hub      *hub
	observer *observer
}

// New builds an empty router: no tenants, no endpoints, no canonical objects.
func New() *Router {
	h := newHub()
	obs := newObserver()
	r := &Router{hub: h, observer: obs}
	r.store = newStore(h, obs)
	return r
}

// SetClock replaces the router's clock, so that a test can assert on
// modifiedAt without racing it.
func (r *Router) SetClock(now func() time.Time) { r.store.now = now }

// Handler returns the router's HTTP surface: the AgmaSync API generated from
// openapi.yaml, plus the /_test control plane.
func (r *Router) Handler() http.Handler {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// The strict handler passes only a context.Context, and the bearer token
	// that names the acting participant is not a parameter of any operation, so
	// the echo context has to travel with the request.
	e.Use(withEchoContext)

	oapi.RegisterHandlers(e, oapi.NewStrictHandler(r, nil))
	r.registerControlPlane(e)
	return e
}

// application reads the acting participant off the request.
//
// A real deployment takes this from an OAuth token, which authorizes an
// application rather than an endpoint — which is exactly why the acting endpoint
// is named per request in a header instead of being derived. Here the bearer
// token is the application identifier, which keeps the distinction visible
// without a token service.
func application(ctx echo.Context) (string, bool) {
	header := ctx.Request().Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return "", false
	}
	return token, true
}

// actingEndpoint resolves the x-agrirouter-endpoint-id header, and checks it
// belongs to the participant the token authorizes.
func (r *Router) actingEndpoint(ctx echo.Context, id uuid.UUID) (*endpoint, error) {
	appID, ok := application(ctx)
	if !ok {
		return nil, errForbidden
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	ep, ok := r.store.endpoints[id]
	if !ok || ep.appID != appID {
		return nil, errForbidden
	}
	return ep, nil
}

// endpointByExternalID resolves the externalEndpointId the configuration and
// initial-load resources are addressed by.
//
// The two identifier styles are not interchangeable: the entity operations name
// the acting endpoint by its agrirouter id in a header, while endpoint
// management, and these resources with it, use the participant's own identifier.
func (r *Router) endpointByExternalID(ctx echo.Context, externalID string) (*endpoint, error) {
	appID, ok := application(ctx)
	if !ok {
		return nil, errForbidden
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	ep, ok := r.store.byExternal[externalID]
	if !ok {
		return nil, errNotFound
	}
	if ep.appID != appID {
		return nil, errForbidden
	}
	return ep, nil
}

// observer records what the router was asked to do, so that a test can assert
// on what the participant actually sent rather than only on what it ended up
// holding.
//
// It is how a scenario checks that a write carried the base revision it should
// have, or that a binding was sent at all — neither of which is visible in the
// participant's own store afterwards.
type observer struct {
	mu      sync.Mutex
	records []Observation
	subs    map[chan Observation]struct{}
}

// Observation is one thing the router saw.
type Observation struct {
	Kind   string         `json:"kind"`
	Detail map[string]any `json:"detail"`
}

func newObserver() *observer {
	return &observer{subs: map[chan Observation]struct{}{}}
}

func (o *observer) record(kind string, detail map[string]any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	obs := Observation{Kind: kind, Detail: detail}
	o.records = append(o.records, obs)
	for ch := range o.subs {
		select {
		case ch <- obs:
		default:
		}
	}
}

// Observations returns everything the router has seen so far.
func (r *Router) Observations() []Observation {
	r.observer.mu.Lock()
	defer r.observer.mu.Unlock()
	out := make([]Observation, len(r.observer.records))
	copy(out, r.observer.records)
	return out
}

// --- control plane -------------------------------------------------------
//
// These endpoints stand in for the parts of agrirouter that are not
// participant-facing: creating a tenant and an endpoint, and the user's opt-in
// decision. None of them exists in openapi.yaml, and a participant never calls
// them — which is the point, since opt-in is a user's decision made in
// agrirouter and initial load follows from it.

func (r *Router) registerControlPlane(e *echo.Echo) {
	e.POST("/_test/tenants", r.createTenant)
	e.POST("/_test/endpoints", r.createEndpoint)
	e.PUT("/_test/endpoints/:externalEndpointId/opt-in", r.setOptIn)
	e.GET("/_test/observations", r.listObservations)
}

// TenantResponse is the control plane's answer to creating a tenant.
type TenantResponse struct {
	TenantID uuid.UUID `json:"tenantId"`
}

func (r *Router) createTenant(ctx echo.Context) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	id := uuid.New()
	r.store.tenants[id] = true
	return ctx.JSON(http.StatusCreated, TenantResponse{TenantID: id})
}

// EndpointRequest creates an endpoint for a participant in a tenant.
type EndpointRequest struct {
	ApplicationID string    `json:"applicationId"`
	TenantID      uuid.UUID `json:"tenantId"`
	ExternalID    string    `json:"externalId"`
}

// EndpointResponse carries the agrirouter identifier the entity operations use.
type EndpointResponse struct {
	EndpointID uuid.UUID `json:"endpointId"`
	ExternalID string    `json:"externalId"`
}

func (r *Router) createEndpoint(ctx echo.Context) error {
	var req EndpointRequest
	if err := ctx.Bind(&req); err != nil {
		return ctx.JSON(http.StatusBadRequest, oapi.Error{Message: err.Error()})
	}

	ep := r.insertEndpoint(req.ExternalID, req.ApplicationID, req.TenantID)

	return ctx.JSON(http.StatusCreated, EndpointResponse{
		EndpointID: ep.id,
		ExternalID: ep.externalID,
	})
}

// insertEndpoint records an endpoint the router had not heard of, minting the
// agrirouter identifier for it.
//
// PutEndpoint creates through here as a participant does, and the control plane
// through here as well, so an endpoint is the same thing however it arrived.
//
// appID is the application that owns the endpoint, which is what entitlement and
// the identifier-mapping namespace are keyed by.
func (r *Router) insertEndpoint(externalID, appID string, tenantID uuid.UUID) *endpoint {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	ep := &endpoint{
		id:         uuid.New(),
		externalID: externalID,
		appID:      appID,
		tenantID:   tenantID,
		declared:   map[agmasync.EntityType]bool{},
		toggles:    map[agmasync.EntityType]bool{},
	}
	r.store.endpoints[ep.id] = ep
	r.store.byExternal[ep.externalID] = ep
	r.store.tenants[ep.tenantID] = true
	return ep
}

// OptInRequest is the user's opt-in decision, made in agrirouter.
type OptInRequest struct {
	EntityTypes []string `json:"entityTypes"`
}

// setOptIn records the opt-in and starts the endpoint's initial load.
//
// The HTTP face of [Router.selectTypes]; the in-process [Router.OptIn] is the
// other. Everything the selection means lives in the one function they share,
// so the two faces cannot drift.
func (r *Router) setOptIn(ctx echo.Context) error {
	var req OptInRequest
	if err := ctx.Bind(&req); err != nil {
		return ctx.JSON(http.StatusBadRequest, oapi.Error{Message: err.Error()})
	}

	ep, ok := r.lookupEndpoint(ctx.Param("externalEndpointId"))
	if !ok {
		return ctx.JSON(http.StatusNotFound, oapi.Error{Message: "no such endpoint"})
	}

	wanted := make([]agmasync.EntityType, 0, len(req.EntityTypes))
	for _, name := range req.EntityTypes {
		typ := agmasync.EntityType(name)
		if !typ.Valid() {
			return ctx.JSON(http.StatusBadRequest,
				oapi.Error{Message: "unknown entity type " + name})
		}
		wanted = append(wanted, typ)
	}

	if err := r.selectTypes(ep, wanted); err != nil {
		return ctx.JSON(http.StatusBadRequest, oapi.Error{Message: err.Error()})
	}

	// The control plane echoes the selection back for the test's benefit. A
	// participant is told it on the ROUTE_CHANGED frame and nowhere else.
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return ctx.JSON(http.StatusOK, OptInRequest{
		EntityTypes: typeNamesOf(selectedTogglesLocked(ep)),
	})
}

// selectTypes applies the user's selection to an endpoint.
//
// This is what opting in *means*, and it is the only place it is written: both
// the control plane's HTTP handler and the in-process [Router.OptIn] come
// through here. Keeping one copy is not tidiness — the two faces are exercised
// by different suites, so a second copy is one that can be wrong for a long time
// without a test noticing.
//
// It states the whole selection rather than adding to it, as the user's own
// choice does: types not named are deselected, and naming none deselects
// everything. The set is closed over entity dependencies first, because a
// receiving endpoint has to be able to resolve every reference on the objects it
// is sent.
func (r *Router) selectTypes(ep *endpoint, types []agmasync.EntityType) error {
	closure := agmasync.DependencyClosure(types)

	// A user can only select from what the participant declared. Nothing else
	// bounds this, so a selection naming an undeclared type is the one case
	// where the two steps could be conflated, and it is refused rather than
	// quietly granted.
	r.store.mu.Lock()
	var undeclared []string
	for _, typ := range closure {
		if !ep.declared[typ] {
			undeclared = append(undeclared, string(typ))
		}
	}
	if len(undeclared) > 0 {
		r.store.mu.Unlock()
		return fmt.Errorf("%w: %s", errNotDeclared, strings.Join(undeclared, ", "))
	}

	added, removed := false, false
	for _, typ := range closure {
		if !ep.toggles[typ] {
			ep.toggles[typ] = true
			added = true
		}
	}
	for typ := range ep.toggles {
		if !slices.Contains(closure, typ) {
			delete(ep.toggles, typ)
			removed = true
		}
	}
	if added || removed {
		ep.selectionChangedAt = r.store.now().UTC()
	}
	empty := len(ep.toggles) == 0
	r.store.mu.Unlock()

	switch {
	case empty:
		// An endpoint has an initial-load state only while it is opted into at
		// least one entity type; the last toggle takes the state with it. What it
		// does not take is previousLoadCompletedAt, which hangs off the endpoint:
		// an endpoint that opts back in is not a newcomer, its identifier mapping
		// having survived, and the set it is then sent is a repeat.
		r.store.mu.Lock()
		ep.load = nil
		r.store.mu.Unlock()
	case added:
		// Only a widening restarts the load. A narrowing leaves the endpoint in
		// step for what it remains opted into, and agrirouter has no more to
		// send than it already had.
		r.startLoad(ep)
	}

	// Every move is announced, narrowings and the empty selection included, and
	// only a move: repeating a selection unchanged is not news.
	if added || removed {
		r.publishSelection(ep)
	}
	return nil
}

func typeNamesOf(toggles []oapi.EntityTypeToggle) []string {
	out := make([]string, 0, len(toggles))
	for _, t := range toggles {
		out = append(out, t.EntityType)
	}
	return out
}

func (r *Router) listObservations(ctx echo.Context) error {
	return ctx.JSON(http.StatusOK, r.Observations())
}

func (r *Router) lookupEndpoint(externalID string) (*endpoint, bool) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	ep, ok := r.store.byExternal[externalID]
	return ep, ok
}

// selectedTogglesLocked renders the entity types the user selected.
//
// An emptied selection renders as an empty list rather than as nothing at all:
// that is what the ROUTE_CHANGED frame states when the user deselects the last
// type, and it is a statement and not an omission.
func selectedTogglesLocked(ep *endpoint) []oapi.EntityTypeToggle {
	types := []oapi.EntityTypeToggle{}
	for _, typ := range agmasync.EntityTypes {
		if ep.toggles[typ] {
			types = append(types, oapi.EntityTypeToggle{EntityType: string(typ)})
		}
	}
	return types
}

// declarationFor renders what the participant said this endpoint can exchange.
// Step one of the two, and it grants nothing.
func (r *Router) declarationFor(ep *endpoint) oapi.MasterdataConfig {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	toggles := []oapi.EntityTypeToggle{}
	for _, typ := range agmasync.EntityTypes {
		if ep.declared[typ] {
			toggles = append(toggles, oapi.EntityTypeToggle{EntityType: string(typ)})
		}
	}
	id := ep.id
	return oapi.MasterdataConfig{EndpointId: &id, Capabilities: toggles}
}

// declare records what the endpoint can exchange, and narrows the user's
// selection to it.
//
// The declaration has to be dependency-closed for the same reason the selection
// does: an endpoint that could receive fields but not the farms they hang off
// could not resolve their references. Narrowing is the only way a participant's
// own call changes what is delivered, and it can only ever remove — withdrawing
// a type has the effect on it that the user deselecting it would.
func (r *Router) declare(ep *endpoint, types []agmasync.EntityType) error {
	closure := agmasync.DependencyClosure(types)
	if len(closure) != len(types) {
		return errNotClosed
	}

	r.store.mu.Lock()
	ep.declared = map[agmasync.EntityType]bool{}
	for _, typ := range types {
		ep.declared[typ] = true
	}
	withdrawn := false
	for typ := range ep.toggles {
		if !ep.declared[typ] {
			delete(ep.toggles, typ)
			withdrawn = true
		}
	}
	empty := withdrawn && len(ep.toggles) == 0
	r.store.mu.Unlock()

	if empty {
		r.store.mu.Lock()
		ep.load = nil
		r.store.mu.Unlock()
	}
	// A withdrawal narrows the selection, which is a move like the user's own
	// and is announced the same way. The participant that made this call is the
	// one told, which is redundant for it and not for its siblings on the stream.
	if withdrawn {
		r.publishSelection(ep)
	}
	return nil
}

// OptIn is the in-process equivalent of the control plane's opt-in — the user's
// selection — for a test or a scenario that mounts the router directly.
//
// It is the same operation the HTTP handler performs, both going through
// [Router.selectTypes], so what it means to select is defined once. Endpoints
// made by [Router.AddEndpoint] have declared everything, so narrowing that with
// [Router.Declare] is what a test does to exercise the bound the declaration
// puts on the selection.
func (r *Router) OptIn(externalID string, types ...agmasync.EntityType) error {
	ep, ok := r.lookupEndpoint(externalID)
	if !ok {
		return errNotFound
	}
	return r.selectTypes(ep, types)
}

// Declare is the in-process equivalent of the participant declaring what its
// endpoint can exchange. It enables nothing on its own.
func (r *Router) Declare(externalID string, types ...agmasync.EntityType) error {
	ep, ok := r.lookupEndpoint(externalID)
	if !ok {
		return errNotFound
	}
	return r.declare(ep, types)
}

// AddEndpoint is the in-process equivalent of creating an endpoint.
//
// It declares every entity type, which is what a participant whose software
// handles all of them does on onboarding, so that a test can go straight to the
// user's selection. Use [Router.Declare] to narrow it.
func (r *Router) AddEndpoint(appID string, tenantID uuid.UUID, externalID string) uuid.UUID {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	declared := map[agmasync.EntityType]bool{}
	for _, typ := range agmasync.EntityTypes {
		declared[typ] = true
	}

	ep := &endpoint{
		id:         uuid.New(),
		externalID: externalID,
		appID:      appID,
		tenantID:   tenantID,
		declared:   declared,
		toggles:    map[agmasync.EntityType]bool{},
	}
	r.store.endpoints[ep.id] = ep
	r.store.byExternal[externalID] = ep
	r.store.tenants[tenantID] = true
	return ep.id
}

// RemoveEndpoint deletes an endpoint from its tenant, as a user does in
// agrirouter.
//
// The canonical objects it contributed stay — they are the tenant's data, and
// other endpoints are synchronizing against them — and so does the identifier
// mapping. The mapping belongs to the participant rather than to the endpoint,
// so removing one endpoint discards nothing: a participant that re-onboards
// finds its pairs intact and resolves through them from the new endpoint, which
// is the same store it had all along.
func (r *Router) RemoveEndpoint(externalID string) error {
	ep, ok := r.lookupEndpoint(externalID)
	if !ok {
		return errNotFound
	}

	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	delete(r.store.endpoints, ep.id)
	delete(r.store.byExternal, externalID)
	return nil
}

// DropNextInitialLoad arms one initial-load stream to end after a single object
// without the endpoint advancing to RECONCILING.
//
// It is the failure an endpoint cannot distinguish by watching its connection: a
// dropped set ends the response exactly as a complete one does. An endpoint that
// reads the state finds itself still at LOADING_FROM_AGRIROUTER and takes the
// set again; one that trusts the response ending reconciles against half a set.
//
// There is no agrirouter equivalent, and no control-plane route: it is a test
// affordance, not a protocol operation.
func (r *Router) DropNextInitialLoad(externalID string) error {
	ep, ok := r.lookupEndpoint(externalID)
	if !ok {
		return errNotFound
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	ep.dropLoad = true
	return nil
}

// CorruptInitialLoad makes this endpoint's initial-load stream carry a frame
// that cannot be decoded, on every attempt until it is turned off.
//
// It is the failure a retry does not fix, which is what separates it from
// [Router.DropNextInitialLoad]: taking the set again produces the same broken
// frame, so an endpoint that treats every failed take as a dropped connection
// exhausts its attempts and has nothing to say about why.
//
// There is no agrirouter equivalent, and no control-plane route: it is a test
// affordance, not a protocol operation.
func (r *Router) CorruptInitialLoad(externalID string, on bool) error {
	ep, ok := r.lookupEndpoint(externalID)
	if !ok {
		return errNotFound
	}
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	ep.corruptLoad = on
	return nil
}

// corrupted reports whether this endpoint's load stream is armed to fail. It is
// not consumed: the failure recurs until a test clears it.
func (r *Router) corrupted(ep *endpoint) bool {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	return ep.corruptLoad
}

// takeDrop consumes the armed drop, so the next attempt is served in full.
func (r *Router) takeDrop(ep *endpoint) bool {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	drop := ep.dropLoad
	ep.dropLoad = false
	return drop
}

// AddTenant is the in-process equivalent of creating a tenant.
func (r *Router) AddTenant() uuid.UUID {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	id := uuid.New()
	r.store.tenants[id] = true
	return id
}
