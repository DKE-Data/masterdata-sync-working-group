package testrouter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// Failures the store reports. The handlers turn these into the status codes the
// specification names.
var (
	errNotFound          = errors.New("not found")
	errForbidden         = errors.New("forbidden")
	errUnresolvedRef     = errors.New("reference does not resolve")
	errBaseRequired      = errors.New("base revision required")
	errRevisionConflict  = errors.New("revision conflict")
	errLocalIDBound      = errors.New("local id already bound")
	errAgrirouterIDBound = errors.New("agrirouter id already bound")
)

// revisionConflict carries the revision that stands, which a rejected write has
// to be told so it can rebase rather than guess at base + 1.
type revisionConflict struct{ current int }

func (e *revisionConflict) Error() string {
	return fmt.Sprintf("revision conflict, current revision is %d", e.current)
}
func (e *revisionConflict) Is(target error) bool { return target == errRevisionConflict }

// mappingConflict names which identifier is taken and the mapping that holds
// it. Both ends belong to the rejected participant, so naming it discloses
// nothing it does not already hold.
type mappingConflict struct {
	reason   string
	existing *binding
}

func (e *mappingConflict) Error() string { return e.reason }
func (e *mappingConflict) Is(target error) bool {
	switch e.reason {
	case agmasync.ReasonLocalIDAlreadyBound:
		return target == errLocalIDBound
	case agmasync.ReasonAgrirouterIDAlreadyBound:
		return target == errAgrirouterIDBound
	}
	return false
}

// store is the SSOT: canonical objects, the identifier mapping, and the
// endpoints entitled to them.
type store struct {
	mu sync.Mutex

	objects map[uuid.UUID]*object

	// local maps a participant's own identifier to a canonical object. The key
	// is (application, entity type, localId): a participant keeps one store
	// behind however many endpoints it operates, so the same string from two of
	// its endpoints names the same record. Which endpoint acted still decides
	// entitlement and sourceEndpointId; it does not partition this.
	//
	// Tenant does not partition it either, so a participant operating in several
	// tenants must keep its local identifiers unique across all of them, not per
	// tenant. Reusing one string in a second tenant is rejected: the identifier
	// is already taken here, and the object it names is untouchable from there.
	local map[localKey]uuid.UUID

	// canonical is the same mapping read the other way, which delivery needs:
	// what does this participant call this object?
	canonical map[canonicalKey]string

	endpoints  map[uuid.UUID]*endpoint
	byExternal map[string]*endpoint
	tenants    map[uuid.UUID]bool

	seq uint64

	hub      *hub
	observer *observer
	now      func() time.Time
}

type localKey struct {
	appID   string
	typ     agmasync.EntityType
	localID string
}

type canonicalKey struct {
	appID string
	objID uuid.UUID
}

func newStore(hub *hub, obs *observer) *store {
	return &store{
		objects:    map[uuid.UUID]*object{},
		local:      map[localKey]uuid.UUID{},
		canonical:  map[canonicalKey]string{},
		endpoints:  map[uuid.UUID]*endpoint{},
		byExternal: map[string]*endpoint{},
		tenants:    map[uuid.UUID]bool{},
		hub:        hub,
		observer:   obs,
		now:        time.Now,
	}
}

// put creates or updates a canonical object. It implements "Concurrency
// control" and the loop prevention rules that go with it.
func (s *store) put(
	ep *endpoint, typ agmasync.EntityType, localID string, body []byte, base *int,
) (*object, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !ep.optedInto(typ) {
		return nil, false, errForbidden
	}

	incoming, err := contentOf(body)
	if err != nil {
		return nil, false, err
	}
	if err := s.resolveRefs(ep.appID, typ, incoming); err != nil {
		return nil, false, err
	}
	active := activeOf(body)

	objID, known := s.local[localKey{ep.appID, typ, localID}]
	if !known {
		// A base on a write that resolves to nothing is a `412`: the
		// participant believes it is updating an object agrirouter does not
		// know under that localId — after an unbind, for example — and
		// creating one silently would be the duplicate binding exists to
		// prevent.
		if base != nil {
			return nil, false, &revisionConflict{current: 0}
		}
		obj := s.create(ep, typ, localID, incoming, active)
		s.deliver(obj, ep.id, false)
		return obj, true, nil
	}

	obj := s.objects[objID]

	// A payload equal to the current canonical value succeeds as a no-op
	// whatever the base: no new revision, nothing forwarded. This is what makes
	// a write whose outcome was never observed safe to retry.
	if sameContent(obj.content, incoming) && obj.active == active {
		return obj, false, nil
	}
	if base == nil {
		return nil, false, errBaseRequired
	}
	if *base > obj.revision || *base < 1 {
		return nil, false, &revisionConflict{current: obj.revision}
	}

	// A base behind the current revision is not necessarily a failure. The
	// merge compares two sets of changes against the base: the participant's,
	// and everyone else's since. Only an attribute both of them touched, and
	// touched differently, is a conflict.
	changed := map[string]json.RawMessage{}
	for k := range union(incoming, obj.content) {
		sent, sentPresent := incoming[k]
		if !sentPresent {
			sent = nil
		}
		current, currentPresent := obj.content[k]
		if !currentPresent {
			current = nil
		}
		atBase := obj.valueAt(k, *base)

		participantChanged := !bytes.Equal(sent, atBase)
		othersChanged := !bytes.Equal(current, atBase)

		if !participantChanged {
			continue
		}
		// Both changed it, and not to the same thing. Nothing here can decide
		// between them, so the write is rejected and the participant rebases.
		if othersChanged && !bytes.Equal(sent, current) {
			return nil, false, &revisionConflict{current: obj.revision}
		}
		if bytes.Equal(sent, current) {
			continue
		}
		changed[k] = sent
	}

	if len(changed) == 0 {
		return obj, false, nil
	}

	obj.revision++
	for k, v := range changed {
		if v == nil {
			delete(obj.content, k)
		} else {
			obj.content[k] = v
		}
		obj.stamps[k] = obj.revision
		obj.history[k] = append(obj.history[k], attributeValue{revision: obj.revision, value: v})
	}
	obj.active = active
	obj.modifiedAt = s.now().UTC()
	obj.sourceEndpointID = ep.id
	s.seq++
	obj.seq = s.seq

	s.deliver(obj, ep.id, false)
	return obj, false, nil
}

