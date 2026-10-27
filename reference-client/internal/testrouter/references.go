package testrouter

import (
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// reference is a reference to another entity as it travels on the wire: an
// `Id` naming the target. See "References" in specification.md.
type reference struct {
	ID wireID `json:"id"`
}

// resolveRefs turns every reference in an incoming entity into a canonical one.
//
// On send a participant may use either identifier. A reference without an
// agrirouter entry is resolved by its referenceId against the sending
// endpoint's own mapping, and if it does not resolve — the target has not
// been sent yet — the write is rejected, which is what forces a target to be
// sent before the first reference to it. This is what keeps the agrirouter
// identifier off the write path: a participant builds references out of its
// own keys without first correlating canonical ones.
//
// Every slot references one entity type, so the slot alone says where a
// referenceId is looked up. A parentOrganizationRef additionally has to name an
// organization, which is a check on the target's content rather than its type.
func (s *store) resolveRefs(
	ep *endpoint, typ agmasync.EntityType, content map[string]json.RawMessage,
) error {
	return s.walkRefs(typ, content, func(slot refSlot, ref *reference) error {
		agrirouterID, _, err := ref.ID.read()
		if err != nil {
			return err
		}
		var target uuid.UUID
		switch {
		case agrirouterID != nil:
			target = *agrirouterID
		case ref.ID.ReferenceID != nil:
			id, ok := s.local[localKeyOf(ep, slot.target, *ref.ID.ReferenceID)]
			if !ok {
				return errUnresolvedRef
			}
			target = id
		default:
			return errUnresolvedRef
		}
		// A reference stays inside the tenant, whichever identifier names the
		// target: a referenceId resolves there by construction, an agrirouter
		// entry has to be checked.
		obj, ok := s.objects[target]
		if !ok || obj.typ != slot.target || obj.tenantID != ep.tenantID {
			return errUnresolvedRef
		}
		if slot.partyType != "" && partyTypeOf(obj.content[detailsKey]) != slot.partyType {
			return fmt.Errorf("%s must name a party whose %s are %s", slot.key, detailsKey, slot.partyType)
		}
		ref.ID = canonicalID(target)
		return nil
	})
}

// rewriteRefs prepares an outgoing entity's references for one recipient.
//
// agrirouter keeps the agrirouter entry and adds the receiving endpoint's own
// identifier for the target, omitting it where there is none. It must not be
// the sender's, even a sibling endpoint's: the receiver resolves through the
// agrirouter entry, so the sender's key carries no meaning in the receiver's
// namespace, and passing it through would disclose the sender's internal key
// for the target. This is the same rule the object's own `id` follows.
func (s *store) rewriteRefs(
	endpointID uuid.UUID, typ agmasync.EntityType, content map[string]json.RawMessage,
) {
	_ = s.walkRefs(typ, content, func(_ refSlot, ref *reference) error {
		target, _, err := ref.ID.read()
		if err != nil || target == nil {
			return nil
		}
		ref.ID = s.renderID(endpointID, *target)
		return nil
	})
}

// namedAsParent reports whether any person's parentOrganizationRef names the object.
func (s *store) namedAsParent(id uuid.UUID) bool {
	for _, obj := range s.objects {
		if obj.typ != agmasync.TypeParty {
			continue
		}
		named := false
		_ = s.walkRefs(obj.typ, cloneContent(obj.content), func(_ refSlot, ref *reference) error {
			if target, _, err := ref.ID.read(); err == nil && target != nil && *target == id {
				named = true
			}
			return nil
		})
		if named {
			return true
		}
	}
	return false
}

func cloneContent(content map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(content))
	for k, v := range content {
		out[k] = v
	}
	return out
}

// walkRefs visits every reference slot of an entity, rewriting each in place.
func (s *store) walkRefs(
	typ agmasync.EntityType, content map[string]json.RawMessage,
	visit func(refSlot, *reference) error,
) error {
	for _, slot := range refSlots[typ] {
		container := content
		if slot.in != "" {
			raw, ok := content[slot.in]
			if !ok || isNull(raw) {
				continue
			}
			container = nil
			if err := json.Unmarshal(raw, &container); err != nil || container == nil {
				continue
			}
		}
		if err := walkSlot(slot, container, visit); err != nil {
			return err
		}
		if slot.in != "" {
			if remade, err := json.Marshal(container); err == nil {
				content[slot.in] = remade
			}
		}
	}
	return nil
}

// walkSlot visits the reference in one slot of a container.
func walkSlot(
	slot refSlot, container map[string]json.RawMessage, visit func(refSlot, *reference) error,
) error {
	raw, ok := container[slot.key]
	if !ok || len(raw) == 0 || isNull(raw) {
		return nil
	}
	updated, err := visitRef(raw, func(r *reference) error { return visit(slot, r) })
	if err != nil {
		return err
	}
	container[slot.key] = updated
	return nil
}

func visitRef(raw json.RawMessage, visit func(*reference) error) (json.RawMessage, error) {
	var ref reference
	if err := json.Unmarshal(raw, &ref); err != nil {
		// Not shaped like a reference; leave it alone rather than reject the
		// whole entity over a slot this version does not understand.
		return raw, nil
	}
	// A slot that is present but names nothing is an empty slot, not a dangling
	// reference. The generated types make a required reference a value rather
	// than a pointer, so an unset one still marshals as `{"id":{}}`. Whether a
	// required reference was supplied is schema validation, which this router
	// does not attempt.
	if ref.ID.ReferenceID == nil && len(ref.ID.UniqueIDs) == 0 {
		return raw, nil
	}
	if err := visit(&ref); err != nil {
		return raw, err
	}
	remade, err := json.Marshal(ref)
	if err != nil {
		return raw, nil
	}
	return remade, nil
}
