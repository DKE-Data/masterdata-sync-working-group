package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// prefix names an addition, a property ADAPT has no slot for.
const prefix = "_agmasync_"

type genSchema struct {
	name string
	body omap
}

type generator struct {
	std           *adaptStd
	existing      map[string]string          // components/schemas of openapi.yaml: name -> type
	existingProps map[string]map[string]bool // their properties
	types         map[string]string          // generated schemas: name -> type
	byDef         map[string][]string        // ADAPT definition -> generated schemas based on it
	errs          []string
	notes         []string
}

func (g *generator) fail(format string, args ...any) {
	g.errs = append(g.errs, fmt.Sprintf(format, args...))
}

// generate builds every schema of the profile. It collects all problems
// rather than stopping at the first, so that an ADAPT upgrade shows the
// whole list of decisions to make.
func generate(prof []*schemaSpec, std *adaptStd, existing *components) ([]genSchema, []string, []string) {
	g := &generator{
		std:           std,
		existing:      existing.types,
		existingProps: existing.props,
		types:         map[string]string{},
		byDef:         map[string][]string{},
	}
	for _, s := range prof {
		def, ok := std.defs[s.adapt]
		if !ok {
			g.fail("%s: ADAPT has no definition %q", s.name, s.adapt)
			continue
		}
		if _, dup := g.types[s.name]; dup {
			g.fail("%s: defined twice", s.name)
		}
		g.types[s.name], _ = def["type"].(string)
		g.byDef[s.adapt] = append(g.byDef[s.adapt], s.name)
	}

	var out []genSchema
	for _, s := range prof {
		def, ok := std.defs[s.adapt]
		if !ok {
			continue
		}
		var body omap
		if g.types[s.name] == "object" {
			body = g.object(s, def)
		} else {
			body = g.primitive(s, def)
		}
		out = append(out, genSchema{s.name, body})
	}
	return out, g.notes, g.errs
}

// typeOf is the JSON type of a schema in components/schemas.
func (g *generator) typeOf(name string) (string, bool) {
	if t, ok := g.types[name]; ok {
		return t, true
	}
	t, ok := g.existing[name]
	return t, ok
}