func (s *store) create(
	ep *endpoint, typ agmasync.EntityType, localID string,
	content map[string]json.RawMessage, active bool,
) *object {
	s.seq++
	obj := &object{
		id:               uuid.New(),
		typ:              typ,
		tenantID:         ep.tenantID,
		revision:         1,
		active:           active,
		modifiedAt:       s.now().UTC(),
		sourceEndpointID: ep.id,
		seq:              s.seq,
		content:          content,
		stamps:           map[string]int{},
		history:          map[string][]attributeValue{},
	}
	for k, v := range content {
		obj.stamps[k] = 1
		obj.history[k] = []attributeValue{{revision: 1, value: v}}
	}
	s.objects[obj.id] = obj
	s.local[localKey{ep.appID, typ, localID}] = obj.id
	s.canonical[canonicalKey{ep.appID, obj.id}] = localID
	return obj
}

// deactivate signals that an entity was deactivated in its source system.
//
// The first deactivation is a write like any other and honours the base. On an
// object that is already inactive the base is ignored and no revision is
// produced, which is what the required idempotency amounts to.
func (s *store) deactivate(
	ep *endpoint, typ agmasync.EntityType, localID string, base *int,
) (*object, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !ep.optedInto(typ) {
		return nil, errForbidden
	}
	objID, known := s.local[localKey{ep.appID, typ, localID}]
	if !known {
		return nil, errNotFound
	}
	obj := s.objects[objID]

	if !obj.active {
		return obj, nil
	}
	if base == nil {
		return nil, errBaseRequired
	}
	if *base != obj.revision {
		return nil, &revisionConflict{current: obj.revision}
	}

	obj.revision++
	obj.active = false
	obj.modifiedAt = s.now().UTC()
	obj.sourceEndpointID = ep.id
	s.seq++
	obj.seq = s.seq

	s.deliver(obj, ep.id, true)
	return obj, nil
}

// bind records that a canonical object is one this participant already holds.
//
// It creates no revision, does not change sourceEndpointId, and is delivered to
// nobody. A local identifier denotes exactly one canonical object, so binding a
// second is a conflict, and so is claiming an object the participant already
// knows under another identifier.
func (s *store) bind(ep *endpoint, typ agmasync.EntityType, localID string, objID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindLocked(ep, typ, localID, objID)
}

func (s *store) bindLocked(
	ep *endpoint, typ agmasync.EntityType, localID string, objID uuid.UUID,
) error {
	obj, ok := s.objects[objID]
	if !ok || obj.typ != typ || obj.tenantID != ep.tenantID {
		return errNotFound
	}
	if !ep.optedInto(typ) {
		return errForbidden
	}

	if existing, ok := s.local[localKey{ep.appID, typ, localID}]; ok {
		if existing == objID {
			return nil // idempotent
		}
		return &mappingConflict{
			reason:   agmasync.ReasonLocalIDAlreadyBound,
			existing: &binding{LocalID: localID, AgrirouterID: existing},
		}
	}
	if existing, ok := s.canonical[canonicalKey{ep.appID, objID}]; ok {
		return &mappingConflict{
			reason:   agmasync.ReasonAgrirouterIDAlreadyBound,
			existing: &binding{LocalID: existing, AgrirouterID: objID},
		}
	}

	s.local[localKey{ep.appID, typ, localID}] = objID
	s.canonical[canonicalKey{ep.appID, objID}] = localID
	return nil
}

// unbind declares that the participant no longer holds an object.
//
// It removes no canonical object, touches no other participant's mapping,
// creates no revision, and reaches nobody. It answers the same whether or not a
// mapping existed, so a retry after a lost response is safe.
func (s *store) unbind(
	ep *endpoint, typ agmasync.EntityType, localID string, objID uuid.UUID,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	obj, ok := s.objects[objID]
	if !ok || obj.typ != typ {
		return errNotFound
	}
	if current, ok := s.local[localKey{ep.appID, typ, localID}]; ok && current == objID {
		delete(s.local, localKey{ep.appID, typ, localID})
		delete(s.canonical, canonicalKey{ep.appID, objID})
	}
	return nil
}

