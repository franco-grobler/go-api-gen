// Package validate collects field-level validation failures.
//
// A V accumulates failures rather than returning on the first one, so a single
// request reports everything that is wrong with it. Each failure carries a
// location path into the request, mirroring the shape FastAPI produces:
// ["body", "items", 0, "name"].
package validate

// Part names the section of a request a location path starts from.
const (
	Body   = "body"
	Query  = "query"
	Path   = "path"
	Header = "header"
	Cookie = "cookie"
)

// FieldError is one validation failure.
type FieldError struct {
	// Type is a machine-readable slug such as "string_too_short".
	Type string `json:"type"`
	// Loc is the path to the offending value: string keys and int indexes.
	Loc []any `json:"loc"`
	// Msg is a human-readable explanation.
	Msg string `json:"msg"`
	// Ctx carries the constraint that failed, for example min_length: 3.
	Ctx map[string]any `json:"ctx,omitempty"`
}

// V accumulates failures at a location.
//
// Field and Index return a V pointing deeper into the request while sharing
// the same underlying list, so a nested walk needs no plumbing to report back
// up. The zero V is not usable; start one with New.
type V struct {
	loc  []any
	errs *[]FieldError
}

// New returns a V rooted at the given location, usually a request part such as
// validate.Body.
func New(root ...any) *V {
	errs := make([]FieldError, 0, 4)
	return &V{loc: root, errs: &errs}
}

// Field returns a V pointing at a named member of the current location.
func (v *V) Field(name string) *V {
	return v.push(name)
}

// Index returns a V pointing at an element of the current location.
func (v *V) Index(i int) *V {
	return v.push(i)
}

// push returns a V one level deeper, copying the path so that sibling walks
// cannot overwrite each other's location through a shared backing array.
func (v *V) push(seg any) *V {
	loc := make([]any, len(v.loc)+1)
	copy(loc, v.loc)
	loc[len(v.loc)] = seg
	return &V{loc: loc, errs: v.errs}
}

// Loc returns a copy of the current location path.
func (v *V) Loc() []any {
	out := make([]any, len(v.loc))
	copy(out, v.loc)
	return out
}

// Fail records a failure at the current location.
//
// ctx holds alternating key/value pairs describing the constraint, for example
// Fail("string_too_short", "...", "min_length", 3). An odd trailing key is
// ignored rather than panicking, since a validator should never take down a
// request handler.
func (v *V) Fail(slug, msg string, ctx ...any) {
	e := FieldError{Type: slug, Loc: v.Loc(), Msg: msg}
	if len(ctx) >= 2 {
		e.Ctx = make(map[string]any, len(ctx)/2)
		for i := 0; i+1 < len(ctx); i += 2 {
			key, ok := ctx[i].(string)
			if !ok {
				continue
			}
			e.Ctx[key] = ctx[i+1]
		}
	}
	*v.errs = append(*v.errs, e)
}

// Add records an already-built failure at its own location.
func (v *V) Add(e FieldError) {
	*v.errs = append(*v.errs, e)
}

// Failed reports whether anything has been recorded.
func (v *V) Failed() bool { return len(*v.errs) > 0 }

// Errors returns the failures recorded so far, in the order they were found.
func (v *V) Errors() []FieldError { return *v.errs }

// Len returns how many failures have been recorded.
func (v *V) Len() int { return len(*v.errs) }

// Validator is implemented by a generated type that has custom rules declared
// with x-validate. The generated code calls it after the schema's own
// constraints have been checked.
type Validator interface {
	Validate(v *V)
}

// Absent reports whether name is one of the required properties the client
// omitted.
//
// Generated validation calls this to skip a property's other checks. A field
// nobody sent is reported as missing; reporting it as too short as well would
// describe a value that does not exist, and a form binding to the errors would
// show two complaints under one input.
func Absent(missing []string, name string) bool {
	for _, m := range missing {
		if m == name {
			return true
		}
	}
	return false
}

// OneOfString reports whether v is one of the allowed values. Generated code
// uses it for an inline enum; a named enum type gets its own Valid method.
func OneOfString(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