func (g *generator) object(s *schemaSpec, def map[string]any) omap {
	at := fmt.Sprintf("%s (ADAPT %s)", s.name, s.adapt)
	if s.enumeration != "" || s.examples != nil {
		g.fail("%s: enumeration and examples apply to properties of an object", at)
	}
	props, _ := def["properties"].(map[string]any)
	adaptRequired := set(anyStrings(def["required"]))
	excluded := set(s.exclude)
	optional := set(s.optional)
	unprefixed := set(s.unprefixed)

	listed := map[string]*propSpec{}
	for _, p := range s.props {
		if listed[p.name] != nil {
			g.fail("%s: property %s listed twice", at, p.name)
		}
		listed[p.name] = p
		if _, ok := props[p.name]; ok && p.empty() {
			g.fail("%s: %s is kept as ADAPT defines it without an entry; drop it", at, p.name)
		}
	}

	// Every exclusion and relaxation still applies.
	for _, name := range s.exclude {
		if _, ok := props[name]; !ok {
			g.fail("%s: excludes %s, which ADAPT does not have", at, name)
		}
		if listed[name] != nil {
			g.fail("%s: %s is both a property and excluded", at, name)
		}
	}
	for _, name := range s.optional {
		if !adaptRequired[name] {
			g.fail("%s: lists %s as optional, which ADAPT does not require", at, name)
		}
	}
	for _, name := range sortedKeys(adaptRequired) {
		if excluded[name] && !optional[name] {
			g.fail("%s: excludes %s, which ADAPT requires; list it in optional", at, name)
		}
	}
	for _, name := range s.unprefixed {
		if _, ok := props[name]; ok || listed[name] == nil {
			g.fail("%s: unprefixed lists %s, which is not an addition", at, name)
		}
	}

	properties := omap{}
	var required flowSeq
	// An optional property accepts null, which removes it on a write, unless
	// its object is replaced whole.
	add := func(p *propSpec, isRequired bool, build func(string, *propSpec) omap) {
		inferred := !isRequired && !s.replacedWhole
		switch {
		case !p.nullableSet:
			p.nullable = inferred
		case p.nullable && isRequired:
			g.fail("%s: %s is required and nullable", at, p.name)
		case p.nullable == inferred:
			g.fail("%s: %s is nullable: %t without saying so; drop it", at, p.name, inferred)
		}
		properties = append(properties, kv{p.name, build(s.name, p)})
		if isRequired {
			required = append(required, p.name)
		}
	}

	// Every ADAPT property not excluded is kept, in ADAPT's order.
	for _, name := range g.std.order[s.adapt] {
		if excluded[name] {
			continue
		}
		p := listed[name]
		if p == nil {
			p = &propSpec{name: name, cons: map[string]any{}}
		}
		if have, ok := g.existingProps[s.name]; ok && !have[name] {
			g.notes = append(g.notes, fmt.Sprintf("%s: keeps ADAPT property %s, which openapi.yaml does not have yet; exclude it if it is not wanted", at, name))
		}
		isRequired := adaptRequired[name] && !optional[name]
		if p.required {
			if isRequired {
				g.fail("%s: %s is required by ADAPT already; drop required: true", at, name)
			}
			if optional[name] {
				g.fail("%s: %s is both optional and required", at, name)
			}
			isRequired = true
		}
		raw := props[name]
		add(p, isRequired, func(schema string, p *propSpec) omap { return g.adaptProperty(schema, p, raw) })
	}

	// Additions follow, in the profile's order.
	for _, p := range s.props {
		if _, ok := props[p.name]; ok {
			continue
		}
		if !strings.HasPrefix(p.name, prefix) && !unprefixed[p.name] {
			g.fail("%s: ADAPT has no property %s. An addition is named %s<name>, "+
				"or, in a schema used only inside a prefixed value, listed in unprefixed", at, p.name, prefix)
		}
		if slot := strings.TrimPrefix(p.name, prefix); slot != p.name {
			if _, ok := props[slot]; ok {
				g.notes = append(g.notes, fmt.Sprintf("%s: ADAPT now has %s; %s may move there in a breaking version", at, slot, p.name))
			}
		}
		add(p, p.required, g.addition)
	}

	out := omap{{"type", "object"}, {"additionalProperties", false}}
	if s.description != nil {
		out = append(out, kv{"description", s.description})
	}
	out = append(out, kv{"properties", properties})
	if len(required) > 0 {
		out = append(out, kv{"required", required})
	}
	return out
}

// primitive builds a schema based on an ADAPT scalar type, such as a code.
func (g *generator) primitive(s *schemaSpec, def map[string]any) omap {
	at := fmt.Sprintf("%s (ADAPT %s)", s.name, s.adapt)
	if s.props != nil || s.exclude != nil || s.optional != nil || s.unprefixed != nil {
		g.fail("%s: ADAPT %s is a %s, without properties", at, s.adapt, g.types[s.name])
	}
	sh, err := g.std.resolveInline(def, at)
	if err != nil {
		g.fail("%v", err)
		return nil
	}
	p := &propSpec{description: s.description, enumeration: s.enumeration, examples: s.examples, cons: map[string]any{}}
	g.codeList(at, p, s.adapt)
	return g.render(at, p, sh.typ, "", nil, sh.cons)
}

