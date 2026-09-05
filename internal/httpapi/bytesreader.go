package httpapi

import (
	"bytes"
	"io"
)

// newBytesReader adapts a byte slice to what http.ServeContent needs.
//
// ServeContent wants an io.ReadSeeker so it can honour Range requests and set Content-Length;
// bytes.Reader provides exactly that, and this exists only to name the intent at the call
// site.
func newBytesReader(b []byte) io.ReadSeeker { return bytes.NewReader(b) }
