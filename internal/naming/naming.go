// Package naming converts spec identifiers into Go identifiers.
//
// Every emitter routes through here so that a schema, a property, and an
// operation that share a name also share a Go spelling, and so that renaming
// rules stay in one auditable place.
package naming

import (
	"strconv"
	"strings"
	"unicode"
)

// initialisms are rendered in all caps, following the convention Go's own
// style checkers enforce: userId becomes UserID, not UserId.
var initialisms = map[string]string{
	"ACL": "ACL", "API": "API", "ASCII": "ASCII", "CPU": "CPU", "CSS": "CSS",
	"DNS": "DNS", "EOF": "EOF", "GUID": "GUID", "HTML": "HTML", "HTTP": "HTTP",
	"HTTPS": "HTTPS", "ID": "ID", "IP": "IP", "JSON": "JSON", "LHS": "LHS",
	"OK": "OK", "QPS": "QPS", "RAM": "RAM", "RHS": "RHS", "RPC": "RPC",
	"SLA":  "SLA",
	"SMTP": "SMTP", "SQL": "SQL", "SSH": "SSH", "TCP": "TCP", "TLS": "TLS",
	"TTL": "TTL", "UDP": "UDP", "UI": "UI", "UID": "UID", "UUID": "UUID",
	"URI": "URI", "URL": "URL", "UTF8": "UTF8", "VM": "VM", "XML": "XML",
	"XMPP": "XMPP", "XSRF": "XSRF", "XSS": "XSS",
}

// keywords may not be used as identifiers.
var keywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true,
	"for":  true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true,
	"return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

// predeclared identifiers are legal but shadowing them in generated code is a
// readability trap, so unexported names avoid them too.
var predeclared = map[string]bool{
	"any": true, "bool": true, "byte": true, "complex64": true,
	"complex128": true,
	"error":      true, "float32": true, "float64": true, "int": true,
	"int8":  true,
	"int16": true, "int32": true, "int64": true, "rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "true": true, "false": true, "iota": true, "nil": true,
	"append": true, "cap": true, "close": true, "copy": true, "delete": true,
	"len": true, "make": true, "new": true, "panic": true, "print": true,
	"println": true, "recover": true, "min": true, "max": true, "clear": true,
}

// Exported converts a spec name to an exported Go identifier.
//
//	user_id      -> UserID
//	api-key      -> APIKey
//	oneTwo       -> OneTwo
//	2fa_enabled  -> N2FAEnabled
func Exported(s string) string {
	words := Words(s)
	if len(words) == 0 {
		return "Unnamed"
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString(titleWord(w))
	}
	return sanitizeLeading(b.String())
}

// Unexported converts a spec name to an unexported Go identifier, suitable for
// a local variable or a private method.
func Unexported(s string) string {
	words := Words(s)
	if len(words) == 0 {
		return "unnamed"
	}
	var b strings.Builder
	// The first word stays lower case in full, so userID becomes userID and
	// ID alone becomes id rather than iD.
	b.WriteString(strings.ToLower(words[0]))
	for _, w := range words[1:] {
		b.WriteString(titleWord(w))
	}
	out := sanitizeLeading(b.String())
	out = strings.ToLower(out[:1]) + out[1:]
	if keywords[out] || predeclared[out] {
		return out + "_"
	}
	return out
}

// titleWord capitalizes one word, honouring the initialism list.
func titleWord(w string) string {
	if w == "" {
		return ""
	}
	if up, ok := initialisms[strings.ToUpper(w)]; ok {
		return up
	}
	r := []rune(strings.ToLower(w))
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// sanitizeLeading guarantees the identifier starts with a letter. A name
// beginning with a digit gets an N prefix rather than being silently dropped.
func sanitizeLeading(s string) string {
	if s == "" {
		return "Unnamed"
	}
	r := []rune(s)
	if unicode.IsDigit(r[0]) {
		return "N" + s
	}
	if !unicode.IsLetter(r[0]) && r[0] != '_' {
		return "X" + s
	}
	return s
}

// Words splits a spec name into words, handling snake_case, kebab-case,
// dotted.paths, spaces, and camelCase or PascalCase runs. Acronym runs stay
// together: parseHTTPResponse yields parse, HTTP, Response.
func Words(s string) []string {
	var words []string
	var cur []rune

	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}

	rs := []rune(s)
	for i, r := range rs {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// A lower-to-upper transition starts a new word: userId -> user,
			// Id.
			prevAlnum := i > 0 &&
				(unicode.IsLetter(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			if prevAlnum && unicode.IsUpper(r) && !unicode.IsUpper(rs[i-1]) {
				flush()
			}
			// The tail of an acronym run followed by a lower-case letter starts
			// a new word: HTTPResponse -> HTTP, Response.
			acronymTail := i > 0 && i+1 < len(rs) &&
				unicode.IsUpper(r) && unicode.IsUpper(rs[i-1]) &&
				unicode.IsLower(rs[i+1])
			if acronymTail {
				flush()
			}
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return words
}

// EnumConst builds the Go constant name for one value of a named enum type,
// for example EnumConst("Status", "in_progress") is StatusInProgress.
func EnumConst(typeName, value string) string {
	suffix := Exported(value)
	if suffix == "Unnamed" {
		suffix = "Empty"
	}
	return typeName + suffix
}

// Deduper hands out unique identifiers within one scope, such as the fields of
// a struct or the methods of an interface.
type Deduper struct {
	used map[string]bool
}

// NewDeduper returns a Deduper that already considers the given names taken.
func NewDeduper(reserved ...string) *Deduper {
	d := &Deduper{used: map[string]bool{}}
	for _, r := range reserved {
		d.used[r] = true
	}
	return d
}

// Taken reports whether name has already been handed out or reserved.
func (d *Deduper) Taken(name string) bool { return d.used[name] }

// Take returns name, or name with a numeric suffix if it is already taken.
func (d *Deduper) Take(name string) string {
	if !d.used[name] {
		d.used[name] = true
		return name
	}
	for i := 2; ; i++ {
		alt := name + strconv.Itoa(i)
		if !d.used[alt] {
			d.used[alt] = true
			return alt
		}
	}
}
