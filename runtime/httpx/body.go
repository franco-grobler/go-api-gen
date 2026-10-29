package httpx

import (
	"bufio"
	"errors"
	"io"
	"net/http"
)

// Body returns the request body to decode, and false when the request has
// none.
//
// A body is absent when it holds no bytes at all, however that is signalled:
// a Content-Length of 0, a chunked body whose first read is the end, or, for
// a request built by hand rather than read by an http.Server, a nil Body.
// Content-Length alone cannot decide it: an http.Server never leaves Body
// nil, a chunked request reports a length of -1, and http.NewRequest with an
// unknown kind of reader reports 0 for a body that has bytes. So the body is
// read up to its first byte, and the returned reader replays that byte.
//
// A body of only whitespace is present: it is not JSON, and decoding it
// fails, as it should. A read error other than the end of the body is left
// for the decoder to meet, so the request is not mistaken for one without a
// body.
func Body(r *http.Request) (io.Reader, bool) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, false
	}
	br := bufio.NewReader(r.Body)
	if _, err := br.Peek(1); errors.Is(err, io.EOF) {
		return nil, false
	}
	return br, true
}
