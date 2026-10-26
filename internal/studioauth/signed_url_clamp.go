package studioauth

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// maxSignedURLSeconds is the longest a signed URL made through Studio lives.
// A request acting as Studio or elevated signs as the service role, so the link
// it makes reaches the object whatever the app's own rules say, and anyone the
// link is passed to keeps that reach until it expires.
const maxSignedURLSeconds = 3600

// signingBodyLimit bounds the signing request read to rewrite it. A bulk
// signing request lists paths, so it is not tiny, but it is not a file either.
const signingBodyLimit = 1 << 20

// isSigningRequest reports whether a request makes a signed download or upload
// URL: `POST /storage/v1/object/sign/...` or `/storage/v1/object/upload/sign/...`.
func isSigningRequest(method, path string) bool {
	return method == http.MethodPost &&
		(under(path, "/storage/v1/object/sign") || under(path, "/storage/v1/object/upload/sign"))
}

// clampSignedURLExpiry caps `expiresIn` on a signing request at
// maxSignedURLSeconds, and sets it to that when the body names none, so an
// upstream default cannot outlive the cap. It reports false, having written
// the response, when the body is not a JSON object it can rewrite: forwarding a
// body it could not read would forward an uncapped expiry.
func clampSignedURLExpiry(w http.ResponseWriter, req2 *http.Request, path string) bool {
	if !isSigningRequest(req2.Method, path) {
		return true
	}
	body, err := clampedSigningBody(req2.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "signing request body must be a JSON object with a numeric expiresIn")
		return false
	}
	req2.Body = io.NopCloser(bytes.NewReader(body))
	req2.ContentLength = int64(len(body))
	req2.Header.Set("Content-Length", strconv.Itoa(len(body)))
	req2.Header.Set("Content-Type", "application/json")
	return true
}

// clampedSigningBody is the signing body with its expiry capped.
func clampedSigningBody(r io.Reader) ([]byte, error) {
	fields := map[string]json.RawMessage{}
	if r != nil {
		raw, err := io.ReadAll(io.LimitReader(r, signingBodyLimit))
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &fields); err != nil {
				return nil, err
			}
		}
	}
	expiry, err := cappedExpiry(fields["expiresIn"])
	if err != nil {
		return nil, err
	}
	fields["expiresIn"] = json.RawMessage(strconv.Itoa(expiry))
	return json.Marshal(fields)
}

// cappedExpiry reads `expiresIn`, as a number or a numeric string, and caps
// it. Absent, or not positive, becomes the cap itself.
func cappedExpiry(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return maxSignedURLSeconds, nil
	}
	text := strings.Trim(string(raw), `"`)
	seconds, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, err
	}
	if math.IsNaN(seconds) || seconds <= 0 || seconds > maxSignedURLSeconds {
		return maxSignedURLSeconds, nil
	}
	return int(seconds), nil
}
