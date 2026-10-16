package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	rootSchemaFile = "adapt-root-schema.json"
	dataTypesFile  = "adapt-data-type-definitions.json"
)

// lockFile is adapt/adapt.lock.yaml, written by tools/adapt/pull.sh.
type lockFile struct {
	Repo   string            `yaml:"repo"`
	Tag    string            `yaml:"tag"`
	Commit string            `yaml:"commit"`
	Files  map[string]string `yaml:"files"`
}

// readLock reads the lock and checks every file it lists against its
// checksum, so the generator never runs on an edited copy of ADAPT.
func readLock(dir string) (*lockFile, error) {
	b, err := os.ReadFile(filepath.Join(dir, "adapt.lock.yaml"))
	if err != nil {
		return nil, fmt.Errorf("%w (run tools/adapt/pull.sh <tag>)", err)
	}
	var l lockFile
	if err := yaml.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("adapt/adapt.lock.yaml: %w", err)
	}
	for _, f := range []string{rootSchemaFile, dataTypesFile} {
		if _, ok := l.Files[f]; !ok {
			return nil, fmt.Errorf("adapt/adapt.lock.yaml lists no %s", f)
		}
	}
	for name, want := range l.Files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != want {
			return nil, fmt.Errorf("adapt/%s does not match adapt/adapt.lock.yaml. The files in adapt/ are "+
				"not edited: deviations go in adapt/profile.yaml. Run tools/adapt/pull.sh %s to restore it", name, l.Tag)
		}
	}
	return &l, nil
}

// adaptStd is the pinned ADAPT release: the type definitions of the root
// schema, and the VALID codes of each enumerated data type.
type adaptStd struct {
	defs  map[string]map[string]any
	order map[string][]string // property names of each definition, in ADAPT's order
	enums map[string][]string
}

func loadADAPT(dir string, l *lockFile) (*adaptStd, error) {
	var root struct {
		ID   string                    `json:"$id"`
		Defs map[string]map[string]any `json:"$defs"`
	}
	if err := readJSON(filepath.Join(dir, rootSchemaFile), &root); err != nil {
		return nil, err
	}
	order, err := propertyOrder(filepath.Join(dir, rootSchemaFile))
	if err != nil {
		return nil, err
	}
	// The checksums pin the files; a version that disagrees with the tag is
	// upstream's to fix, and worth knowing about.
	if !strings.HasSuffix(root.ID, "/"+l.Tag) {
		fmt.Fprintf(os.Stderr, "adapt: note: adapt/%s has $id %q at tag %s\n", rootSchemaFile, root.ID, l.Tag)
	}

	var dtd struct {
		Version string `json:"version"`
		Defs    []struct {
			Code       string `json:"definitionCode"`
			Enumerated *struct {
				Items []struct {
					Code   string `json:"code"`
					Status string `json:"enumerationItemStatusCode"`
				} `json:"items"`
			} `json:"enumeratedDataTypeDefinitionAttributes"`
		} `json:"dataTypeDefinitions"`
	}
	if err := readJSON(filepath.Join(dir, dataTypesFile), &dtd); err != nil {
		return nil, err
	}
	if dtd.Version != l.Tag {
		fmt.Fprintf(os.Stderr, "adapt: note: adapt/%s has version %q at tag %s\n", dataTypesFile, dtd.Version, l.Tag)
	}

	std := &adaptStd{defs: root.Defs, order: order, enums: map[string][]string{}}
	for _, d := range dtd.Defs {
		if d.Enumerated == nil {
			continue
		}
		var codes []string
		for _, it := range d.Enumerated.Items {
			if it.Status == "VALID" {
				codes = append(codes, it.Code)
			}
		}
		std.enums[d.Code] = codes
	}
	return std, nil
}

