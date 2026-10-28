// Package date provides Date, a calendar day with no time of day and no
// time zone: what OpenAPI's format: date describes.
//
// A full-date in RFC 3339 is four digits of year, two of month and two of
// day, "2026-10-27". time.Time cannot stand in for one: it parses and prints
// a timestamp, so a date sent as a date would be rejected, and one written
// back would carry a time and a zone the contract never mentioned. Generated
// code maps format: date to Date, which reads and writes exactly that form,
// as text, in JSON and in a parameter alike.
package date

import (
	"cmp"
	"errors"
	"fmt"
	"time"
)

// layout is the full-date of RFC 3339 in time's notation.
const layout = "2006-01-02"

// Date is a day in the proleptic Gregorian calendar.
//
// The zero Date is not a valid date: it has no month and no day. It is what
// an absent value decodes to, and it refuses to be encoded, so a response
// that forgot to set a required date fails loudly rather than sending
// "0000-00-00".
type Date struct {
	Year  int        // 0 to 9999, as a full-date writes it in four digits
	Month time.Month // January is 1
	Day   int        // 1 to the length of Month in Year
}

// errFormat is the reason every malformed input is rejected with.
var errFormat = errors.New("want a full-date, YYYY-MM-DD")

// Of returns the day t falls on, in t's own location.
func Of(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// Today returns the current day in loc.
func Today(loc *time.Location) Date { return Of(time.Now().In(loc)) }

// Parse reads a full-date: exactly "YYYY-MM-DD", a real day of a real month.
// A timestamp, a date with a time or a zone, and a date with digits missing
// are all rejected, as are 2026-02-29 and 2026-13-01.
func Parse(s string) (Date, error) {
	// time.Parse alone would also accept a one-digit day or month in some
	// layouts and an out-of-range year in none; checking the shape first
	// keeps the rule exactly RFC 3339's.
	if len(s) != len(layout) || s[4] != '-' || s[7] != '-' {
		return Date{}, &ParseError{Value: s, Err: errFormat}
	}
	for i := range len(s) {
		if i != 4 && i != 7 && (s[i] < '0' || s[i] > '9') {
			return Date{}, &ParseError{Value: s, Err: errFormat}
		}
	}
	t, err := time.Parse(layout, s)
	if err != nil {
		// The shape is right, so what is wrong is the day: a month past 12,
		// a day the month does not have.
		return Date{}, &ParseError{Value: s, Err: errors.New("no such day")}
	}
	return Of(t), nil
}

// MustParse is Parse for a literal known to be valid; it panics otherwise.
func MustParse(s string) Date {
	d, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

// ParseError reports a string that is not a full-date.
type ParseError struct {
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("date: parsing %q: %v", e.Value, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// IsValid reports whether d is a real day that a full-date can write.
func (d Date) IsValid() bool {
	if d.Year < 0 || d.Year > 9999 || d.Month < time.January ||
		d.Month > time.December || d.Day < 1 {
		return false
	}
	return Of(d.In(time.UTC)) == d
}

// IsZero reports whether d is the zero Date.
func (d Date) IsZero() bool { return d == Date{} }

// String writes d as a full-date, "2026-10-27". An invalid Date is written
// with its fields as they are, which is for reading in a log, not for a
// client: MarshalText refuses it.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// In returns the first instant of d in loc.
func (d Date) In(loc *time.Location) time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// AddDays returns the day n days after d, or before it when n is negative.
func (d Date) AddDays(n int) Date {
	return Of(d.In(time.UTC).AddDate(0, 0, n))
}

// Compare returns -1 if d is before o, 0 if they are the same day, and +1 if
// d is after o.
func (d Date) Compare(o Date) int {
	if c := cmp.Compare(d.Year, o.Year); c != 0 {
		return c
	}
	if c := cmp.Compare(d.Month, o.Month); c != 0 {
		return c
	}
	return cmp.Compare(d.Day, o.Day)
}

// Before reports whether d is earlier than o.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

// After reports whether d is later than o.
func (d Date) After(o Date) bool { return d.Compare(o) > 0 }

// MarshalText writes d as a full-date. encoding/json uses it, so a Date in
// JSON is the string "2026-10-27". An invalid Date, the zero one included,
// is an error rather than a string no client could parse back.
func (d Date) MarshalText() ([]byte, error) {
	if !d.IsValid() {
		return nil, fmt.Errorf("date: %s is not a valid date", d)
	}
	return []byte(d.String()), nil
}

// UnmarshalText reads a full-date, as Parse does. encoding/json uses it for
// a JSON string, and generated servers for a parameter.
func (d *Date) UnmarshalText(b []byte) error {
	parsed, err := Parse(string(b))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