// adaptProperty builds a property ADAPT has: type and constraints from
// ADAPT, the rest from the profile.
func (g *generator) adaptProperty(schema string, p *propSpec, raw any) omap {
	at := schema + "." + p.name
	m, _ := raw.(map[string]any)
	sh, err := g.std.resolve(m, at)
	if err != nil {
		g.fail("%v", err)
		return nil
	}
	if p.typ != "" || p.items != nil || p.additional != nil {
		g.fail("%s: type, items and additionalProperties come from ADAPT", at)
	}

	leaf := sh
	if sh.typ == "array" {
		leaf = sh.items
	}
	target := p.ref
	if target == "" && leaf.def != "" {
		switch names := g.byDef[leaf.def]; {
		case len(names) == 1:
			target = names[0]
		case len(names) > 1 && leaf.typ == "object":
			g.fail("%s: several schemas are based on ADAPT %s (%s); set ref", at, leaf.def, strings.Join(names, ", "))
		}
	}
	if target == "" && leaf.typ == "object" {
		g.fail("%s: ADAPT %s is an object; set ref to the schema it maps to", at, leaf.def)
	}
	if target != "" {
		t, ok := g.typeOf(target)
		switch {
		case !ok:
			g.fail("%s: ref %s is not a schema of openapi.yaml", at, target)
		case t != leaf.typ:
			g.fail("%s: ref %s is of type %s, but ADAPT's is %s", at, target, t, leaf.typ)
		}
		if _, generated := g.types[target]; !generated && len(leaf.cons) > 0 {
			g.notes = append(g.notes, fmt.Sprintf("%s: ADAPT constrains it (%s); check that %s does too",
				at, strings.Join(sortedKeys(leaf.cons), ", "), target))
		}
	}

	if target == "" {
		g.codeList(at, p, leaf.def)
	}

	var itemCons map[string]any
	cons := sh.cons
	if sh.typ == "array" && target == "" {
		itemCons = leaf.cons
	}
	return g.render(at, p, sh.typ, target, &shape{typ: leaf.typ, cons: itemCons}, cons)
}

// addition builds a property ADAPT has no slot for, typed by the profile.
func (g *generator) addition(schema string, p *propSpec) omap {
	at := schema + "." + p.name
	switch {
	case p.typ == "" && p.ref == "":
		g.fail("%s: an addition needs type or ref", at)
	case p.typ != "" && p.typ != "array" && p.ref != "":
		g.fail("%s: ref with type %s; ref alone refers to a schema, with type array to the items", at, p.typ)
	case p.typ == "array" && p.ref == "" && p.items == nil:
		g.fail("%s: an array needs ref or items", at)
	case p.enumeration != "":
		g.fail("%s: enumeration applies to ADAPT codes; give examples", at)
	}
	if p.ref != "" {
		if _, ok := g.typeOf(p.ref); !ok {
			g.fail("%s: ref %s is not a schema of openapi.yaml", at, p.ref)
		}
	}
	typ := p.typ
	var items *shape
	if typ == "array" && p.items != nil {
		items = &shape{}
	}
	return g.render(at, p, typ, p.ref, items, nil)
}

