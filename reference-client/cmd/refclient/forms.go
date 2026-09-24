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
	"github.com/DKE-Data/masterdata-sync-working-group/reference-client/internal/platform/store"
)

// The create form is this platform's own screen, not the protocol's.
//
// A farm management system asks its user for a name and an owner, not for a
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
	// Name is the input's name and the protocol attribute it fills. A dotted
	// name is a path into a nested object, so "address.city" fills
	// {"address": {"city": …}}.
	Name  string
	Label string

	// Kind picks the control and how the value is parsed: text, number, enum,
	// ref or geometry.
	Kind string

	Placeholder string
	Hint        string

	// Examples are an extensible enumeration's documented values. They are
	// offered rather than enforced — that is what extensible means, and a
	// select would teach the opposite.
	Examples []string

	// RefTypes are the entity types a reference may point at. More than one
	// where the protocol admits either, as a farm's owner admits an
	// organization or a person.
	RefTypes []agmasync.EntityType

	// Choices are the records currently available to point at. Filled per
	// request by [fillChoices]; a reference can only name something the
	// platform already holds, because that is what the wire carries — this
	// platform's own identifier for the target.
	Choices []refChoice

	Required bool
}

// refChoice is one record a reference may name. Value carries the target's type
// as well as its identifier, since a party slot admits two types and the
// reference has to say which.
type refChoice struct {
	Value string
	Label string
}

// formFields is what the platform asks for, per entity type.
//
// It mirrors the columns in internal/platform/store: a field here with no
// column there would be typed in and silently dropped.
func formFields(typ agmasync.EntityType) []formField {
	switch typ {
	case agmasync.TypeOrganization:
		return []formField{
			{Name: "name", Label: "Name", Kind: "text",
				Placeholder: "Hof Nord GmbH", Required: true},
			{Name: "commercial_registry_number", Label: "Commercial registry number",
				Kind: "text", Placeholder: "HRB 12345"},
			{Name: "address.city", Label: "City", Kind: "text", Placeholder: "Osnabrück"},
			{Name: "address.country", Label: "Country", Kind: "text", Placeholder: "DE"},
		}
	case agmasync.TypePerson:
		return []formField{
			{Name: "last_name", Label: "Last name", Kind: "text",
				Placeholder: "Meyer", Required: true},
			{Name: "first_name", Label: "First name", Kind: "text", Placeholder: "Anke"},
			{Name: "title", Label: "Title", Kind: "text", Placeholder: "Dr."},
		}
	case agmasync.TypeFarm:
		return []formField{
			{Name: "name", Label: "Name", Kind: "text",
				Placeholder: "Hof Nord", Required: true},
			// Required because the protocol requires it. A farm without an
			// owner is not a farm this platform can send at all, so accepting
			// one here would only hold a record back for a 400 later — and the
			// order it imposes, a party before the farm it holds, is the
			// protocol's own.
			{Name: "owner", Label: "Owner", Kind: "ref",
				RefTypes: []agmasync.EntityType{
					agmasync.TypeOrganization, agmasync.TypePerson},
				Hint:     "an organization or a person held here",
				Required: true},
			{Name: "address.city", Label: "City", Kind: "text", Placeholder: "Osnabrück"},
		}
	case agmasync.TypeField:
		return []formField{
			{Name: "name", Label: "Name", Kind: "text",
				Placeholder: "Nordacker", Required: true},
			{Name: "area", Label: "Area (ha)", Kind: "number", Placeholder: "12.4"},
			{Name: "farm", Label: "Farm", Kind: "ref",
				RefTypes: []agmasync.EntityType{agmasync.TypeFarm},
				Hint:     "the farm it belongs to"},
		}
	case agmasync.TypeFieldBoundary:
		return []formField{
			{Name: "boundary_type", Label: "Boundary type", Kind: "enum",
				Examples: []string{"CONCEPTUAL", "OPERATIONAL",
					"ECONOMIC_DEFINED", "ADMINISTRATIVE_RECEIVED"},
				Hint: "extensible: another value is allowed"},
			{Name: "creation_method", Label: "Creation method", Kind: "enum",
				Examples: []string{"UNKNOWN", "MANUAL", "DRIVEN", "SURVEYED",
					"AUTO_OPERATION", "AUTO_IMAGERY", "ADMINISTRATIVE"}},
			// The one field left as JSON, because a polygon is not something a
			// person types into boxes. A real platform draws it on a map; this
			// one shows the geometry the map would have produced.
			{Name: "boundary", Label: "Boundary (GeoJSON)", Kind: "geometry",
				Required: true,
				Hint:     "Polygon or MultiPolygon"},
		}
	default:
		return nil
	}
}

