package main

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestRepositoryUpToDate fails when openapi.yaml does not match adapt/.
func TestRepositoryUpToDate(t *testing.T) {
	if err := run("../..", true); err != nil {
		t.Fatal(err)
	}
}

// testStd is a small ADAPT: a thing with a required name and code, and an
// optional note.
func testStd() *adaptStd {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	return &adaptStd{
		order: map[string][]string{"thing": {"name", "code", "note"}},
		defs: map[string]map[string]any{
			"name":      str(),
			"colorCode": str(),
			"thing": {
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"allOf": []any{
						map[string]any{"$ref": "#/$defs/name"},
						map[string]any{"description": "The name."},
					}},
					"code": map[string]any{"$ref": "#/$defs/colorCode"},
					"note": map[string]any{"type": "array", "items": str(), "minItems": 1.0},
				},
				"required": []any{"name", "code"},
			},
		},
		enums: map[string][]string{"Color": {"A", "B"}},
	}
}

func generateTest(t *testing.T, profile string) ([]genSchema, []string) {
	t.Helper()
	prof, err := parseProfile([]byte(profile), "deviations.yaml")
	if err != nil {
		t.Fatal(err)
	}
	existing := &components{types: map[string]string{"Obj": "object", "Text": "string"}}
	out, _, errs := generate(prof, testStd(), existing)
	return out, errs
}

func TestGenerate(t *testing.T) {
	out, errs := generateTest(t, `
schemas:
  Thing:
    adapt: thing
    properties:
      code:
        description: Or C.
        examples: [C]
      note:
        minItems: 0
      _agmasync_ref:
        ref: Obj
`)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	want := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"code": map[string]any{"type": "string", "examples": []any{"A", "B", "C"},
				"description": extensibleEnum + " From the ADAPT Color list. Or C.\n"},
			"note": map[string]any{"type": []any{"array", "null"}, "items": map[string]any{"type": "string"}},
			"_agmasync_ref": map[string]any{"oneOf": []any{
				map[string]any{"$ref": "#/components/schemas/Obj"},
				map[string]any{"type": "null"},
			}},
		},
		"required": []any{"name", "code"},
	}
	if got := normalize(out[0].body); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

// TestGenerateRejects covers the changes an ADAPT upgrade can bring, each of
// which the profile must account for.
func TestGenerateRejects(t *testing.T) {
	cases := []struct {
		name, schema, want string
	}{
		{"entry that changes nothing", `
    exclude: [note]
    properties: {name: {}}`,
			"name is kept as ADAPT defines it without an entry; drop it"},
		{"property both customised and excluded", `
    exclude: [note]
    properties: {note: {nullable: true}}`,
			"note is both a property and excluded"},
		{"exclusion of a property ADAPT dropped", `
    exclude: [note, gone]`,
			"excludes gone, which ADAPT does not have"},
		{"requirement dropped silently", `
    exclude: [note, code]`,
			"excludes code, which ADAPT requires; list it in optional"},
		{"relaxation ADAPT made itself", `
    exclude: [note]
    optional: [note]`,
			"lists note as optional, which ADAPT does not require"},
		{"requirement ADAPT made itself", `
    exclude: [note]
    properties: {name: {required: true}}`,
			"name is required by ADAPT already"},
		{"unprefixed addition", `
    exclude: [note]
    properties: {extra: {type: string}}`,
			"ADAPT has no property extra"},
		{"ref to a schema of another type", `
    exclude: [note]
    properties: {code: {ref: Obj}}`,
			"ref Obj is of type object, but ADAPT's is string"},
		{"example ADAPT lists now", `
    exclude: [note]
    properties: {code: {examples: [B]}}`,
			"example B is in ADAPT's Color list now"},
		{"enumeration that follows from ADAPT", `
    exclude: [note]
    properties: {code: {enumeration: Color}}`,
			"enumeration Color follows from ADAPT"},
		{"nullable stated where it is inferred", `
    exclude: [note]
    properties: {_agmasync_x: {type: string, nullable: true}}`,
			"_agmasync_x is nullable: true without saying so"},
		{"required and nullable", `
    exclude: [note]
    properties: {_agmasync_x: {type: string, required: true, nullable: true}}`,
			"_agmasync_x is required and nullable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := generateTest(t, "schemas:\n  Thing:\n    adapt: thing"+c.schema+"\n")
			if !strings.Contains(strings.Join(errs, "\n"), c.want) {
				t.Errorf("errors %q do not contain %q", errs, c.want)
			}
		})
	}
}

// TestReplacedWhole: in an object replaced whole, optional properties do
// not accept null.
func TestReplacedWhole(t *testing.T) {
	out, errs := generateTest(t, `
schemas:
  Thing:
    adapt: thing
    replacedWhole: true
    optional: [code]
`)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	props := normalize(out[0].body).(map[string]any)["properties"].(map[string]any)
	for name, want := range map[string]any{"code": "string", "note": "array"} {
		if got := props[name].(map[string]any)["type"]; got != want {
			t.Errorf("%s: type %v, want %v", name, got, want)
		}
	}
}

func TestEmitReadsBack(t *testing.T) {
	model := omap{
		{"plain", "a string"},
		{"reserved", flowSeq{"null", "true", "1.5", "0x10", "~"}},
		{"indicators", flowSeq{"#/x", "a: b", "it's", "-x", "a,b", "https://x.example"}},
		{"pattern", "^[A-Z]{2}$"},
		{"$ref", "#/components/schemas/X"},
		{"folded", folded{text: strings.Repeat("word ", 30) + "[a link](https://example.com/a/very/long/path/that/does/not/fit/on/one/line/at/all)\n\nsecond  paragraph\n"}},
		{"long", flowSeq{strings.Repeat("A", 40), strings.Repeat("B", 40)}},
		{"seq", []any{omap{{"$ref", "x"}, {"type", "null"}}, "y"}},
		{"empty", omap{}},
		{"numbers", flowSeq{0, 1.5, false}},
	}
	var b strings.Builder
	emitMap(&b, model, 0)
	var got any
	if err := yaml.Unmarshal([]byte(b.String()), &got); err != nil {
		t.Fatalf("%v\n%s", err, b.String())
	}
	if !reflect.DeepEqual(normalize(got), normalize(model)) {
		t.Errorf("read back differs:\n%s", b.String())
	}
	for _, l := range strings.Split(b.String(), "\n") {
		if len(l) > width && !strings.Contains(l, "https://") {
			t.Errorf("line over %d columns: %q", width, l)
		}
	}
}
