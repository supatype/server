package studioauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/supatype/server/internal/studioctx"
)

// maxSignedURLSeconds is the longest a signed URL made through Studio lives.
// A request acting as Studio or elevated signs as the service role, so the link
// it makes reaches the object whatever the app's own rules say, and anyone the
// link is passed to keeps that reach until it expires.
const maxSignedURLSeconds = studioctx.MaxSignedURLSeconds

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
	if errors.Is(err, errLookAlikeKey) {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
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

// signingBodyKeys are the body fields the storage signing endpoints read:
// `expiresIn` on every one, `paths` on the bulk form and `transform` on a
// download. The forwarded body is rebuilt from these alone.
var signingBodyKeys = []string{"expiresIn", "paths", "transform"}

// errLookAlikeKey refuses a key that matches an accepted one only by case.
var errLookAlikeKey = errors.New("signing request body key differs from an accepted key only by case")

// clampedSigningBody is the signing body rebuilt from signingBodyKeys, with
// its expiry capped.
func clampedSigningBody(r io.Reader) ([]byte, error) {
	fields, err := readSigningFields(r)
	if err != nil {
		return nil, err
	}
	kept, err := acceptedSigningFields(fields)
	if err != nil {
		return nil, err
	}
	expiry, err := cappedExpiry(kept["expiresIn"])
	if err != nil {
		return nil, err
	}
	kept["expiresIn"] = json.RawMessage(strconv.Itoa(expiry))
	return json.Marshal(kept)
}

// readSigningFields reads the signing body as a JSON object; an empty body is
// an empty object.
func readSigningFields(r io.Reader) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if r == nil {
		return fields, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r, signingBodyLimit))
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return fields, err
	}
	err = json.Unmarshal(raw, &fields)
	return fields, err
}

// acceptedSigningFields keeps the fields named exactly as an accepted key and
// drops the rest. A key that case-folds to an accepted one without being it,
// `expiresin` or `expireſIn` (U+017F), is refused: a decoder that matches keys
// without regard to case, as Go's struct decoding does with Unicode simple
// folding, would read it as the expiry the cap never saw.
func acceptedSigningFields(fields map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	kept := map[string]json.RawMessage{}
	for name, value := range fields {
		for _, key := range signingBodyKeys {
			if name == key {
				kept[name] = value
			} else if strings.EqualFold(name, key) {
				return nil, errLookAlikeKey
			}
		}
	}
	return kept, nil
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
