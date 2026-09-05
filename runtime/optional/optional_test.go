package optional

import (
	"encoding/json"
	"testing"
)

// UpdateUser is the shape the three-state distinction exists for: a PATCH body
// where "leave alone" and "clear it" are different requests.
type UpdateUser struct {
	Name  Optional[string] `json:"name,omitzero"`
	Email Optional[string] `json:"email,omitzero"`
	Age   Optional[int]    `json:"age,omitzero"`
}

func TestThreeStatesFromJSON(t *testing.T) {
	var u UpdateUser
	if err := json.Unmarshal(
		[]byte(`{"name":"ada","email":null}`),
		&u,
	); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Set to a value.
	if v, ok := u.Name.Get(); !ok || v != "ada" {
		t.Errorf("Name.Get() = %q, %v; want \"ada\", true", v, ok)
	}
	if !u.Name.IsSet() || u.Name.IsNull() {
		t.Errorf(
			"Name: IsSet=%v IsNull=%v; want true, false",
			u.Name.IsSet(),
			u.Name.IsNull(),
		)
	}

	// Explicitly null: present, but with no value to act on.
	if !u.Email.IsSet() {
		t.Error("Email.IsSet() = false; an explicit null is still present")
	}
	if !u.Email.IsNull() {
		t.Error("Email.IsNull() = false; want true")
	}
	if _, ok := u.Email.Get(); ok {
		t.Error("Email.Get() reported a value for an explicit null")
	}

	// Absent: the key never appeared.
	if u.Age.IsSet() || u.Age.IsNull() {
		t.Errorf(
			"Age: IsSet=%v IsNull=%v; want false, false",
			u.Age.IsSet(),
			u.Age.IsNull(),
		)
	}
	if !u.Age.IsZero() {
		t.Error("Age.IsZero() = false; an absent field must be zero for " +
			"omitzero")
	}
}

func TestMarshalOmitsAbsentKeepsNull(t *testing.T) {
	u := UpdateUser{Name: Some("ada"), Email: Null[string]()}
	got, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"name":"ada","email":null}`; string(got) != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

// A decode followed by an encode must reproduce exactly the fields the client
// sent, which is what makes a generated PATCH handler safe to pass through.
func TestRoundTrip(t *testing.T) {
	for _, in := range []string{
		`{}`,
		`{"name":"ada"}`,
		`{"email":null}`,
		`{"name":"ada","email":null,"age":36}`,
	} {
		var u UpdateUser
		if err := json.Unmarshal([]byte(in), &u); err != nil {
			t.Fatalf("Unmarshal(%s): %v", in, err)
		}
		out, err := json.Marshal(u)
		if err != nil {
			t.Fatalf("Marshal(%s): %v", in, err)
		}
		if string(out) != in {
			t.Errorf("round trip of %s produced %s", in, out)
		}
	}
}

func TestConstructorsAndAccessors(t *testing.T) {
	if v := Some(7).Or(1); v != 7 {
		t.Errorf("Some(7).Or(1) = %d, want 7", v)
	}
	if v := Null[int]().Or(1); v != 1 {
		t.Errorf("Null.Or(1) = %d, want 1", v)
	}
	if v := Unset[int]().Or(1); v != 1 {
		t.Errorf("Unset.Or(1) = %d, want 1", v)
	}
	if Unset[int]().IsSet() {
		t.Error("Unset().IsSet() = true")
	}
	if got := Some(7).MustGet(); got != 7 {
		t.Errorf("MustGet = %d, want 7", got)
	}
	if p := Some(7).Ptr(); p == nil || *p != 7 {
		t.Errorf("Some(7).Ptr() = %v, want a pointer to 7", p)
	}
	if p := Null[int]().Ptr(); p != nil {
		t.Errorf("Null.Ptr() = %v, want nil", p)
	}

	// A nil pointer means "no value", which is null rather than absent.
	if got := FromPtr[int](nil); !got.IsNull() {
		t.Errorf("FromPtr(nil) = %v, want null", got)
	}
	n := 3
	if got := FromPtr(&n); !got.IsSet() || got.MustGet() != 3 {
		t.Errorf("FromPtr(&3) = %v, want set to 3", got)
	}
}

func TestMustGetPanics(t *testing.T) {
	for name, o := range map[string]Optional[int]{
		"absent": Unset[int](),
		"null":   Null[int](),
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("MustGet did not panic")
				}
			}()
			_ = o.MustGet()
		})
	}
}

// Optional must nest: a struct value behind an Optional round-trips whole.
func TestNestedStruct(t *testing.T) {
	type Address struct {
		Line1 string           `json:"line1"`
		Line2 Optional[string] `json:"line2,omitzero"`
	}
	type Person struct {
		Addr Optional[Address] `json:"addr,omitzero"`
	}

	var p Person
	if err := json.Unmarshal(
		[]byte(`{"addr":{"line1":"1 High St"}}`),
		&p,
	); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	addr, ok := p.Addr.Get()
	if !ok || addr.Line1 != "1 High St" {
		t.Fatalf("Addr = %v, %v", addr, ok)
	}
	if addr.Line2.IsSet() {
		t.Error("nested absent field reported as set")
	}

	out, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"addr":{"line1":"1 High St"}}`; string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}
}

func TestDecodeErrorPropagates(t *testing.T) {
	var u UpdateUser
	if err := json.Unmarshal([]byte(`{"age":"not a number"}`), &u); err == nil {
		t.Error("Unmarshal accepted a string for Optional[int]")
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b Optional[string]
		want bool
	}{
		{"same value", Some("a"), Some("a"), true},
		{"different value", Some("a"), Some("b"), false},
		{"both null", Null[string](), Null[string](), true},
		{"both absent", Unset[string](), Unset[string](), true},
		{"null vs absent", Null[string](), Unset[string](), false},
		{"null vs empty value", Null[string](), Some(""), false},
	}
	for _, tt := range tests {
		if got := Equal(tt.a, tt.b); got != tt.want {
			t.Errorf(
				"%s: Equal(%v, %v) = %v, want %v",
				tt.name,
				tt.a,
				tt.b,
				got,
				tt.want,
			)
		}
	}
}
