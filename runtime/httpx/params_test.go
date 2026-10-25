package httpx

import (
	"net/url"
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	if got := Split("", ","); got != nil {
		t.Errorf("Split(\"\") = %q, want no elements", got)
	}
	if got := Split("a,b", ","); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Split(\"a,b\") = %q", got)
	}
}

func TestPairs(t *testing.T) {
	got, err := Pairs("x,1,y,2", ",")
	if err != nil || !reflect.DeepEqual(got, map[string]string{
		"x": "1", "y": "2",
	}) {
		t.Errorf("Pairs = %v, %v", got, err)
	}
	if _, err := Pairs("x,1,y", ","); err == nil {
		t.Error("an odd count should not pair up")
	}
}

func TestKeyValues(t *testing.T) {
	got, err := KeyValues("x=1,y=2")
	if err != nil || !reflect.DeepEqual(got, map[string]string{
		"x": "1", "y": "2",
	}) {
		t.Errorf("KeyValues = %v, %v", got, err)
	}
	if _, err := KeyValues("x=1,y"); err == nil {
		t.Error("a key with no value should fail")
	}
}

func TestDeepObject(t *testing.T) {
	q := url.Values{
		"m[a]": {"1", "ignored"}, "m[]": {"x"}, "mm[b]": {"2"}, "m": {"3"},
	}
	want := map[string]string{"a": "1"}
	if got := DeepObject(q, "m"); !reflect.DeepEqual(got, want) {
		t.Errorf("DeepObject = %v, want %v", got, want)
	}
}

func TestPickAndRest(t *testing.T) {
	q := url.Values{"lo": {"1"}, "limit": {"10"}, "team": {"core"}}
	if got := Pick(q, "lo", "hi"); !reflect.DeepEqual(got,
		map[string]string{"lo": "1"}) {
		t.Errorf("Pick = %v", got)
	}
	if got := Rest(q, "lo", "limit"); !reflect.DeepEqual(got,
		map[string]string{"team": "core"}) {
		t.Errorf("Rest = %v", got)
	}
}
