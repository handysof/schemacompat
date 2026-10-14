package schemacompat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

const baseSchema = `{
	"type": "object",
	"properties": {
		"nic":        {"type": "string", "minLength": 5, "maxLength": 12},
		"area":       {"type": "string", "enum": ["nattandiya", "tabbowa"]},
		"age":        {"type": "integer", "minimum": 0, "maximum": 120},
		"occupation": {"type": "string"},
		"address":    {"type": "object", "properties": {"city": {"type": "string"}}},
		"tags":       {"type": "array", "items": {"type": "string"}}
	},
	"required": ["nic"]
}`

func mustParse(t *testing.T, raw string) *jsonschema.Schema {
	t.Helper()
	var s jsonschema.Schema
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if _, err := s.Resolve(nil); err != nil {
		t.Fatalf("resolve schema: %v", err)
	}
	return &s
}

// declaredOnly is how internal/form calls Check: its custom_fields never hold
// undeclared keys.
var declaredOnly = Options{DeclaredKeysOnly: true}

// withProps rebuilds baseSchema with some properties replaced (or removed
// when the value is "") and an optional replacement "required" list.
func withProps(overrides map[string]string, required string) string {
	props := map[string]string{
		"nic":        `{"type": "string", "minLength": 5, "maxLength": 12}`,
		"area":       `{"type": "string", "enum": ["nattandiya", "tabbowa"]}`,
		"age":        `{"type": "integer", "minimum": 0, "maximum": 120}`,
		"occupation": `{"type": "string"}`,
		"address":    `{"type": "object", "properties": {"city": {"type": "string"}}}`,
		"tags":       `{"type": "array", "items": {"type": "string"}}`,
	}
	for k, v := range overrides {
		if v == "" {
			delete(props, k)
		} else {
			props[k] = v
		}
	}
	var parts []string
	for _, k := range []string{"nic", "area", "age", "occupation", "address", "tags", "notes", "joined_via"} {
		if v, ok := props[k]; ok {
			parts = append(parts, `"`+k+`": `+v)
		}
	}
	if required == "" {
		required = `["nic"]`
	}
	return `{"type": "object", "properties": {` + strings.Join(parts, ", ") + `}, "required": ` + required + `}`
}

func TestCheck_Allowed(t *testing.T) {
	tests := []struct {
		name string
		next string
	}{
		{"identical", baseSchema},
		{"add optional field", withProps(map[string]string{"notes": `{"type": "string"}`}, "")},
		{"deprecate field", withProps(map[string]string{"occupation": `{"type": "string", "deprecated": true}`}, "")},
		{"change annotations", withProps(map[string]string{"occupation": `{"type": "string", "title": "Job", "description": "What they do"}`}, "")},
		{"add enum option", withProps(map[string]string{"area": `{"type": "string", "enum": ["nattandiya", "tabbowa", "kuruswatta"]}`}, "")},
		{"drop enum entirely", withProps(map[string]string{"area": `{"type": "string"}`}, "")},
		{"lower minLength", withProps(map[string]string{"nic": `{"type": "string", "minLength": 3, "maxLength": 12}`}, "")},
		{"raise maxLength", withProps(map[string]string{"nic": `{"type": "string", "minLength": 5, "maxLength": 20}`}, "")},
		{"drop bounds", withProps(map[string]string{"age": `{"type": "integer"}`}, "")},
		{"widen integer to number", withProps(map[string]string{"age": `{"type": "number", "minimum": 0, "maximum": 120}`}, "")},
		{"widen to type list", withProps(map[string]string{"occupation": `{"type": ["string", "null"]}`}, "")},
		{"drop type", withProps(map[string]string{"occupation": `{}`}, "")},
		{"make field optional", withProps(nil, `[]`)},
		{"close additionalProperties nothing used", withProps(map[string]string{"address": `{"type": "object", "properties": {"city": {"type": "string"}}, "additionalProperties": false}`}, "")},
		{"add nested optional field", withProps(map[string]string{"address": `{"type": "object", "properties": {"city": {"type": "string"}, "zip": {"type": "string"}}}`}, "")},
	}
	current := mustParse(t, baseSchema)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Check(current, mustParse(t, tt.next), declaredOnly); err != nil {
				t.Errorf("Check: unexpected error %v", err)
			}
		})
	}
}

