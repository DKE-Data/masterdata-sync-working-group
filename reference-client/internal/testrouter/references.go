package testrouter

import (
	"encoding/json"
	"fmt"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// reference is a reference to another entity as it travels on the wire.
type reference struct {
	AgrirouterID *uuid.UUID `json:"agrirouter_id,omitempty"`
	LocalID      *string    `json:"local_id,omitempty"`
}

// resolveRefs turns every reference in an incoming entity into a canonical one.
//
// On send a participant may use either identifier. A reference carrying only a
// localId is resolved against the sending participant's own mapping, and if it does not
// resolve — the target has not been sent yet — the write is rejected, which is
// what forces a target to be sent before the first reference to it. This is what
// keeps agrirouterId off the write path: a participant builds references out of
// its own keys without first correlating canonical ones.
//
// Every slot references one entity type, so the slot alone says where a
// localId is looked up. A membership additionally has to name an
// organization, which is a check on the target's content rather than its type.
func (s *store) resolveRefs(
	appID string, typ agmasync.EntityType, content map[string]json.RawMessage,
) error {
	return s.walkRefs(typ, content, func(slot refSlot, ref *reference) error {
		var target uuid.UUID
		switch {
		case ref.AgrirouterID != nil:
			target = *ref.AgrirouterID
		case ref.LocalID != nil:
			id, ok := s.local[localKey{appID, slot.target, *ref.LocalID}]
			if !ok {
				return errUnresolvedRef
			}
			target = id
		default:
			return errUnresolvedRef
		}
		obj, ok := s.objects[target]
		if !ok || obj.typ != slot.target {
			return errUnresolvedRef
		}
		if slot.partyType != "" && partyTypeOf(obj.content["details"]) != slot.partyType {
			return fmt.Errorf("%s must name a party whose details are %s", slot.attribute, slot.partyType)
		}
		ref.AgrirouterID = &target
		ref.LocalID = nil
		return nil
	})
}

// rewriteRefs prepares an outgoing entity's references for one recipient.
//
// agrirouter populates agrirouterId and replaces localId with the receiving
// participant's own identifier for the target, omitting it where there is none.
// It must not be left as the sender's: the receiver resolves through
// agrirouterId, so the sender's key carries no meaning in the receiver's
// namespace, and passing it through would disclose the sender's internal key
// for the target. This is the same rule the envelope's own localId follows.
func (s *store) rewriteRefs(
	appID string, typ agmasync.EntityType, content map[string]json.RawMessage,
) {
	_ = s.walkRefs(typ, content, func(_ refSlot, ref *reference) error {
		ref.LocalID = nil
		if ref.AgrirouterID == nil {
			return nil
		}
		if localID, ok := s.canonical[canonicalKey{appID, *ref.AgrirouterID}]; ok {
			ref.LocalID = &localID
		}
		return nil
	})
}

// namesAsMember reports whether any person's memberships name the object.
func (s *store) namesAsMember(id uuid.UUID) bool {
	for _, obj := range s.objects {
		if obj.typ != agmasync.TypeParty {
			continue
		}
		named := false
		_ = s.walkRefs(obj.typ, cloneContent(obj.content), func(_ refSlot, ref *reference) error {
			if ref.AgrirouterID != nil && *ref.AgrirouterID == id {
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

// walkSlot visits the references in one slot of a container.
func walkSlot(
	slot refSlot, container map[string]json.RawMessage, visit func(refSlot, *reference) error,
) error {
	raw, ok := container[slot.key]
	if !ok || len(raw) == 0 || isNull(raw) {
		return nil
	}
	one := func(r *reference) error { return visit(slot, r) }

	if !slot.each {
		updated, err := visitRef(raw, one)
		if err != nil {
			return err
		}
		container[slot.key] = updated
		return nil
	}

	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	for i, item := range items {
		if slot.attribute == "" {
			updated, err := visitRef(item, one)
			if err != nil {
				return err
			}
			items[i] = updated
			continue
		}

		var attributes map[string]json.RawMessage
		if err := json.Unmarshal(item, &attributes); err != nil {
			continue
		}
		inner, ok := attributes[slot.attribute]
		if !ok {
			continue
		}
		updated, err := visitRef(inner, one)
		if err != nil {
			return err
		}
		attributes[slot.attribute] = updated
		if remade, err := json.Marshal(attributes); err == nil {
			items[i] = remade
		}
	}
	if remade, err := json.Marshal(items); err == nil {
		container[slot.key] = remade
	}
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
	// reference. The generated types make every required reference a value
	// rather than a pointer, so an unset one still marshals, and rejecting
	// those would reject every entity that leaves an optional slot alone.
	// Whether a required reference was supplied is schema validation, which
	// this router does not attempt.
	if ref.AgrirouterID == nil && ref.LocalID == nil {
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
