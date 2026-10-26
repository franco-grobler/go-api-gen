// Package optional provides a value that distinguishes three JSON states:
// absent, explicitly null, and set to a value.
//
// A pointer can only express two of those, which is why PATCH endpoints
// written against pointer-based models cannot tell "leave this field alone"
// from "clear this field". Generated structs use Optional for every field that
// is not required.
package optional

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Optional holds a value that may be absent or explicitly null.
//
// The zero Optional is absent, so a decoded struct reports exactly the fields
// the client actually sent.
type Optional[T any] struct {
	val  T
	set  bool
	null bool
}

// Some returns an Optional holding v.
func Some[T any](v T) Optional[T] {
	return Optional[T]{val: v, set: true}
}

// Null returns an Optional that is present and explicitly null.
func Null[T any]() Optional[T] {
	return Optional[T]{set: true, null: true}
}

// Unset returns an absent Optional, the same as the zero value. It exists so
// that intent reads clearly at a call site.
func Unset[T any]() Optional[T] {
	return Optional[T]{}
}

// Get returns the value and whether one is present. A null Optional reports
// false, because there is no value to act on.
func (o Optional[T]) Get() (T, bool) {
	if !o.set || o.null {
		var zero T
		return zero, false
	}
	return o.val, true
}

// MustGet returns the value, panicking if there is none. Use it only where the
// surrounding code has already established the field is set.
func (o Optional[T]) MustGet() T {
	v, ok := o.Get()
	if !ok {
		panic("optional: MustGet on an " + o.state())
	}
	return v
}

// Or returns the value, or def when the Optional is absent or null.
func (o Optional[T]) Or(def T) T {
	if v, ok := o.Get(); ok {
		return v
	}
	return def
}

// IsSet reports whether the field was present in the JSON at all, including
// when it was present as null.
func (o Optional[T]) IsSet() bool { return o.set }

// IsNull reports whether the field was present and explicitly null.
func (o Optional[T]) IsNull() bool { return o.set && o.null }

// IsZero reports whether the field is absent. encoding/json consults this for
// the omitzero tag option, so an absent field is omitted on marshal.
func (o Optional[T]) IsZero() bool { return !o.set }

// Ptr returns a pointer to the value, or nil when absent or null, for interop
// with APIs that take a pointer.
func (o Optional[T]) Ptr() *T {
	if !o.set || o.null {
		return nil
	}
	v := o.val
	return &v
}

// FromPtr converts a pointer into an Optional: nil becomes null rather than
// absent, since a nil pointer in a populated struct means "no value".
func FromPtr[T any](p *T) Optional[T] {
	if p == nil {
		return Null[T]()
	}
	return Some(*p)
}

// MarshalJSON writes the value, or null when the field is null or absent.
//
// An absent optional field normally never reaches this method, because
// generated structs tag it with omitzero. When one does, for example because
// the field is required and nullable, or the value sits in a map or a slice,
// null is the only honest rendering.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.set || o.null {
		return []byte("null"), nil
	}
	return json.Marshal(o.val)
}

// UnmarshalJSON records that the field was present and decodes it.
//
// encoding/json does not call this method for a key that is absent from the
// object, which is exactly how absence stays distinguishable from null.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.null = true
		var zero T
		o.val = zero
		return nil
	}
	o.null = false
	return json.Unmarshal(data, &o.val)
}

// String renders the state for logs and test failures.
func (o Optional[T]) String() string {
	if v, ok := o.Get(); ok {
		return fmt.Sprintf("%v", v)
	}
	return o.state()
}

func (o Optional[T]) state() string {
	if !o.set {
		return "absent Optional"
	}
	return "null Optional"
}

// Equal reports whether two Optionals are in the same state and hold equal
// values. It is a free function because T is only comparable at the use site.
func Equal[T comparable](a, b Optional[T]) bool {
	if a.set != b.set || a.null != b.null {
		return false
	}
	if !a.set || a.null {
		return true
	}
	return a.val == b.val
}