func TestCheck_Rejected(t *testing.T) {
	tests := []struct {
		name string
		next string
		want string
	}{
		{"remove field", withProps(map[string]string{"occupation": ""}, ""), `field "occupation" cannot be removed`},
		{"change type", withProps(map[string]string{"occupation": `{"type": "integer"}`}, ""), "occupation: type cannot change from string to integer"},
		{"add enum", withProps(map[string]string{"occupation": `{"type": "string", "enum": ["a", "b"]}`}, ""), "occupation: cannot restrict to a fixed list of options"},
		{"remove enum option", withProps(map[string]string{"area": `{"type": "string", "enum": ["nattandiya"]}`}, ""), `area: option "tabbowa" cannot be removed`},
		{"raise minLength", withProps(map[string]string{"nic": `{"type": "string", "minLength": 8, "maxLength": 12}`}, ""), "nic: minLength cannot be added or raised"},
		{"lower maximum", withProps(map[string]string{"age": `{"type": "integer", "minimum": 0, "maximum": 99}`}, ""), "age: maximum cannot be added or lowered"},
		{"add pattern", withProps(map[string]string{"occupation": `{"type": "string", "pattern": "^[a-z]+$"}`}, ""), "occupation: pattern cannot be added or changed"},
		{"add format", withProps(map[string]string{"occupation": `{"type": "string", "format": "date"}`}, ""), "occupation: format cannot be added or changed"},
		{"require existing field", withProps(nil, `["nic", "occupation"]`), `field "occupation" cannot be made required`},
		{"require new field", withProps(map[string]string{"notes": `{"type": "string"}`}, `["nic", "notes"]`), `new field "notes" must be optional`},
		{"remove nested field", withProps(map[string]string{"address": `{"type": "object", "properties": {}}`}, ""), `address: field "city" cannot be removed`},
		{"change items type", withProps(map[string]string{"tags": `{"type": "array", "items": {"type": "integer"}}`}, ""), "tags[]: type cannot change"},
		{"add oneOf", withProps(map[string]string{"occupation": `{"type": "string", "oneOf": [{"maxLength": 3}]}`}, ""), `occupation: adding or changing "oneOf" is not supported`},
	}
	current := mustParse(t, baseSchema)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Check(current, mustParse(t, tt.next), declaredOnly)
			if !errors.Is(err, ErrIncompatible) {
				t.Fatalf("Check = %v, want ErrIncompatible", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestCheck_ReportsEveryViolation(t *testing.T) {
	next := withProps(map[string]string{"occupation": "", "age": `{"type": "string"}`}, `["nic", "area"]`)
	err := Check(mustParse(t, baseSchema), mustParse(t, next), declaredOnly)
	var incompatible *IncompatibleError
	if !errors.As(err, &incompatible) {
		t.Fatalf("Check = %v, want *IncompatibleError", err)
	}
	if len(incompatible.Violations) != 3 {
		t.Errorf("violations = %q, want 3 (removed, retyped, newly required)", incompatible.Violations)
	}
}

func TestCheck_NewFieldMustAcceptExistingUndeclaredValues(t *testing.T) {
	tests := []struct {
		name    string
		current string
		next    string
		want    string // "" = compatible
	}{
		{
			"typed additionalProperties, new field narrower",
			`{"type": "object", "properties": {}, "additionalProperties": {"type": "string"}}`,
			`{"type": "object", "properties": {"foo": {"type": "integer"}}, "additionalProperties": {"type": "string"}}`,
			"foo: type cannot change from string to integer",
		},
		{
			"typed additionalProperties, new field same type",
			`{"type": "object", "properties": {}, "additionalProperties": {"type": "string"}}`,
			`{"type": "object", "properties": {"foo": {"type": "string"}}, "additionalProperties": {"type": "string"}}`,
			"",
		},
		{
			"matching patternProperties governs, not additionalProperties",
			`{"type": "object", "properties": {}, "patternProperties": {"^x_": {"type": "string", "maxLength": 10}}, "additionalProperties": false}`,
			`{"type": "object", "properties": {"x_note": {"type": "string", "maxLength": 5}}, "patternProperties": {"^x_": {"type": "string", "maxLength": 10}}, "additionalProperties": false}`,
			"x_note: maxLength cannot be added or lowered",
		},
		{
			"no undeclared keys allowed before",
			`{"type": "object", "properties": {}}`,
			`{"type": "object", "properties": {"foo": {"type": "integer", "minimum": 1}}}`,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Check(mustParse(t, tt.current), mustParse(t, tt.next), declaredOnly)
			if tt.want == "" {
				if err != nil {
					t.Errorf("Check: unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Check = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

// Without DeclaredKeysOnly, a schema that never set additionalProperties is
// assumed to have accepted any undeclared key — plain JSON Schema semantics.
func TestCheck_UndeclaredKeysOpenByDefault(t *testing.T) {
	open := mustParse(t, `{"type": "object", "properties": {}}`)
	tests := []struct {
		name string
		next string
		want string // "" = compatible
	}{
		{"new typed field", `{"type": "object", "properties": {"foo": {"type": "integer"}}}`, "foo: type cannot change from any to integer"},
		{"new untyped field", `{"type": "object", "properties": {"foo": {"title": "Foo"}}}`, ""},
		{"close additionalProperties", `{"type": "object", "properties": {}, "additionalProperties": false}`, "cannot disallow additional properties"},
		{"type additionalProperties", `{"type": "object", "properties": {}, "additionalProperties": {"type": "string"}}`, "{*}: type cannot change from any to string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Check(open, mustParse(t, tt.next), Options{})
			if tt.want == "" {
				if err != nil {
					t.Errorf("Check: unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Check = %v, want error containing %q", err, tt.want)
			}
		})
		t.Run(tt.name+" (declared keys only)", func(t *testing.T) {
			if err := Check(open, mustParse(t, tt.next), declaredOnly); err != nil {
				t.Errorf("Check: unexpected error %v", err)
			}
		})
	}
}