// propertyOrder reads the property names of each definition in the order
// ADAPT writes them, which decoding into a map loses.
func propertyOrder(path string) (map[string][]string, error) {
	var root struct {
		Defs map[string]struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := readJSON(path, &root); err != nil {
		return nil, err
	}
	order := map[string][]string{}
	for name, def := range root.Defs {
		if def.Properties == nil {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(def.Properties))
		if _, err := dec.Token(); err != nil { // {
			return nil, fmt.Errorf("%s: %s: %w", path, name, err)
		}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, name, err)
			}
			order[name] = append(order[name], key.(string))
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", path, name, err)
			}
		}
	}
	return order, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// shape is an ADAPT property schema with ADAPT's allOf wrappers and $refs
// resolved.
type shape struct {
	def   string         // the ADAPT definition it was reached through, if any
	typ   string         // JSON type
	items *shape         // for arrays
	cons  map[string]any // validation keywords, see constraintKeys
}

// constraintKeys are the validation keywords carried over from ADAPT, and
// the ones a profile entry may set.
var constraintKeys = []string{"format", "enum", "minimum", "maximum", "minItems", "maxItems", "pattern"}

func isArrayKeyword(k string) bool { return k == "minItems" || k == "maxItems" }

func (a *adaptStd) resolve(s map[string]any, at string) (*shape, error) {
	if all, ok := s["allOf"].([]any); ok {
		for k := range s {
			if k != "allOf" && k != "description" {
				return nil, fmt.Errorf("%s: unsupported keyword %q beside allOf", at, k)
			}
		}
		// ADAPT wraps a $ref and the description of its use in an allOf.
		var ref string
		for _, e := range all {
			m, _ := e.(map[string]any)
			for k, v := range m {
				switch k {
				case "$ref":
					ref, _ = v.(string)
				case "description":
				default:
					return nil, fmt.Errorf("%s: unsupported keyword %q in allOf", at, k)
				}
			}
		}
		if ref == "" {
			return nil, fmt.Errorf("%s: allOf without $ref", at)
		}
		return a.resolveRef(ref, at)
	}
	if ref, ok := s["$ref"].(string); ok {
		for k := range s {
			if k != "$ref" && k != "description" {
				return nil, fmt.Errorf("%s: unsupported keyword %q beside $ref", at, k)
			}
		}
		return a.resolveRef(ref, at)
	}
	return a.resolveInline(s, at)
}

func (a *adaptStd) resolveRef(ref, at string) (*shape, error) {
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	if !ok || a.defs[name] == nil {
		return nil, fmt.Errorf("%s: unresolvable $ref %q", at, ref)
	}
	sh, err := a.resolveInline(a.defs[name], at)
	if err != nil {
		return nil, err
	}
	sh.def = name
	return sh, nil
}

func (a *adaptStd) resolveInline(s map[string]any, at string) (*shape, error) {
	typ, _ := s["type"].(string)
	if typ == "" {
		return nil, fmt.Errorf("%s: ADAPT schema without a type", at)
	}
	sh := &shape{typ: typ, cons: map[string]any{}}
	for k, v := range s {
		switch k {
		case "type", "description":
		case "properties", "required":
			if typ != "object" {
				return nil, fmt.Errorf("%s: %q on a %s", at, k, typ)
			}
		case "items":
			m, _ := v.(map[string]any)
			if typ != "array" || m == nil {
				return nil, fmt.Errorf("%s: unexpected items", at)
			}
			it, err := a.resolve(m, at+"[]")
			if err != nil {
				return nil, err
			}
			sh.items = it
		case "format", "enum", "minimum", "maximum", "minItems", "maxItems", "pattern":
			// minItems 0 is the default.
			if k == "minItems" && v == 0.0 {
				continue
			}
			sh.cons[k] = jsonValue(v)
		default:
			return nil, fmt.Errorf("%s: unsupported ADAPT keyword %q; teach tools/adapt about it", at, k)
		}
	}
	if typ == "array" && sh.items == nil {
		return nil, fmt.Errorf("%s: array without items", at)
	}
	return sh, nil
}

// jsonValue turns integral JSON numbers into ints, as YAML would read them.
func jsonValue(v any) any {
	switch x := v.(type) {
	case float64:
		if x == math.Trunc(x) {
			return int(x)
		}
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonValue(e)
		}
		return flowSeq(out)
	}
	return v
}
