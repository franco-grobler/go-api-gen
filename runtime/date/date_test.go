package date

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestParseAcceptsFullDates(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Date
	}{
		{"2026-10-27", Date{2026, time.October, 27}},
		{"2024-02-29", Date{2024, time.February, 29}}, // leap year
		{"2000-02-29", Date{2000, time.February, 29}}, // divisible by 400
		{"0000-01-01", Date{0, time.January, 1}},
		{"9999-12-31", Date{9999, time.December, 31}},
	} {
		got, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
		if got.String() != tc.in {
			t.Errorf("Parse(%q).String() = %q", tc.in, got.String())
		}
	}
}

func TestParseRejectsWhatIsNotAFullDate(t *testing.T) {
	for _, in := range []string{
		"",
		"2026-10-27T00:00:00Z", // a timestamp is not a date
		"2026-10-27T00:00:00",
		"2026-10-27 ",
		" 2026-10-27",
		"2026-10-27Z",
		"2026-13-01", // no thirteenth month
		"2026-00-10",
		"2026-02-29", // not a leap year
		"1900-02-29", // divisible by 100, not by 400
		"2026-04-31",
		"2026-10-00",
		"2026-1-27",
		"26-10-27",
		"20261027",
		"2026/10/27",
		"+2026-10-27",
		"２０２６-10-27",
	} {
		_, err := Parse(in)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("Parse(%q) = %v, want a *ParseError", in, err)
		}
	}
}

func TestJSONIsAFullDateString(t *testing.T) {
	type period struct {
		From Date  `json:"from"`
		To   *Date `json:"to"`
	}
	in := period{
		From: MustParse("2024-02-29"),
		To:   new(MustParse("2026-10-27")),
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"from":"2024-02-29","to":"2026-10-27"}`; string(b) != want {
		t.Errorf("marshalled %s, want %s", b, want)
	}

	var out period
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.From != in.From || *out.To != *in.To {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}
}

func TestJSONRejectsTimestampsAndNonStrings(t *testing.T) {
	for _, in := range []string{
		`"2026-10-27T00:00:00Z"`,
		`"2026-02-30"`,
		`""`,
		`20261027`,
	} {
		var d Date
		if err := json.Unmarshal([]byte(in), &d); err == nil {
			t.Errorf("Unmarshal(%s) = %v, want an error", in, d)
		}
	}
}

func TestInvalidDateIsNotEncoded(t *testing.T) {
	for _, d := range []Date{
		{},
		{2026, time.February, 29},
		{10000, time.January, 1},
		{2026, 13, 1},
	} {
		if b, err := json.Marshal(d); err == nil {
			t.Errorf("Marshal(%+v) = %s, want an error", d, b)
		}
	}
}

func TestOfIgnoresTheTimeOfDay(t *testing.T) {
	loc := time.FixedZone("UTC+14", 14*60*60)
	instant := time.Date(2026, time.October, 27, 11, 0, 0, 0, time.UTC)
	if got := Of(instant); got != (Date{2026, time.October, 27}) {
		t.Errorf("Of(UTC) = %v", got)
	}
	// The same instant is already the next day further east.
	if got := Of(instant.In(loc)); got != (Date{2026, time.October, 28}) {
		t.Errorf("Of(UTC+14) = %v", got)
	}
	if got := MustParse("2026-10-27").In(loc); !got.Equal(
		time.Date(2026, time.October, 27, 0, 0, 0, 0, loc)) {
		t.Errorf("In = %v", got)
	}
}

func TestArithmeticAndOrder(t *testing.T) {
	d := MustParse("2024-02-28")
	if got := d.AddDays(1); got.String() != "2024-02-29" {
		t.Errorf("AddDays(1) = %v", got)
	}
	if got := d.AddDays(2); got.String() != "2024-03-01" {
		t.Errorf("AddDays(2) = %v", got)
	}
	if got := d.AddDays(-59); got.String() != "2023-12-31" {
		t.Errorf("AddDays(-59) = %v", got)
	}
	later := MustParse("2024-03-01")
	same := MustParse("2024-02-28")
	if !d.Before(later) || d.After(later) || d.Compare(same) != 0 ||
		later.Compare(d) != 1 {
		t.Error("ordering is wrong")
	}
	if !(Date{}).IsZero() || d.IsZero() || (Date{}).IsValid() {
		t.Error("IsZero or IsValid is wrong")
	}
}

func TestMustParsePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParse did not panic on a timestamp")
		}
	}()
	MustParse("2026-10-27T00:00:00Z")
}