// render writes a property schema. typ is its JSON type, or empty for a
// reference; target is the schema it, or an array's items, refers to.
// Constraints from ADAPT (adaptCons, and the items' in items.cons) are
// overridden by the profile's, where minItems 0 removes ADAPT's.
func (g *generator) render(at string, p *propSpec, typ, target string, items *shape, adaptCons map[string]any) omap {
	cons, itemCons := map[string]any{}, map[string]any{}
	for k, v := range adaptCons {
		cons[k] = v
	}
	if items != nil {
		for k, v := range items.cons {
			itemCons[k] = v
		}
	}
	for k, v := range p.cons {
		if typ == "array" && !isArrayKeyword(k) {
			itemCons[k] = v
		} else {
			cons[k] = v
		}
	}
	if cons["minItems"] == 0 {
		delete(cons, "minItems")
	}

	ref := func() omap { return omap{{"$ref", "#/components/schemas/" + target}} }
	var out omap
	switch {
	case typ == "array" || (typ != "" && target == ""):
		if p.nullable {
			out = append(out, kv{"type", flowSeq{typ, "null"}})
		} else {
			out = append(out, kv{"type", typ})
		}
	case p.nullable:
		out = append(out, kv{"oneOf", []any{ref(), omap{{"type", "null"}}}})
	default:
		out = ref()
	}
	if v, ok := cons["format"]; ok {
		out = append(out, kv{"format", v})
	}
	if d := describe(p); d != nil {
		out = append(out, kv{"description", d})
	}
	if typ == "array" {
		switch {
		case target != "":
			out = append(out, kv{"items", ref()})
		case p.items != nil:
			out = append(out, kv{"items", p.items})
		case items != nil:
			body := omap{{"type", items.typ}}
			for _, k := range constraintKeys {
				if v, ok := itemCons[k]; ok {
					body = append(body, kv{k, v})
				}
			}
			out = append(out, kv{"items", body})
		}
	}
	for _, k := range constraintKeys {
		if v, ok := cons[k]; ok && k != "format" {
			out = append(out, kv{k, v})
		}
	}
	if p.additional != nil {
		out = append(out, kv{"additionalProperties", p.additional})
	}
	if ex := g.examples(at, p); len(ex) > 0 {
		out = append(out, kv{"examples", ex})
	}
	return out
}

// examples are the VALID codes of the ADAPT enumeration, followed by the
// profile's examples. An example ADAPT lists itself is an error, so that a
// code ADAPT adopts is noticed.
func (g *generator) examples(at string, p *propSpec) flowSeq {
	var out flowSeq
	if p.enumeration != "" {
		codes, ok := g.std.enums[p.enumeration]
		if !ok {
			g.fail("%s: ADAPT has no enumeration %s", at, p.enumeration)
		}
		for _, c := range codes {
			out = append(out, c)
		}
	}
	for _, e := range p.examples {
		if slices.Contains(out, e) {
			g.fail("%s: example %v is in ADAPT's %s list now; drop it from examples", at, e, p.enumeration)
			continue
		}
		out = append(out, e)
	}
	return out
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func anyStrings(v any) []string {
	var out []string
	l, _ := v.([]any)
	for _, e := range l {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// extensibleEnum prefixes the description of every extensible enumeration
// (see "Extensible enumerations" in specification.md).
const extensibleEnum = "[Extensible enum](https://github.com/DKE-Data/masterdata-sync-working-group/blob/main/specification.md#extensible-enumerations)."

// codeList makes a code an extensible enumeration of the ADAPT list it takes
// its values from: <name>Code those of <Name>. A closed enum stays as it is.
func (g *generator) codeList(at string, p *propSpec, def string) {
	list := ""
	if name, ok := strings.CutSuffix(def, "Code"); ok && name != "" {
		if _, ok := g.std.enums[strings.ToUpper(name[:1])+name[1:]]; ok {
			list = strings.ToUpper(name[:1]) + name[1:]
		}
	}
	if p.cons["enum"] != nil {
		return
	}
	switch {
	case p.enumeration != "" && p.enumeration == list:
		g.fail("%s: enumeration %s follows from ADAPT; drop it", at, list)
	case p.enumeration == "":
		p.enumeration = list
	}
	p.extensible = p.enumeration != ""
}

// describe is a property's description: for an extensible enumeration the
// prefix and the ADAPT list, then the profile's text.
func describe(p *propSpec) any {
	if !p.extensible {
		return p.description
	}
	head := []string{extensibleEnum, "From the ADAPT " + p.enumeration + " list."}
	switch d := p.description.(type) {
	case nil:
		return folded{text: strings.Join(head, " ") + "\n", lines: head}
	case string:
		head = append(head, d)
		return folded{text: strings.Join(head, " ") + "\n", lines: head}
	case folded:
		text := strings.Join(head, " ") + " " + d.text
		if d.lines == nil {
			return folded{text: text}
		}
		return folded{text: text, lines: append(head, d.lines...)}
	}
	return p.description
}
