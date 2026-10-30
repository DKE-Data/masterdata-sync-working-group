package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/DKE-Data/masterdata-sync-working-group/agmasync"
	"github.com/DKE-Data/masterdata-sync-working-group/agmasync/oapi"
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

// The create form is this platform's own screen, not the protocol's.
//
// A farm management system asks its user for a name and a grower, not for a
// canonical entity, and the mapping from what was typed to what goes on the
// wire is exactly the work a real integration has to do. Doing it here keeps
// that work visible: the fields below are the platform's, and everything that
// turns them into an entity is one function at the bottom of this file.
//
// Only the modelled attributes are offered. What this platform has no column
// for it cannot ask a person for either, and does not keep when it arrives
// from elsewhere: its writes leave such attributes out, which keeps them.

// formField is one input on the create form.
type formField struct {
	// Name is the input's name and the content attribute it fills. A dotted
	// name is a path into nested objects, so "boundary.geometry" fills
	// {"boundary": {"geometry": …}}. A segment `list[CODE]` is the entry of
	// an address list whose addressContactTypeCode is CODE.
	Name  string
	Label string

	// Kind picks the control and how the value is parsed: text, area, enum,
	// ref or geometry.
	Kind string

	Placeholder string
	Hint        string

	// Examples are an extensible enumeration's documented values. They are
	// offered rather than enforced — that is what extensible means, and a
	// select would teach the opposite.
	Examples []string

	// RefType is the entity type a reference points at.
	RefType agmasync.EntityType

	// Choices are the records currently available to point at. Filled per
	// request by [fillChoices]; a reference can only name something the
	// platform already holds, because that is what the wire carries — this
	// platform's own identifier for the target.
	Choices []refChoice

	Required bool
}

// refChoice is one record a reference may name, by this platform's own
// identifier for it.
type refChoice struct {
	Value string
	Label string
}

// formFields is what the platform asks for, per record type.
//
// It mirrors the columns in internal/platform/store: a field here with no
// column there would be typed in and silently dropped. An organization's and a
// person's own attributes sit in the party's _agmasync_details; see
// [attributesFromForm].
func formFields(typ recordType) []formField {
	switch typ {
	case recordOrganization:
		return []formField{
			{Name: "name", Label: "Name", Kind: "text",
				Placeholder: "Hof Nord GmbH", Required: true},
			{Name: "_agmasync_details.commercialRegistryNumber", Label: "Commercial registry number",
				Kind: "text", Placeholder: "HRA 12345"},
			{Name: physicalAddress + ".city", Label: "City", Kind: "text", Placeholder: "Osnabrück"},
			{Name: physicalAddress + ".countryCode", Label: "Country code", Kind: "text", Placeholder: "DE"},
		}
	case recordPerson:
		return []formField{
			{Name: "name", Label: "Name", Kind: "text",
				Placeholder: "Anke Meyer", Required: true},
			{Name: "_agmasync_details.title", Label: "Title", Kind: "text", Placeholder: "Dr."},
			{Name: "_agmasync_details.firstName", Label: "First name", Kind: "text", Placeholder: "Anke"},
			{Name: "_agmasync_details.lastName", Label: "Last name", Kind: "text", Placeholder: "Meyer"},
		}
	case recordType(agmasync.TypeFarm):
		return []formField{
			{Name: "name", Label: "Name", Kind: "text",
				Placeholder: "Hof Nord", Required: true},
			// Optional, as in the protocol: a system that records no parties
			// has no grower to give (ADR 13). Where there is one, the party
			// goes before the farm it holds.
			{Name: "_agmasync_growerRef", Label: "Grower", Kind: "ref",
				RefType: agmasync.TypeParty,
				Hint:    "a party held here"},
			{Name: "_agmasync_address.city", Label: "City", Kind: "text", Placeholder: "Osnabrück"},
		}
	case recordType(agmasync.TypeField):
		return []formField{
			{Name: "name", Label: "Name", Kind: "text",
				Placeholder: "Nordacker", Required: true},
			{Name: "arableArea", Label: "Area (ha)", Kind: "area", Placeholder: "12.4"},
			{Name: "_agmasync_farmRef", Label: "Farm", Kind: "ref",
				RefType: agmasync.TypeFarm,
				Hint:    "the farm it belongs to"},
		}
	case recordType(agmasync.TypeFieldBoundary):
		return []formField{
			// Required, as in the protocol: the boundary names its field, so
			// the field goes first.
			{Name: "_agmasync_fieldRef", Label: "Field", Kind: "ref",
				RefType:  agmasync.TypeField,
				Hint:     "the field it describes",
				Required: true},
			{Name: "name", Label: "Name", Kind: "text", Placeholder: "Nordacker 2026"},
			{Name: "_agmasync_boundaryType", Label: "Boundary type", Kind: "enum",
				Examples: []string{"CONCEPTUAL", "OPERATIONAL",
					"ECONOMIC_DEFINED", "ADMINISTRATIVE_RECEIVED"},
				Hint: "extensible: another value is allowed"},
			{Name: "boundary.boundaryCreationMethodCode", Label: "Creation method", Kind: "enum",
				Examples: []string{"UNKNOWN", "MANUAL", "DRIVEN", "SURVEYED",
					"AUTO_OPERATION", "AUTO_IMAGERY", "ADMINISTRATIVE"}},
			// The one field left as text, because a polygon is not something a
			// person types into boxes. A real platform draws it on a map; this
			// one shows the geometry the map would have produced.
			{Name: "boundary.geometry", Label: "Boundary (WKT)", Kind: "geometry",
				Required: true,
				Hint:     "POLYGON or MULTIPOLYGON, in WGS84; holes as further rings"},
		}
	default:
		return nil
	}
}

