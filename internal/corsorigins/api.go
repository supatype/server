package corsorigins

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/utilities"
)

// Authorize admits a caller, writing the refusal itself when it does not.
type Authorize func(w http.ResponseWriter, req *http.Request, mutating bool) bool

// APIConfig is what the origins API needs from the gateway.
type APIConfig struct {
	Policy *Policy
	// Mode is the deployment mode; dev mode allows every origin whatever the list says.
	Mode string
	// Extra is the request's host-configured origins (Cloud's tenant manifest), or nil.
	Extra     func(*http.Request) []string
	Authorize Authorize
}

// Response is what GET, PUT and DELETE answer: what is in force, where each part comes from, and
// whether this deployment applies it at all.
type Response struct {
	Origins    []string   `json:"origins"`
	Source     string     `json:"source"`
	Default    []string   `json:"default"`
	SiteOrigin string     `json:"siteOrigin"`
	Configured []string   `json:"configured"`
	Platform   []string   `json:"platform"`
	Saved      []string   `json:"saved"`
	UpdatedAt  *time.Time `json:"updatedAt"`
	Mode       string     `json:"mode"`
	// Enforced is false in dev mode, which reflects every origin.
	Enforced bool `json:"enforced"`
	// Editable is false when the deployment has no database to save a list in.
	Editable bool `json:"editable"`
}

type saveBody struct {
	Origins []string `json:"origins"`
}

// API serves GET (what is in force), PUT {"origins": [...]} (save a list that replaces the
// default) and DELETE (reset to the default).
func API(c APIConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mutating := req.Method == http.MethodPut || req.Method == http.MethodDelete
		if !mutating && req.Method != http.MethodGet {
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, apiError("method not allowed"))
			return
		}
		if !c.Authorize(w, req, mutating) {
			return
		}
		if err := c.apply(req); err != nil {
			writeFailure(w, err)
			return
		}
		utilities.WriteJSON(w, http.StatusOK, c.describe(req))
	})
}

// apply carries out a PUT or DELETE; a GET changes nothing.
func (c APIConfig) apply(req *http.Request) error {
	switch req.Method {
	case http.MethodPut:
		var body saveBody
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.Origins == nil {
			return badRequest{"body must be {\"origins\": [...]}"}
		}
		_, err := c.Policy.Save(req.Context(), body.Origins)
		return err
	case http.MethodDelete:
		return c.Policy.Reset(req.Context())
	}
	return nil
}

func (c APIConfig) describe(req *http.Request) Response {
	var extra []string
	if c.Extra != nil {
		extra = c.Extra(req)
	}
	snap := c.Policy.InForce(req.Context(), extra)
	out := Response{
		Origins:    snap.Origins,
		Source:     snap.Source,
		Default:    snap.Default,
		SiteOrigin: c.Policy.SiteOrigin(),
		Configured: Union(c.Policy.ConfiguredOrigins(), lenient(extra)),
		Platform:   c.Policy.PlatformOrigins(),
		Saved:      nil,
		Mode:       c.Mode,
		Enforced:   c.Mode != "dev",
		Editable:   c.Policy.store != nil,
	}
	if snap.Saved != nil {
		out.Saved = Union(snap.Saved.Origins)
		at := snap.Saved.UpdatedAt
		out.UpdatedAt = &at
	}
	return out
}

type badRequest struct{ message string }

func (b badRequest) Error() string { return b.message }

func writeFailure(w http.ResponseWriter, err error) {
	var bad badRequest
	switch {
	case errors.As(err, &bad), errors.Is(err, ErrInvalidOrigin):
		utilities.WriteJSON(w, http.StatusBadRequest, apiError(err.Error()))
	case errors.Is(err, ErrNoStore):
		utilities.WriteJSON(w, http.StatusConflict, apiError(err.Error()))
	default:
		logrus.WithError(err).Error("cors: could not change the saved origins")
		utilities.WriteJSON(w, http.StatusServiceUnavailable, apiError("could not change the saved origins"))
	}
}

func apiError(message string) map[string]string {
	return map[string]string{"error": "cors_origins", "message": message}
}
