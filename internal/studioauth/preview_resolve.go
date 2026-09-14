package studioauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/supatype/server/internal/utilities"
)

// previewExchangeTTL is how long the token a code buys stays good.
//
// Deliberately far shorter than the link itself. The link may live a week; this lives a minute, and
// the holder exchanges again on the next page load. That gap is what makes revocation mean
// something: a revoked link stops resolving immediately, and the longest anyone can keep reading on
// an already-exchanged token is this.
//
// A preview page is rendered per request, so re-exchanging costs one round trip against a single
// indexed lookup. Longer would buy nothing and would only widen the window.
const previewExchangeTTL = time.Minute

// PreviewResolveAPI exchanges a preview code for a short-lived token.
//
//	POST /preview-links/resolve   {"code": "pl_….…"}
//
// **Unauthenticated on purpose.** The whole point of a preview link is that its holder has no
// account: requiring one would defeat the feature. The code is the credential, and it is checked
// against a stored hash in constant time.
//
// Guessing is not a practical attack: an id is 64 bits and a secret 192, both from the system
// CSPRNG, and a wrong secret is indistinguishable from a wrong id in the response.
func PreviewResolveAPI(c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
			return
		}

		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<10)).Decode(&body); err != nil {
			utilities.WriteJSON(w, http.StatusBadRequest, errorBody("could not read the request body"))
			return
		}
		if strings.TrimSpace(body.Code) == "" {
			utilities.WriteJSON(w, http.StatusBadRequest, errorBody("a preview code is required"))
			return
		}

		link, epoch, linkExpires, err := resolvePreviewLink(req.Context(), c, body.Code)
		if err != nil {
			if errors.Is(err, errNoPreviewDatabase) {
				utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody("no database"))
				return
			}
			// One answer for every reason a code does not work. Distinguishing "revoked" from
			// "never existed" would confirm that a draft is there to be found.
			utilities.WriteJSON(w, http.StatusNotFound, errorBody(
				"this preview link is not valid. It may have expired, or been revoked."))
			return
		}

		// Never past the link's own expiry: a link with thirty seconds left must not buy a minute.
		expires := time.Now().Add(previewExchangeTTL)
		if linkExpires.Before(expires) {
			expires = linkExpires
		}

		token, err := signPreviewToken(c.JWTSecret, previewClaims{
			Scope:    PreviewScope(link.Scope),
			Model:    link.Model,
			RecordID: link.RecordID,
			Epoch:    epoch,
			Expires:  expires,
		})
		if err != nil {
			utilities.WriteJSON(w, http.StatusInternalServerError, errorBody("could not sign the token"))
			return
		}

		utilities.WriteJSON(w, http.StatusOK, map[string]any{
			"token":     token,
			"expiresAt": expires.UTC().Format(time.RFC3339),
			"scope":     link.Scope,
			// So a caller can stop re-exchanging once the link itself is done.
			"linkExpiresAt": linkExpires.UTC().Format(time.RFC3339),
		})
	})
}

// listPreviewLinksForRecord answers what is currently outstanding for one record.
//
// Ids and times only. The codes are not here to be re-read: they were shown once at mint, and this
// exists so that someone can see they shared something and take it back, not so they can recover a
// credential they did not keep.
func listPreviewLinksForRecord(w http.ResponseWriter, req *http.Request, c Config) {
	settings, err := PublishingSettingsFromConfigFile(c.AdminConfigPath)
	if err != nil {
		utilities.WriteJSON(w, http.StatusNotImplemented, errorBody(
			"this project has no versioned models, so there are no preview links"))
		return
	}
	if _, _, ok := requirePreviewMinter(w, req, c, PreviewScopeRecord, settings); !ok {
		return
	}

	model := strings.TrimSpace(req.URL.Query().Get("model"))
	recordID := strings.TrimSpace(req.URL.Query().Get("recordId"))
	if model == "" || recordID == "" {
		utilities.WriteJSON(w, http.StatusBadRequest, errorBody(
			"a model and a recordId are required"))
		return
	}

	links, err := listPreviewLinks(req.Context(), c, model, recordID)
	if err != nil {
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody(
			"could not read the preview links"))
		return
	}
	utilities.WriteJSON(w, http.StatusOK, map[string]any{"links": links})
}

// revokeOnePreviewLink withdraws a single link.
//
// Whoever may share a draft may unshare it, so this asks the same of a caller as minting does
// rather than reserving it for an elevated role. Withdrawing access is the safe direction: the
// failure mode of being too strict here is a link nobody can take back.
func revokeOnePreviewLink(w http.ResponseWriter, req *http.Request, c Config, id string) {
	settings, err := PublishingSettingsFromConfigFile(c.AdminConfigPath)
	if err != nil {
		utilities.WriteJSON(w, http.StatusNotImplemented, errorBody(
			"this project has no versioned models, so there are no preview links"))
		return
	}
	if _, _, ok := requirePreviewMinter(w, req, c, PreviewScopeRecord, settings); !ok {
		return
	}

	if !strings.HasPrefix(id, previewIDPrefix) {
		utilities.WriteJSON(w, http.StatusBadRequest, errorBody("that is not a preview link id"))
		return
	}

	if err := revokePreviewLink(req.Context(), c, id); err != nil {
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody(
			"could not revoke the preview link"))
		return
	}
	// Idempotent, and says so: revoking an already dead link is the caller's intent satisfied, not
	// a failure to report.
	utilities.WriteJSON(w, http.StatusOK, map[string]any{"revoked": true, "id": id})
}
