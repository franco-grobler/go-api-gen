// Package union decides which variant of a oneOf or anyOf a JSON value is.
//
// Generated code declares a union as a struct with one pointer field per
// variant. Its UnmarshalJSON asks this package which variant to decode into:
// by the discriminator property when the schema names one, and otherwise by
// the shape of the value, described for each variant by a Shape.
//
// A value that fits no variant, or a discriminator that names none, is not a
// decoding error. It is a Problem, which the union records and its Validate
// method reports at the value's location, so the client gets a 422 that says
// where the value is and what it should have been, like any other
// validation failure.
package union

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
)

// Kind is a set of JSON value kinds.
type Kind uint8

// The JSON value kinds a Shape accepts. Number includes integers; Integer is
// a number written without a fraction or an exponent.
const (
	Object Kind = 1 << iota
	Array
	String
	Integer
	Number
	Boolean
	Null

	// Any accepts every kind of value except null.
	Any = Object | Array | String | Integer | Number | Boolean
)

// Shape describes the values one variant accepts, as far as telling the
// variants apart needs: the full constraints are checked by the variant's
// own validation once it is chosen.
type Shape struct {
	// Name identifies the variant in a Problem's message.
	Name string
	// Kinds are the JSON kinds the variant accepts.
	Kinds Kind
	// Values, when set, are the only values it accepts: an enum or a const,
	// each as compact JSON.
	Values []string
	// Required are the keys an object must have.
	Required []string
	// Properties are the keys the variant declares.
	Properties []string
	// Closed rejects an object with a key Properties does not list.
	Closed bool
	// Fixed lists the declared properties whose values are an enum or a
	// const: a key that is present must hold one of them. It is what tells
	// apart the variants that share their keys but not a "type" value.
	Fixed []Fixed
}

// Fixed is a property that only accepts certain values.
type Fixed struct {
	Property string
	// Values are compact JSON.
	Values []string
}

// Mode is how a union treats a value that fits more than one variant.
type Mode int

const (
	// OneOf requires a single best fit and reports a tie as ambiguous.
	OneOf Mode = iota
	// AnyOf takes the first of the best fits, in declaration order.
	AnyOf
)

// Problem is why a value could not be matched to a variant. Its fields are
// those of validate.V's Fail: a type slug, a message, and context pairs.
type Problem struct {
	Type string
	Msg  string
	Ctx  []any
}

// IsNull reports whether data is the JSON null.
func IsNull(data []byte) bool {
	return string(bytes.TrimSpace(data)) == "null"
}

// Choose returns the index of the variant data is, or -1 and why there is
// none.
//
// A variant fits when the value is a kind it accepts, any value it is
// restricted to, and for an object, when it has the required keys, no key a
// closed variant does not declare, and an allowed value under each Fixed
// property. Of the variants that fit, the most specific wins: for an object,
// the one that declares the most of its keys, which is how Pydantic's smart
// mode settles a union too. A tie is ambiguous under OneOf; under AnyOf the
// first of them wins.
func Choose(data []byte, mode Mode, shapes []Shape) (int, *Problem) {
	val := inspect(data)
	best, bestScore, tie := -1, -1, false
	for i := range shapes {
		score, ok := shapes[i].fit(&val)
		switch {
		case !ok:
		case score > bestScore:
			best, bestScore, tie = i, score, false
		case score == bestScore:
			tie = true
		}
	}
	names := make([]string, len(shapes))
	for i := range shapes {
		names[i] = shapes[i].Name
	}
	expected := strings.Join(names, ", ")
	switch {
	case best < 0:
		return -1, &Problem{
			Type: "union_no_match",
			Msg:  "Input should match one of: " + expected,
			Ctx:  []any{"expected", expected},
		}
	case tie && mode == OneOf:
		return -1, &Problem{
			Type: "union_ambiguous",
			Msg:  "Input matches more than one of: " + expected,
			Ctx:  []any{"expected", expected},
		}
	}
	return best, nil
}

// Tag returns the string value of the discriminator property, or a Problem
// when data is not an object holding one.
func Tag(data []byte, property string) (string, *Problem) {
	var obj map[string]json.RawMessage
	var tag string
	if json.Unmarshal(data, &obj) == nil {
		if raw, ok := obj[property]; ok && json.Unmarshal(raw, &tag) == nil {
			return tag, nil
		}
	}
	return "", &Problem{
		Type: "union_tag_not_found",
		Msg: "Unable to extract tag using discriminator '" + property +
			"'",
		Ctx: []any{"discriminator", "'" + property + "'"},
	}
}

// UnknownTag is the Problem for a discriminator value no variant has.
func UnknownTag(tag, property string, expected []string) *Problem {
	quoted := make([]string, len(expected))
	for i, e := range expected {
		quoted[i] = "'" + e + "'"
	}
	tags := strings.Join(quoted, ", ")
	return &Problem{
		Type: "union_tag_invalid",
		Msg: "Input tag '" + tag + "' found using '" + property +
			"' does not match any of the expected tags: " + tags,
		Ctx: []any{
			"discriminator", "'" + property + "'",
			"tag", tag,
			"expected_tags", tags,
		},
	}
}

// value is what Choose learns about data once, for every Shape to read.
type value struct {
	kind Kind
	data []byte
	obj  map[string]json.RawMessage // set when kind is Object
}

func inspect(data []byte) value {
	data = bytes.TrimSpace(data)
	v := value{data: data}
	if len(data) == 0 {
		return v
	}
	switch data[0] {
	case '{':
		if json.Unmarshal(data, &v.obj) == nil {
			v.kind = Object
		}
	case '[':
		v.kind = Array
	case '"':
		v.kind = String
	case 't', 'f':
		v.kind = Boolean
	case 'n':
		v.kind = Null
	default:
		v.kind = Number
		if !bytes.ContainsAny(data, ".eE") {
			v.kind = Integer
		}
	}
	return v
}

// fit reports whether the value fits the shape, and if so how specifically.
func (s *Shape) fit(v *value) (int, bool) {
	accepts := s.Kinds&v.kind != 0 ||
		v.kind == Integer && s.Kinds&Number != 0
	if !accepts {
		return 0, false
	}
	score := 0
	// An integer is more an Integer than a Number.
	if v.kind == Integer && s.Kinds&Integer != 0 {
		score++
	}
	if len(s.Values) > 0 {
		if !slices.Contains(s.Values, compact(v.data)) {
			return 0, false
		}
		score++
	}
	if v.kind != Object {
		return score, true
	}
	for _, r := range s.Required {
		if _, ok := v.obj[r]; !ok {
			return 0, false
		}
	}
	for key := range v.obj {
		if slices.Contains(s.Properties, key) {
			score++
		} else if s.Closed {
			return 0, false
		}
	}
	for _, f := range s.Fixed {
		raw, ok := v.obj[f.Property]
		if ok && !slices.Contains(f.Values, compact(raw)) {
			return 0, false
		}
	}
	return score, true
}

// compact renders JSON the way the generator writes a Shape's values, so
// equal values compare equal however the client spaced them.
func compact(data []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, data) != nil {
		return string(data)
	}
	return b.String()
}
