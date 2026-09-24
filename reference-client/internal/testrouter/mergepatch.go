package testrouter

import (
	"bytes"
	"encoding/json"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
)

// A write is a JSON Merge Patch (RFC 7396) of an entity's attributes: a value
// replaces, null removes, and an attribute left out is left alone. The rule
// recurses into plain nested objects, so a participant that models a city but
// not a street changes one without erasing the other.
//
// Three kinds of value are replaced whole instead, because a part of one means
// nothing on its own: arrays (which RFC 7396 already replaces), references,
// and geometries. See "Writing an entity" in specification.md.

// requiredAttributes are the attributes openapi.yaml requires on each entity
// type, less the `type` discriminator, which is envelope. They are required on
// every write, so null — removing one — is never a valid value for them.
var requiredAttributes = map[agmasync.EntityType][]string{
	agmasync.TypeOrganization:  {"name"},
	agmasync.TypePerson:        {"last_name"},
	agmasync.TypeFarm:          {"owner", "name"},
	agmasync.TypeField:         {"name"},
	agmasync.TypeFieldBoundary: {"boundary"},
}

// geometryAttributes are the attributes holding a GeoJSON geometry.
var geometryAttributes = map[agmasync.EntityType][]string{
	agmasync.TypeFarm:          {"geo_reference"},
	agmasync.TypeFieldBoundary: {"boundary"},
}

// wholeAttributes reports the attributes of an entity type a patch replaces
// rather than merges into: its reference slots and its geometries. Arrays need
// no listing, being replaced by the merge rule itself.
func wholeAttributes(typ agmasync.EntityType) map[string]bool {
	out := map[string]bool{}
	for _, slot := range refSlots[typ] {
		out[slot.key] = true
	}
	for _, key := range geometryAttributes[typ] {
		out[key] = true
	}
	return out
}

// isNull reports whether a sent value is the instruction to remove.
func isNull(v json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(v), []byte("null"))
}

// patchAttribute applies one attribute of a patch to the value an object holds
// for it, nil standing for absent on both sides of the call.
func patchAttribute(current, patch json.RawMessage, whole bool) json.RawMessage {
	if isNull(patch) {
		return nil
	}
	if whole {
		return canonicalJSON(patch)
	}
	return canonicalJSON(mergePatch(current, patch))
}

// mergePatch is RFC 7396's MergePatch(target, patch).
func mergePatch(target, patch json.RawMessage) json.RawMessage {
	var patchFields map[string]json.RawMessage
	if json.Unmarshal(patch, &patchFields) != nil || patchFields == nil {
		return patch // not an object: replaces the target outright
	}

	var targetFields map[string]json.RawMessage
	if json.Unmarshal(target, &targetFields) != nil || targetFields == nil {
		targetFields = map[string]json.RawMessage{}
	}
	for key, value := range patchFields {
		if isNull(value) {
			delete(targetFields, key)
			continue
		}
		targetFields[key] = mergePatch(targetFields[key], value)
	}

	merged, err := json.Marshal(targetFields)
	if err != nil {
		return patch
	}
	return merged
}

// canonicalJSON re-encodes a value with sorted keys and no insignificant
// whitespace, so that stored values compare by meaning rather than by how a
// participant happened to spell them. Numbers keep their literal.
func canonicalJSON(v json.RawMessage) json.RawMessage {
	if v == nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(v))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return v
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		return v
	}
	return out
}
