package testrouter

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/testrouter/oapi"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// ResetTenant wipes one tenant's master data, as a user does in agrirouter.
//
// Every canonical object of the tenant goes, and every participant's pairs for
// them with it; every endpoint in the tenant is left opted into nothing, with no
// initial-load state and no marker of a previous COMPLETED. It is the only hard
// removal the router makes. Declarations stay: they say what the participant's
// software can do, which a reset does not change.
//
// Each application with a stake in the tenant is told once, on its stream,
// and the frame is published under the store lock so that nothing later in the
// tenant can overtake it. No ROUTE_CHANGED goes with it: the reset stands for an
// empty selection on every endpoint it lists.
//
// There is no participant-facing operation for this. It is the user's.
func (r *Router) ResetTenant(tenantID uuid.UUID) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	if !r.store.tenants[tenantID] {
		return errNotFound
	}

	// Decided before the wipe, which takes the bindings it is partly decided
	// from, and kept with the reset for catch-up to read.
	apps := r.store.resetRecipientsLocked(tenantID)

	for id, obj := range r.store.objects {
		if obj.tenantID == tenantID {
			delete(r.store.objects, id)
		}
	}
	for key, objID := range r.store.local {
		if _, ok := r.store.objects[objID]; !ok {
			delete(r.store.local, key)
		}
	}
	for key := range r.store.canonical {
		if _, ok := r.store.objects[key.objID]; !ok {
			delete(r.store.canonical, key)
		}
	}
	for _, ep := range r.store.endpoints {
		if ep.tenantID != tenantID {
			continue
		}
		ep.toggles = map[agmasync.EntityType]bool{}
		ep.load = nil
		ep.previousLoadCompletedAt = nil
	}

	r.store.seq++
	r.store.resets[tenantID] = tenantReset{seq: r.store.seq, apps: apps}
	r.observer.record("masterdataReset", map[string]any{"tenantId": tenantID})

	for _, appID := range apps {
		r.hub.publish(appID, r.store.resetFrameLocked(tenantID, appID, encodePosition(r.store.seq)))
	}
	return nil
}

// tenantReset is what is retained of a tenant's latest reset.
type tenantReset struct {
	seq uint64

	// apps are the applications it was announced to, fixed when it happened:
	// the bindings that decided some of them are gone afterwards.
	apps []string
}

// resetRecipientsLocked names the applications a reset of this tenant is
// announced to: those holding a binding to one of its objects, and those with
// an endpoint there that has taken part at some point.
//
// The first has bindings to discard, and includes an application whose
// endpoints in the tenant have all been removed, the mapping having outlived
// them. The second has a route to lose, and includes one that never bound
// anything. An application that is neither has nothing the reset changes.
func (s *store) resetRecipientsLocked(tenantID uuid.UUID) []string {
	var apps []string
	add := func(appID string) {
		if !slices.Contains(apps, appID) {
			apps = append(apps, appID)
		}
	}
	for key := range s.canonical {
		if obj, ok := s.objects[key.objID]; ok && obj.tenantID == tenantID {
			add(key.appID)
		}
	}
	for _, ep := range s.sortedEndpoints() {
		if ep.tenantID == tenantID && ep.selectionChangedSeq != 0 {
			add(ep.appID)
		}
	}
	slices.Sort(apps)
	return apps
}

// resetFrameLocked renders a RESET_MASTERDATA_SYNC frame for one application,
// listing its endpoints in the tenant by both identifiers — none, where it is
// told only for the bindings it held.
func (s *store) resetFrameLocked(tenantID uuid.UUID, appID, id string) frame {
	endpoints := []map[string]any{}
	for _, ep := range s.sortedEndpoints() {
		if ep.tenantID == tenantID && ep.appID == appID {
			endpoints = append(endpoints, map[string]any{
				"endpoint_id": ep.id,
				"external_id": ep.externalID,
			})
		}
	}
	data, _ := json.Marshal(map[string]any{
		"event_type": eventMasterdataReset,
		"tenant_id":  tenantID,
		"endpoints":  endpoints,
	})
	return frame{event: eventMasterdataReset, id: id, entity: data}
}

// resetTenant is the control plane's face of [Router.ResetTenant].
func (r *Router) resetTenant(ctx echo.Context) error {
	id, err := uuid.Parse(ctx.Param("tenantId"))
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, oapi.Error{Message: err.Error()})
	}
	if err := r.ResetTenant(id); err != nil {
		return ctx.JSON(http.StatusNotFound, oapi.Error{Message: "no such tenant"})
	}
	return ctx.NoContent(http.StatusNoContent)
}
