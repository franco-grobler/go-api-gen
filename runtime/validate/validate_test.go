package validate

import (
	"fmt"
	"testing"
)

func locOf(e FieldError) string { return fmt.Sprint(e.Loc) }

// A nested walk must report every failure, each with its own path.
func TestAccumulatesWithLocations(t *testing.T) {
	v := New(Body)
	v.Field("name").Fail("string_too_short", "too short", "min_length", 3)

	items := v.Field("items")
	for i := 0; i < 2; i++ {
		items.Index(i).Field("sku").Fail("missing", "Field required")
	}

	if !v.Failed() || v.Len() != 3 {
		t.Fatalf("recorded %d failures, want 3", v.Len())
	}
	want := []string{"[body name]", "[body items 0 sku]", "[body items 1 sku]"}
	for i, e := range v.Errors() {
		if got := locOf(e); got != want[i] {
			t.Errorf("errors[%d].loc = %s, want %s", i, got, want[i])
		}
	}
}

// Sibling walks share the accumulator but must not share the path's backing
// array, or one branch would overwrite another's location.
func TestSiblingLocationsAreIndependent(t *testing.T) {
	v := New(Body)
	parent := v.Field("address")
	parent.Field("line1").Fail("missing", "")
	parent.Field("country").Fail("missing", "")

	got := []string{locOf(v.Errors()[0]), locOf(v.Errors()[1])}
	want := []string{"[body address line1]", "[body address country]"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("errors[%d].loc = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestContextPairs(t *testing.T) {
	v := New(Query)
	v.Field("page").Fail("greater_than_equal", "too small", "ge", 1, "got", 0)
	e := v.Errors()[0]
	if e.Ctx["ge"] != 1 || e.Ctx["got"] != 0 {
		t.Errorf("ctx = %v, want ge 1 and got 0", e.Ctx)
	}

	// A malformed context must not take down the request.
	v2 := New(Body)
	v2.Field("x").Fail("bad", "msg", "odd")
	if v2.Errors()[0].Ctx != nil {
		t.Errorf("ctx = %v, want nil for an unpaired key", v2.Errors()[0].Ctx)
	}
	v3 := New(Body)
	v3.Field("x").Fail("bad", "msg", 42, "value")
	if _, present := v3.Errors()[0].Ctx["42"]; present {
		t.Error("a non-string context key should be skipped")
	}
}

func TestCleanValidator(t *testing.T) {
	v := New(Body)
	if v.Failed() || v.Len() != 0 {
		t.Errorf("a fresh validator reports %d failures", v.Len())
	}
	if got := v.Errors(); len(got) != 0 {
		t.Errorf("Errors() = %v, want empty", got)
	}
}

func TestLocIsACopy(t *testing.T) {
	v := New(Body)
	f := v.Field("name")
	loc := f.Loc()
	loc[0] = "tampered"
	f.Fail("x", "")
	if got := locOf(v.Errors()[0]); got != "[body name]" {
		t.Errorf("loc = %s; Loc() must return a copy", got)
	}
}
