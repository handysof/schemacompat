// Package schemacompat checks whether a new version of a JSON Schema
// (draft 2020-12, as parsed by github.com/google/jsonschema-go) still accepts
// every instance the current version accepts — i.e. whether data validated
// under the old schema stays valid under the new one.
//
// It's a conservative, rule-based check rather than a full schema-inclusion
// decision: it allows adding optional properties, marking them deprecated,
// relaxing constraints, and editing annotations; it rejects removing or
// retyping properties, making them required, tightening constraints, and
// adding or changing keywords it can't reason about structurally ($ref,
// oneOf, if/then/else, …). jsonschema-go validates instances but can't
// compare two schemas, which is what this adds.
package schemacompat

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// ErrIncompatible is matched (via errors.Is) by *IncompatibleError.
var ErrIncompatible = errors.New("incompatible schema change")

// IncompatibleError lists every way the new schema would reject instances
// the current one accepts.
type IncompatibleError struct {
	Violations []string
}

func (e *IncompatibleError) Error() string {
	return "incompatible schema change: " + strings.Join(e.Violations, "; ")
}

func (e *IncompatibleError) Is(target error) bool { return target == ErrIncompatible }

// Options adjust the check to what the caller knows about its instances.
type Options struct {
	// DeclaredKeysOnly means instances never hold object keys a schema
	// doesn't declare, unless it explicitly allows them with
	// additionalProperties or patternProperties — because the application
	// rejects such keys itself. Plain JSON Schema accepts undeclared keys by
	// default; without this option, a schema that doesn't set
	// additionalProperties is assumed to have let anything through, so any
	// new property, or newly closed additionalProperties, is incompatible.
	DeclaredKeysOnly bool
}

// Check returns an *IncompatibleError if next would reject any instance that
// current accepts, listing every violation found.
func Check(current, next *jsonschema.Schema, opts Options) error {
	c := &checker{opts: opts}
	c.compare("", current, next)
	if len(c.violations) > 0 {
		return &IncompatibleError{Violations: c.violations}
	}
	return nil
}

type checker struct {
	opts       Options
	violations []string
}

// opaqueKeywords are JSON Schema keywords whose effect on existing data the
// checker can't reason about structurally. Removing one only ever relaxes the
// schema, so that's allowed; adding or changing one is rejected.
var opaqueKeywords = []struct {
	name string
	get  func(*jsonschema.Schema) any
}{
	{"$ref", func(s *jsonschema.Schema) any { return s.Ref }},
	{"$defs", func(s *jsonschema.Schema) any { return s.Defs }},
	{"definitions", func(s *jsonschema.Schema) any { return s.Definitions }},
	{"allOf", func(s *jsonschema.Schema) any { return s.AllOf }},
	{"anyOf", func(s *jsonschema.Schema) any { return s.AnyOf }},
	{"oneOf", func(s *jsonschema.Schema) any { return s.OneOf }},
	{"not", func(s *jsonschema.Schema) any { return s.Not }},
	{"if", func(s *jsonschema.Schema) any { return s.If }},
	{"then", func(s *jsonschema.Schema) any { return s.Then }},
	{"else", func(s *jsonschema.Schema) any { return s.Else }},
	{"const", func(s *jsonschema.Schema) any { return s.Const }},
	{"multipleOf", func(s *jsonschema.Schema) any { return s.MultipleOf }},
	{"uniqueItems", func(s *jsonschema.Schema) any { return s.UniqueItems }},
	{"prefixItems", func(s *jsonschema.Schema) any { return s.PrefixItems }},
	{"items (array form)", func(s *jsonschema.Schema) any { return s.ItemsArray }},
	{"additionalItems", func(s *jsonschema.Schema) any { return s.AdditionalItems }},
	{"contains", func(s *jsonschema.Schema) any { return s.Contains }},
	{"unevaluatedItems", func(s *jsonschema.Schema) any { return s.UnevaluatedItems }},
	{"unevaluatedProperties", func(s *jsonschema.Schema) any { return s.UnevaluatedProperties }},
	{"minProperties", func(s *jsonschema.Schema) any { return s.MinProperties }},
	{"maxProperties", func(s *jsonschema.Schema) any { return s.MaxProperties }},
	{"dependentRequired", func(s *jsonschema.Schema) any { return s.DependentRequired }},
	{"dependentSchemas", func(s *jsonschema.Schema) any { return s.DependentSchemas }},
	{"dependencies", func(s *jsonschema.Schema) any { return s.DependencySchemas }},
	{"dependencies", func(s *jsonschema.Schema) any { return s.DependencyStrings }},
	{"patternProperties", func(s *jsonschema.Schema) any { return s.PatternProperties }},
	{"propertyNames", func(s *jsonschema.Schema) any { return s.PropertyNames }},
}

