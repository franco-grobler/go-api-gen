package httpx

import (
	"net/http"
	"strings"
)

// Credential extraction, for the security schemes a specification can declare.
//
// Where a credential lives is something the specification says, so pulling it
// out is the generator's business. What it means is not, which is why nothing
// here verifies anything.

// BearerToken returns the token from an Authorization header of the form
// "Bearer <token>". The scheme name is matched case-insensitively, as RFC 9110
// requires.
func BearerToken(r *http.Request) (string, bool) {
	return SchemeCredential(r, "Bearer")
}

// SchemeCredential returns the part of the Authorization header that follows a
// named authentication scheme.
func SchemeCredential(r *http.Request, scheme string) (string, bool) {
	header := r.Header.Get("Authorization")
	if len(header) <= len(scheme) ||
		!strings.EqualFold(header[:len(scheme)], scheme) ||
		header[len(scheme)] != ' ' {
		return "", false
	}
	value := strings.TrimSpace(header[len(scheme)+1:])
	return value, value != ""
}

// APIKey returns a credential carried by a header, a query parameter, or a
// cookie, which are the three places an apiKey scheme may name.
func APIKey(r *http.Request, in, name string) (string, bool) {
	var value string
	switch in {
	case "header":
		value = r.Header.Get(name)
	case "query":
		value = r.URL.Query().Get(name)
	case "cookie":
		c, err := r.Cookie(name)
		if err != nil {
			return "", false
		}
		value = c.Value
	default:
		return "", false
	}
	return value, value != ""
}
