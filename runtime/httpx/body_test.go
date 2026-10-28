package httpx

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
)

func TestBody(t *testing.T) {
	cases := []struct {
		name    string
		body    io.Reader
		length  int64
		present bool
		want    string
	}{
		{name: "nil", body: nil, length: 0},
		{name: "no body", body: http.NoBody, length: 0},
		{name: "empty", body: strings.NewReader(""), length: 0},
		// A chunked body: no length, and nothing in it.
		{name: "empty chunked", body: strings.NewReader(""), length: -1},
		{
			name: "chunked", body: strings.NewReader(`{"a":1}`), length: -1,
			present: true, want: `{"a":1}`,
		},
		// http.NewRequest reports 0 for a reader whose length it cannot
		// see, so a length of 0 alone does not mean there is no body.
		{
			name: "unknown length", body: strings.NewReader(`{}`), length: 0,
			present: true, want: `{}`,
		},
		{
			name: "whitespace", body: strings.NewReader(" \n"), length: 2,
			present: true, want: " \n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/x", nil)
			if c.body != nil {
				r.Body = io.NopCloser(c.body)
			} else {
				r.Body = nil
			}
			r.ContentLength = c.length
			body, ok := Body(r)
			if ok != c.present {
				t.Fatalf("present = %v, want %v", ok, c.present)
			}
			if !ok {
				return
			}
			got, err := io.ReadAll(body)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Errorf("body = %q, want %q", got, c.want)
			}
		})
	}
}

// A body that cannot be read is not one without a body: the error reaches
// whoever decodes it.
func TestBodyKeepsReadErrors(t *testing.T) {
	boom := errors.New("boom")
	r := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/x", nil)
	r.Body = io.NopCloser(iotest.ErrReader(boom))
	r.ContentLength = -1
	body, ok := Body(r)
	if !ok {
		t.Fatal("a failing body was reported absent")
	}
	if _, err := io.ReadAll(body); !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
}