// sampleGeometry prefills the one field a person cannot reasonably type, so the
// form is usable without going and finding a polygon first.
const sampleGeometry = `{"type":"Polygon","coordinates":` +
	`[[[8.02,52.28],[8.04,52.28],[8.04,52.29],[8.02,52.29],[8.02,52.28]]]}`

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
		for _, typ := range field.RefTypes {
			localIDs, err := tx.LocalIDs(typ)
			if err != nil {
				return nil, err
			}
			sort.Strings(localIDs)
			for _, localID := range localIDs {
				label := localID
				if name, err := displayName(tx, typ, localID); err == nil && name != "" {
					label = fmt.Sprintf("%s — %s", name, localID)
				}
				if len(field.RefTypes) > 1 {
					label = fmt.Sprintf("%s (%s)", label, typ)
				}
				field.Choices = append(field.Choices, refChoice{
					Value: string(typ) + ":" + localID,
					Label: label,
				})
			}
		}
		out = append(out, field)
	}
	return out, nil
}

// displayName is what to call a record on screen: the name it carries, or for a
// person the name assembled from the parts the protocol keeps apart.
func displayName(tx *store.Tx, typ agmasync.EntityType, localID string) (string, error) {
	record, err := tx.LoadRecord(typ, localID)
	if err != nil {
		return "", err
	}
	return nameOfRecord(record), nil
}

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
	parts := []string{}
	for _, key := range []string{"title", "first_name", "last_name"} {
		if v := text(key); v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, " ")
	}
	// A boundary has no name of its own; what it is called is what it is for.
	return text("boundary_type")
}

// attributesFromForm turns what was typed into the attributes of an entity.
//
// This is the mapping a real integration writes, in the direction the sample's
// own screens exercise: the platform's fields in, the protocol's attributes
// out. The reverse direction is [store.Record], which is where a delivered
// object is taken apart.
//
// A field left empty is left out altogether rather than sent as null. The two
// are different on the wire — absent means the sender says nothing about the
// attribute — and a form cannot tell "not filled in" from "deliberately
// cleared" anyway.
func attributesFromForm(typ agmasync.EntityType, form url.Values) ([]byte, error) {
	fields := formFields(typ)
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: %q", agmasync.ErrUnknownEntityType, typ)
	}

	out := map[string]any{}
	var missing []string
	var errs []error
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
	case "number":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is not a number: %q", field.Label, value)
		}
		return parsed, nil

	case "ref":
		// The select carries the target's type alongside its identifier. A
		// reference goes out naming this platform's own identifier for the
		// target, which agrirouter resolves through the mapping — so a target
		// the platform does not hold cannot be named at all, which is why the
		// control is a list of what it holds rather than a text box.
		targetType, localID, ok := strings.Cut(value, ":")
		if !ok || localID == "" {
			return nil, fmt.Errorf("%s is not a reference: %q", field.Label, value)
		}
		return map[string]string{"type": targetType, "local_id": localID}, nil

	case "geometry":
		var geometry any
		if err := json.Unmarshal([]byte(value), &geometry); err != nil {
			return nil, fmt.Errorf("%s is not JSON: %w", field.Label, err)
		}
		return geometry, nil

	default:
		return value, nil
	}
}

// assign writes a value at a dotted path, creating the intermediate object a
// nested attribute needs.
func assign(out map[string]any, path string, value any) {
	parent, leaf, nested := strings.Cut(path, ".")
	if !nested {
		out[path] = value
		return
	}
	child, ok := out[parent].(map[string]any)
	if !ok {
		child = map[string]any{}
		out[parent] = child
	}
	child[leaf] = value
}
