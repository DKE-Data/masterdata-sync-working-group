package testrouter

import (
	"encoding/json"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/google/uuid"
)

// reference is a reference to another entity as it travels on the wire.
type reference struct {
	AgrirouterID *uuid.UUID `json:"agrirouter_id,omitempty"`
	LocalID      *string    `json:"local_id,omitempty"`
	Type         *string    `json:"type,omitempty"`
}

// resolveRefs turns every reference in an incoming entity into a canonical one.
//
// On send a participant may use either identifier. A reference carrying only a
// localId is resolved against the sending participant's own mapping, and if it does not
// resolve — the target has not been sent yet — the write is rejected, which is
// what forces a target to be sent before the first reference to it. This is what
// keeps agrirouterId off the write path: a participant builds references out of
// its own keys without first correlating canonical ones.
func (s *store) resolveRefs(
	appID string, typ agmasync.EntityType, content map[string]json.RawMessage,
) error {
	return s.walkRefs(typ, content, func(ref *reference) error {
		if ref.AgrirouterID != nil {
			if _, ok := s.objects[*ref.AgrirouterID]; !ok {
				return errUnresolvedRef
			}
			ref.LocalID = nil
			return nil
		}
		if ref.LocalID == nil {
			return errUnresolvedRef
		}

		target, err := s.resolveLocal(appID, typ, ref)
		if err != nil {
			return err
		}
		ref.AgrirouterID = &target
		ref.LocalID = nil
		return nil
	})
}

// resolveLocal finds the canonical object the sending participant's own
// identifier names. The lookup is scoped to the application rather than to the
// acting endpoint: the participant has one store, so a reference it builds out
// of its own keys resolves the same whichever of its endpoints sends it.
//
// A reference to a party carries a type discriminator, because the slot admits
// both organizations and persons and a receiver that has to lazy-load the
// target needs to know which collection to ask. Slots whose type is fixed — a
// field's farm — carry none, so the type is implied by the slot.
func (s *store) resolveLocal(
	appID string, owner agmasync.EntityType, ref *reference,
) (uuid.UUID, error) {
	candidates := []agmasync.EntityType{}
	switch {
	case ref.Type != nil:
		candidates = append(candidates, agmasync.EntityType(*ref.Type))
	case owner == agmasync.TypeField:
		// The only untyped slots on a field are its farm and its boundaries.
		candidates = append(candidates, agmasync.TypeFarm, agmasync.TypeFieldBoundary)
	default:
		candidates = append(candidates, agmasync.EntityTypes...)
	}

	for _, t := range candidates {
		if id, ok := s.local[localKey{appID, t, *ref.LocalID}]; ok {
			return id, nil
		}
	}
	return uuid.Nil, errUnresolvedRef
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
	_ = s.walkRefs(typ, content, func(ref *reference) error {
		ref.LocalID = nil
		if ref.AgrirouterID == nil {
			return nil
		}
		if target, ok := s.objects[*ref.AgrirouterID]; ok && ref.Type == nil {
			if isParty(target.typ) {
				t := string(target.typ)
				ref.Type = &t
			}
		}
		if localID, ok := s.canonical[canonicalKey{appID, *ref.AgrirouterID}]; ok {
			ref.LocalID = &localID
		}
		return nil
	})
}

func isParty(t agmasync.EntityType) bool {
	return t == agmasync.TypeOrganization || t == agmasync.TypePerson
}

// walkRefs visits every reference slot of an entity, rewriting each in place.
func (s *store) walkRefs(
	typ agmasync.EntityType, content map[string]json.RawMessage, visit func(*reference) error,
) error {
	for _, slot := range refSlots[typ] {
		raw, ok := content[slot.key]
		if !ok || len(raw) == 0 || string(raw) == "null" {
			continue
		}

		if !slot.each {
			updated, err := visitRef(raw, visit)
			if err != nil {
				return err
			}
			content[slot.key] = updated
			continue
		}

		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			continue
		}
		for i, item := range items {
			if slot.attribute == "" {
				updated, err := visitRef(item, visit)
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
			updated, err := visitRef(inner, visit)
			if err != nil {
				return err
			}
			attributes[slot.attribute] = updated
			if remade, err := json.Marshal(attributes); err == nil {
				items[i] = remade
			}
		}
		if remade, err := json.Marshal(items); err == nil {
			content[slot.key] = remade
		}
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