// entitled reports whether an endpoint may receive an object: same tenant, and
// opted into its entity type. Opt-in is the only filter on delivery — nothing
// inside a synchronized object narrows it.
func (s *store) entitled(ep *endpoint, obj *object) bool {
	return ep.tenantID == obj.tenantID && ep.optedInto(obj.typ)
}

// recipients returns one endpoint per application the object is delivered to,
// in a stable order, optionally excluding the endpoint a change came from.
//
// Entitlement and origin suppression are decided per endpoint — tenant, opt-in,
// and who wrote the change — but delivery is not. A frame names no endpoint, it
// is rendered in the application's namespace, and it goes to a subscription the
// application holds once. Two entitled siblings would therefore produce two
// identical frames on one connection, so the fan-out collapses them: the
// application is a recipient if any of its endpoints is, and the surviving
// endpoint stands for it. Which sibling survives is not observable.
func (s *store) recipients(obj *object, exclude uuid.UUID) []*endpoint {
	var eps []*endpoint
	for _, ep := range s.endpoints {
		if ep.id == exclude || !s.entitled(ep, obj) {
			continue
		}
		eps = append(eps, ep)
	}
	sort.Slice(eps, func(i, j int) bool {
		return eps[i].id.String() < eps[j].id.String()
	})

	seen := map[string]bool{}
	deduped := eps[:0]
	for _, ep := range eps {
		if seen[ep.appID] {
			continue
		}
		seen[ep.appID] = true
		deduped = append(deduped, ep)
	}
	return deduped
}

// deliver fans a change out to every entitled application, suppressing the
// endpoint it originated from. An application holding two entitled endpoints
// receives the object once: the two frames would be identical.
func (s *store) deliver(obj *object, source uuid.UUID, deactivated bool) {
	event := eventMasterdataChanged
	if deactivated {
		event = eventMasterdataDeactivated
	}
	for _, ep := range s.recipients(obj, source) {
		s.hub.publish(ep.appID, frame{
			event:  event,
			id:     encodePosition(obj.seq),
			entity: s.renderLocked(obj, ep),
		})
	}
}

// deliverTo hands one object to one endpoint regardless of origin.
//
// A requested object is delivered even when the requester was its last writer:
// suppression exists to avoid handing an endpoint a revision it already holds,
// and a request states the opposite.
func (s *store) deliverTo(ep *endpoint, obj *object) {
	s.hub.publish(ep.appID, frame{
		event:  eventMasterdataChanged,
		id:     encodePosition(obj.seq),
		entity: s.renderLocked(obj, ep),
	})
}

// render produces the object as one participant sees it. Every localId in the
// result — the envelope's own and one per reference — is resolved in the
// participant's namespace, which is keyed by application: two endpoints of one
// application render identically, and the frame names no endpoint at all.
func (s *store) renderLocked(obj *object, ep *endpoint) json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range obj.content {
		out[k] = v
	}
	s.rewriteRefs(ep.appID, obj.typ, out)

	put := func(k string, v any) {
		raw, err := json.Marshal(v)
		if err == nil {
			out[k] = raw
		}
	}
	put("type", string(obj.typ))
	put("agrirouterId", obj.id)
	put("active", obj.active)
	put("revision", obj.revision)
	put("modifiedAt", obj.modifiedAt)
	put("tenantId", obj.tenantID)
	put("sourceEndpointId", obj.sourceEndpointID)

	// An absent localId is meaningful: it states that agrirouter does not
	// believe this participant holds the object, which is what makes an unbound
	// object recognisable as one to create locally and bind.
	if localID, ok := s.canonical[canonicalKey{ep.appID, obj.id}]; ok {
		put("localId", localID)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func contentOf(body []byte) (map[string]json.RawMessage, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("malformed entity: %w", err)
	}
	content := map[string]json.RawMessage{}
	for k, v := range all {
		if envelopeAttributes[k] {
			continue
		}
		content[k] = v
	}
	return content, nil
}

func activeOf(body []byte) bool {
	var probe struct {
		Active *bool `json:"active"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Active == nil {
		return true
	}
	return *probe.Active
}

func sameContent(a, b map[string]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok || !bytes.Equal(av, bv) {
			return false
		}
	}
	return true
}

// valueAt returns an attribute's value as of a revision, which is what a
// three-way merge diffs both sides against. A nil result means the attribute
// was absent then.
func (o *object) valueAt(attribute string, revision int) json.RawMessage {
	var at json.RawMessage
	for _, entry := range o.history[attribute] {
		if entry.revision > revision {
			break
		}
		at = entry.value
	}
	return at
}

func union(a, b map[string]json.RawMessage) map[string]bool {
	keys := make(map[string]bool, len(a)+len(b))
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	return keys
}