func (c *checker) compare(path string, old, nw *jsonschema.Schema) {
	at := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if path != "" {
			msg = fmt.Sprintf("%s: %s", path, msg)
		}
		c.violations = append(c.violations, msg)
	}

	if oldT, nwT := types(old), types(nw); !typesAccept(nwT, oldT) {
		at("type cannot change from %s to %s", typesString(oldT), typesString(nwT))
	}

	if nw.Enum != nil {
		if old.Enum == nil {
			at("cannot restrict to a fixed list of options")
		} else {
			allowed := map[string]bool{}
			for _, e := range nw.Enum {
				allowed[canonicalValue(e)] = true
			}
			for _, e := range old.Enum {
				if !allowed[canonicalValue(e)] {
					at("option %s cannot be removed", canonicalValue(e))
				}
			}
		}
	}

	if nw.Pattern != "" && nw.Pattern != old.Pattern {
		at("pattern cannot be added or changed")
	}
	if nw.Format != "" && nw.Format != old.Format {
		at("format cannot be added or changed")
	}
	raisedLowerBound(at, "minLength", old.MinLength, nw.MinLength)
	raisedLowerBound(at, "minimum", old.Minimum, nw.Minimum)
	raisedLowerBound(at, "exclusiveMinimum", old.ExclusiveMinimum, nw.ExclusiveMinimum)
	raisedLowerBound(at, "minItems", old.MinItems, nw.MinItems)
	loweredUpperBound(at, "maxLength", old.MaxLength, nw.MaxLength)
	loweredUpperBound(at, "maximum", old.Maximum, nw.Maximum)
	loweredUpperBound(at, "exclusiveMaximum", old.ExclusiveMaximum, nw.ExclusiveMaximum)
	loweredUpperBound(at, "maxItems", old.MaxItems, nw.MaxItems)

	for _, k := range opaqueKeywords {
		nv := k.get(nw)
		if !reflect.ValueOf(nv).IsZero() && !reflect.DeepEqual(nv, k.get(old)) {
			at("adding or changing %q is not supported", k.name)
		}
	}

	for _, key := range nw.Required {
		if !slices.Contains(old.Required, key) {
			if _, existed := old.Properties[key]; existed {
				at("field %q cannot be made required", key)
			} else {
				at("new field %q must be optional", key)
			}
		}
	}

	for _, key := range sortedKeys(old.Properties) {
		nwChild, ok := nw.Properties[key]
		if !ok {
			at(`field %q cannot be removed; mark it "deprecated": true instead`, key)
			continue
		}
		c.compare(joinPath(path, key), old.Properties[key], nwChild)
	}

	// A field new to the schema may already be in instances as an undeclared
	// key, if the old schema accepted those for it — via a matching
	// patternProperties entry or, failing that, additionalProperties. The new
	// field must still accept everything that let through.
	for _, key := range sortedKeys(nw.Properties) {
		if _, declared := old.Properties[key]; declared {
			continue
		}
		for _, governing := range c.undeclaredSchemas(old, key) {
			c.compare(joinPath(path, key), governing, nw.Properties[key])
		}
	}

	// additionalProperties is compared against what the old schema let
	// through for undeclared keys; items against the old items, where an
	// absent one accepts anything.
	switch {
	case isFalse(nw.AdditionalProperties):
		if c.additional(old) != nil {
			at("cannot disallow additional properties")
		}
	case nw.AdditionalProperties != nil:
		if oldAdditional := c.additional(old); oldAdditional != nil {
			c.compare(path+"{*}", oldAdditional, nw.AdditionalProperties)
		}
	}
	if nw.Items != nil {
		c.compare(path+"[]", orEmpty(old.Items), nw.Items)
	}
}

