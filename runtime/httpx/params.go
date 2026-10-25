package httpx

import (
	"errors"
	"net/url"
	"slices"
	"strings"
)

// These read the raw strings of a parameter that is an array or an object,
// as OpenAPI's style and explode settings lay it out. A generated server
// converts each string to its declared type itself, so the shapes live here
// and the types stay in the generated code.

// errOddPairs is returned for an object whose keys and values do not pair up.
var errOddPairs = errors.New("keys and values do not pair up")

// Split separates a delimited value, as a non-exploded array is sent: "a,b,c"
// with sep ",". An empty value is no elements rather than one empty one.
func Split(raw, sep string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, sep)
}

// Pairs reads a non-exploded object, "k1,v1,k2,v2" with sep ",".
func Pairs(raw, sep string) (map[string]string, error) {
	parts := Split(raw, sep)
	if len(parts)%2 != 0 {
		return nil, errOddPairs
	}
	out := make(map[string]string, len(parts)/2)
	for i := 0; i < len(parts); i += 2 {
		out[parts[i]] = parts[i+1]
	}
	return out, nil
}

// KeyValues reads an exploded simple object, "k1=v1,k2=v2".
func KeyValues(raw string) (map[string]string, error) {
	parts := Split(raw, ",")
	out := make(map[string]string, len(parts))
	for _, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, errOddPairs
		}
		out[k] = v
	}
	return out, nil
}

// DeepObject collects the name[key]=value pairs of a deepObject parameter.
// A key sent more than once keeps its first value, as url.Values.Get does.
func DeepObject(q url.Values, name string) map[string]string {
	out := map[string]string{}
	prefix := name + "["
	for k, vs := range q {
		key, ok := strings.CutPrefix(k, prefix)
		if !ok || len(vs) == 0 {
			continue
		}
		if key, ok = strings.CutSuffix(key, "]"); ok && key != "" {
			out[key] = vs[0]
		}
	}
	return out
}

// Pick collects the given keys of an exploded form object whose properties
// are known: each property is a query parameter of its own.
func Pick(q url.Values, keys ...string) map[string]string {
	out := map[string]string{}
	for _, k := range keys {
		if vs := q[k]; len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// Rest collects the query parameters no declared parameter claims, which is
// what an exploded form object with no fixed properties is made of.
func Rest(q url.Values, declared ...string) map[string]string {
	out := map[string]string{}
	for k, vs := range q {
		if len(vs) == 0 || slices.Contains(declared, k) {
			continue
		}
		out[k] = vs[0]
	}
	return out
}