// sampleGeometry prefills the one field a person cannot reasonably type, so the
// form is usable without going and finding a polygon first.
const sampleGeometry = `POLYGON ((8.02 52.28, 8.04 52.28, 8.04 52.29, 8.02 52.29, 8.02 52.28))`

// physicalAddress is the form path of an organization's address: the PHYSICAL
// entry of its contact info's address list, which is the party's address.
const physicalAddress = "contactInfo.addressContactMethods[PHYSICAL]"

// fillChoices lists what each reference field may point at.
//
// Archived records are offered too: a field may well belong to a farm the
// platform has deactivated, and leaving it unpointed would be a worse lie than
// pointing at something inactive.
func fillChoices(tx *store.Tx, fields []formField) ([]formField, error) {
	out := make([]formField, 0, len(fields))
	for _, field := range fields {
		if field.Kind != "ref" {
			out = append(out, field)
			continue
		}
		localIDs, err := tx.LocalIDs(field.RefType)
		if err != nil {
			return nil, err
		}
		sort.Strings(localIDs)
		for _, localID := range localIDs {
			label := localID
			if record, err := tx.LoadRecord(field.RefType, localID); err == nil {
				if name := nameOfRecord(record); name != "" {
					label = fmt.Sprintf("%s — %s", name, localID)
				}
				// A party slot takes either of the platform's party types.
				if field.RefType == agmasync.TypeParty {
					label = fmt.Sprintf("%s (%s)", label, recordTypeOf(record))
				}
			}
			field.Choices = append(field.Choices, refChoice{Value: localID, Label: label})
		}
		out = append(out, field)
	}
	return out, nil
}

// nameOfRecord is what to call a record on screen.
func nameOfRecord(record store.Record) string {
	text := func(key string) string {
		raw, ok := record.Modelled[key]
		if !ok {
			return ""
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return ""
		}
		return v
	}
	if name := text("name"); name != "" {
		return name
	}
	// A boundary need not have a name; what it is called then is what it is for.
	return text("_agmasync_boundaryType")
}