// undeclaredSchemas returns the schemas s applied to key while key wasn't one
// of its properties: every patternProperties entry matching key, or else
// whatever additionalProperties allowed (see additional).
func (c *checker) undeclaredSchemas(s *jsonschema.Schema, key string) []*jsonschema.Schema {
	var matched []*jsonschema.Schema
	for _, pattern := range sortedKeys(s.PatternProperties) {
		if re, err := regexp.Compile(pattern); err == nil && re.MatchString(key) {
			matched = append(matched, s.PatternProperties[pattern])
		}
	}
	if len(matched) > 0 {
		return matched
	}
	if a := c.additional(s); a != nil {
		return []*jsonschema.Schema{a}
	}
	return nil
}

// additional returns the schema s applied to undeclared keys not matched by
// patternProperties, or nil if s let none through: additionalProperties is
// false, or absent with Options.DeclaredKeysOnly. Absent otherwise accepts
// anything, so it's the empty schema.
func (c *checker) additional(s *jsonschema.Schema) *jsonschema.Schema {
	switch {
	case isFalse(s.AdditionalProperties):
		return nil
	case s.AdditionalProperties != nil:
		return s.AdditionalProperties
	case c.opts.DeclaredKeysOnly:
		return nil
	default:
		return &jsonschema.Schema{}
	}
}

// types returns s's "type" as a list (jsonschema-go splits it into Type and
// Types). An empty list means "any type".
func types(s *jsonschema.Schema) []string {
	if s.Type != "" {
		return []string{s.Type}
	}
	return s.Types
}

// typesAccept reports whether every value valid under old is still valid
// under nw (an integer is also a number).
func typesAccept(nw, old []string) bool {
	if len(nw) == 0 {
		return true
	}
	if len(old) == 0 {
		return false
	}
	for _, o := range old {
		if !slices.Contains(nw, o) && !(o == "integer" && slices.Contains(nw, "number")) {
			return false
		}
	}
	return true
}

func typesString(t []string) string {
	if len(t) == 0 {
		return "any"
	}
	return strings.Join(t, "|")
}

// isFalse reports whether s is the boolean schema false, which jsonschema-go
// unmarshals as {"not": {}}.
func isFalse(s *jsonschema.Schema) bool {
	return s != nil && s.Not != nil && reflect.ValueOf(*s.Not).IsZero()
}

func orEmpty(s *jsonschema.Schema) *jsonschema.Schema {
	if s == nil {
		return &jsonschema.Schema{}
	}
	return s
}

func raisedLowerBound[T int | float64](at func(string, ...any), keyword string, old, nw *T) {
	if nw != nil && (old == nil || *nw > *old) {
		at("%s cannot be added or raised", keyword)
	}
}

func loweredUpperBound[T int | float64](at func(string, ...any), keyword string, old, nw *T) {
	if nw != nil && (old == nil || *nw < *old) {
		at("%s cannot be added or lowered", keyword)
	}
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func sortedKeys(m map[string]*jsonschema.Schema) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// canonicalValue encodes an enum value so equal JSON values compare equal
// (encoding/json sorts object keys).
func canonicalValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
