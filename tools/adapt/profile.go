package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// schemaSpec is one schema of adapt/deviations.yaml.
type schemaSpec struct {
	name        string
	adapt       string
	description any // string, or folded
	exclude     []string
	optional    []string
	unprefixed  []string
	// replacedWhole marks an object a write replaces whole, such as an array
	// entry: its optional properties do not accept null.
	replacedWhole bool
	props         []*propSpec
	enumeration   string
	examples      []any
}

// propSpec is one entry of a schema's properties.
type propSpec struct {
	name        string
	description any
	nullable    bool
	nullableSet bool
	extensible  bool // an extensible enumeration, see codeList
	required    bool
	ref         string
	typ         string
	enumeration string
	examples    []any
	cons        map[string]any
	additional  any // additionalProperties
	items       any
}

// src holds the lines of the profile, from which folded descriptions are
// copied as written.
var src []string

// empty is an entry that only names the property.
func (p *propSpec) empty() bool {
	return p.description == nil && !p.nullableSet && !p.required && p.ref == "" && p.typ == "" &&
		p.enumeration == "" && p.examples == nil && len(p.cons) == 0 && p.additional == nil && p.items == nil
}

func readProfile(path string) ([]*schemaSpec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseProfile(b, path)
}

func parseProfile(b []byte, path string) ([]*schemaSpec, error) {
	src = strings.Split(string(b), "\n")
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}
	top, err := pairs(doc.Content[0], path)
	if err != nil {
		return nil, err
	}
	var schemas []*schemaSpec
	for _, p := range top {
		if p[0].Value != "schemas" {
			return nil, fmt.Errorf("%s:%d: unknown key %q", path, p[0].Line, p[0].Value)
		}
		entries, err := pairs(p[1], path)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			s, err := parseSchema(e[0].Value, e[1], path)
			if err != nil {
				return nil, err
			}
			schemas = append(schemas, s)
		}
	}
	return schemas, nil
}

func parseSchema(name string, n *yaml.Node, path string) (*schemaSpec, error) {
	s := &schemaSpec{name: name}
	ps, err := pairs(n, path)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		k, v := p[0].Value, p[1]
		at := fmt.Sprintf("%s:%d: %s.%s", path, p[0].Line, name, k)
		switch k {
		case "adapt":
			s.adapt, err = scalar(v, at)
		case "description":
			s.description, err = description(p[0], v, at)
		case "exclude":
			s.exclude, err = stringList(v, at)
		case "optional":
			s.optional, err = stringList(v, at)
		case "replacedWhole":
			err = v.Decode(&s.replacedWhole)
		case "unprefixed":
			s.unprefixed, err = stringList(v, at)
		case "enumeration":
			s.enumeration, err = scalar(v, at)
		case "examples":
			s.examples, err = list(v, at)
		case "properties":
			var entries [][2]*yaml.Node
			if entries, err = pairs(v, path); err != nil {
				return nil, err
			}
			for _, e := range entries {
				prop, err := parseProp(e[0].Value, e[1], name, path)
				if err != nil {
					return nil, err
				}
				s.props = append(s.props, prop)
			}
		default:
			return nil, fmt.Errorf("%s: unknown key", at)
		}
		if err != nil {
			return nil, err
		}
	}
	if s.adapt == "" {
		return nil, fmt.Errorf("%s:%d: %s has no adapt definition", path, n.Line, name)
	}
	return s, nil
}

func parseProp(name string, n *yaml.Node, schema, path string) (*propSpec, error) {
	p := &propSpec{name: name, cons: map[string]any{}}
	if n.Tag == "!!null" {
		return p, nil
	}
	ps, err := pairs(n, path)
	if err != nil {
		return nil, err
	}
	for _, kv := range ps {
		k, v := kv[0].Value, kv[1]
		at := fmt.Sprintf("%s:%d: %s.%s.%s", path, kv[0].Line, schema, name, k)
		switch k {
		case "description":
			p.description, err = description(kv[0], v, at)
		case "nullable":
			p.nullableSet = true
			err = v.Decode(&p.nullable)
		case "required":
			err = v.Decode(&p.required)
		case "ref":
			p.ref, err = scalar(v, at)
		case "type":
			p.typ, err = scalar(v, at)
		case "enumeration":
			p.enumeration, err = scalar(v, at)
		case "examples":
			p.examples, err = list(v, at)
		case "additionalProperties":
			p.additional, err = value(v)
		case "items":
			p.items, err = value(v)
		case "format", "enum", "minimum", "maximum", "minItems", "maxItems", "pattern":
			p.cons[k], err = value(v)
		default:
			return nil, fmt.Errorf("%s: unknown key", at)
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", at, err)
		}
	}
	return p, nil
}

func pairs(n *yaml.Node, path string) ([][2]*yaml.Node, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s:%d: expected a mapping", path, n.Line)
	}
	var out [][2]*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, [2]*yaml.Node{n.Content[i], n.Content[i+1]})
	}
	return out, nil
}

func scalar(n *yaml.Node, at string) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("%s: expected a string", at)
	}
	return n.Value, nil
}

// description keeps a folded description as the profile writes it: its
// lines, with the indentation of the profile removed.
func description(key, n *yaml.Node, at string) (any, error) {
	s, err := scalar(n, at)
	if err != nil {
		return nil, err
	}
	if n.Style != yaml.FoldedStyle {
		return s, nil
	}
	keyIndent := key.Column - 1
	var lines []string
	for _, l := range src[n.Line:] {
		trimmed := strings.TrimLeft(l, " ")
		if trimmed != "" && len(l)-len(trimmed) <= keyIndent {
			break
		}
		lines = append(lines, l)
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	strip := -1
	for _, l := range lines {
		if t := strings.TrimLeft(l, " "); t != "" && (strip < 0 || len(l)-len(t) < strip) {
			strip = len(l) - len(t)
		}
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
		} else {
			lines[i] = l[strip:]
		}
	}
	return folded{text: s, lines: lines}, nil
}

func stringList(n *yaml.Node, at string) ([]string, error) {
	var out []string
	if err := n.Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: %w", at, err)
	}
	return out, nil
}

func list(n *yaml.Node, at string) ([]any, error) {
	if n.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s: expected a list", at)
	}
	v, err := value(n)
	if err != nil {
		return nil, err
	}
	return []any(v.(flowSeq)), nil
}

// value converts a YAML node to the ordered model the emitter writes.
// Sequences of scalars stay flow sequences.
func value(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return value(n.Alias)
	case yaml.MappingNode:
		var m omap
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := value(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m = append(m, kv{n.Content[i].Value, v})
		}
		return m, nil
	case yaml.SequenceNode:
		out := flowSeq{}
		for _, c := range n.Content {
			v, err := value(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	default:
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	}
}