// attributesFromForm turns what was typed into the attributes of an entity.
//
// This is the mapping a real integration writes, in the direction the sample's
// own screens exercise: the platform's fields in, the protocol's content
// attributes out. The reverse direction is [store.Record], which is where a
// delivered object is taken apart. An organization or a person goes out as a
// party whose _agmasync_details state which.
//
// A field left empty is left out altogether rather than sent as null. The two
// are different on the wire — absent means the sender says nothing about the
// attribute — and a form cannot tell "not filled in" from "deliberately
// cleared" anyway.
func attributesFromForm(typ recordType, form url.Values) ([]byte, error) {
	fields := formFields(typ)
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, typ)
	}

	out := map[string]any{}
	var missing []string
	var errs []error
	if partyType := typ.partyType(); partyType != "" {
		assign(out, "_agmasync_details.partyTypeCode", partyType)
	}
	for _, field := range fields {
		value := strings.TrimSpace(form.Get(field.Name))
		if value == "" {
			if field.Required {
				missing = append(missing, field.Label)
			}
			continue
		}

		parsed, err := parseFormValue(field, value)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		assign(out, field.Name, parsed)
	}

	// A farm's address is one address, of a type the form does not ask for.
	if address, ok := out["_agmasync_address"].(map[string]any); ok {
		address["addressContactTypeCode"] = "PHYSICAL"
	}

	if len(missing) > 0 {
		errs = append(errs, fmt.Errorf("fill in %s", strings.Join(missing, ", ")))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return json.Marshal(out)
}

func parseFormValue(field formField, value string) (any, error) {
	switch field.Kind {
	case "area":
		parsed, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return nil, fmt.Errorf("%s is not a number: %q", field.Label, value)
		}
		return oapi.Area{NumericValue: float32(parsed), UnitOfMeasureCode: oapi.Ha}, nil

	case "ref":
		// A reference goes out naming this platform's own identifier for the
		// target, which agrirouter resolves through the mapping — so a target
		// the platform does not hold cannot be named at all, which is why the
		// control is a list of what it holds rather than a text box. The slot
		// says which type it is.
		return agmasync.LocalRef(value), nil

	case "geometry":
		// Checked for its type only. Whether it is in the canonical form is
		// agrirouter's to judge, and the 400 says so.
		if !strings.HasPrefix(value, "POLYGON ") && !strings.HasPrefix(value, "MULTIPOLYGON ") {
			return nil, fmt.Errorf("%s is not a POLYGON or MULTIPOLYGON in WKT", field.Label)
		}
		return value, nil

	default:
		return value, nil
	}
}

// pathSegment is one step of a form path: an attribute, or with code set the
// entry of the address list key whose addressContactTypeCode is code.
type pathSegment struct {
	key, code string
}

func segmentsOf(path string) []pathSegment {
	var out []pathSegment
	for _, part := range strings.Split(path, ".") {
		key, code, selects := strings.Cut(part, "[")
		if selects {
			code = strings.TrimSuffix(code, "]")
		}
		out = append(out, pathSegment{key: key, code: code})
	}
	return out
}

// assign writes a value at a form path, creating the intermediate objects and
// list entries a nested attribute needs.
func assign(out map[string]any, path string, value any) {
	segments := segmentsOf(path)
	current := out
	for i, segment := range segments {
		if i == len(segments)-1 {
			current[segment.key] = value
			return
		}
		if segment.code == "" {
			child, ok := current[segment.key].(map[string]any)
			if !ok {
				child = map[string]any{}
				current[segment.key] = child
			}
			current = child
			continue
		}
		list, _ := current[segment.key].([]any)
		var entry map[string]any
		for _, item := range list {
			if candidate, ok := item.(map[string]any); ok && candidate["addressContactTypeCode"] == segment.code {
				entry = candidate
			}
		}
		if entry == nil {
			entry = map[string]any{"addressContactTypeCode": segment.code}
			current[segment.key] = append(list, entry)
		}
		current = entry
	}
}

// valueAt reads an attribute by the same path the form writes it under.
func valueAt(modelled map[string]json.RawMessage, path string) (json.RawMessage, bool) {
	current := modelled
	segments := segmentsOf(path)
	for i, segment := range segments {
		raw, ok := current[segment.key]
		if !ok {
			return nil, false
		}
		if segment.code != "" {
			var list []map[string]json.RawMessage
			if err := json.Unmarshal(raw, &list); err != nil {
				return nil, false
			}
			found := false
			for _, entry := range list {
				var code string
				if json.Unmarshal(entry["addressContactTypeCode"], &code) == nil && code == segment.code {
					current, found = entry, true
				}
			}
			if !found {
				return nil, false
			}
			continue
		}
		if i == len(segments)-1 {
			return raw, true
		}
		current = nil
		if err := json.Unmarshal(raw, &current); err != nil {
			return nil, false
		}
	}
	return nil, false
}
