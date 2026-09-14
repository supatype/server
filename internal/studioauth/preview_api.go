package studioauth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/supatype/server/internal/utilities"
)

// PreviewAPI mints, lists and revokes links that let someone read a draft.
//
//	POST /admin/preview-links            mint one   {"model","recordId","scope","ttl"}
//	GET  /admin/preview-links?model=&recordId=      the live links for one record
//	POST /admin/preview-links/{id}/revoke           withdraw that one link
//	POST /admin/preview-links/revoke                withdraw every link at once
//
// # Why a link rather than a grant
//
// A draft is visible to its creator and to the project's Studio roles. None of
// that helps the person a draft most needs showing to: a client, a lawyer, an
// editor's editor, who has no account and should not be given one to read one
// post. A short-lived link is the whole of their credential.
//
// # What is handed out, and what the database sees
//
// The link is an id and a secret. Only the id and a hash of the secret are
// stored, so the table is not a database of secrets: it can be read end to end
// without opening a draft. The bearer exchanges the code at `/preview-links/
// resolve` for a JWT that lives about a minute, and it is that JWT which
// PostgREST verifies and the generated policies read from
// `request.jwt.claims`. So authorization still happens exactly where it did, and
// there is no second verification path to keep in step.
//
// This replaced handing out the JWT itself. That worked, but the token *was* the
// link, which meant a 400-character URL and, worse, nothing to revoke: a signed
// token cannot be recalled, so withdrawing one link meant bumping a counter that
// stranded every other link in the project.
//
// **No `sub`, in the exchanged token.** `auth.uid()` is then null and the bearer
// inherits nothing from looking logged in: the preview claim is the only thing
// that can match, and it matches one record unless the link is project-scoped. A
// token with a subject would make its holder that person for its lifetime.
func PreviewAPI(c Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.Trim(strings.TrimPrefix(req.URL.Path, "/admin/preview-links"), "/")

		if req.Method == http.MethodGet && path == "" {
			listPreviewLinksForRecord(w, req, c)
			return
		}
		if req.Method != http.MethodPost {
			utilities.WriteJSON(w, http.StatusMethodNotAllowed, errorBody("method not allowed"))
			return
		}

		switch {
		case path == "":
			mintPreviewLink(w, req, c)
		case path == "revoke":
			revokePreviewLinks(w, req, c)
		case strings.HasSuffix(path, "/revoke"):
			revokeOnePreviewLink(w, req, c, strings.TrimSuffix(path, "/revoke"))
		default:
			utilities.WriteJSON(w, http.StatusNotFound, errorBody("not found"))
		}
	})
}

// PreviewScope is what one link covers.
type PreviewScope string

const (
	// PreviewScopeRecord covers one record of one model.
	PreviewScopeRecord PreviewScope = "record"
	// PreviewScopeProject covers every unpublished draft in the project.
	PreviewScopeProject PreviewScope = "project"
)

type previewRequest struct {
	Model    string       `json:"model"`
	RecordID string       `json:"recordId"`
	Scope    PreviewScope `json:"scope"`
	// TTL in seconds. Zero means the project's default; anything above the
	// ceiling for this scope is refused rather than quietly clamped, because a
	// caller who asked for a week and got fifteen minutes should be told.
	TTL int `json:"ttl"`
}

type previewResponse struct {
	// The credential, shown once. Studio does not keep it: keeping it would be keeping a secret it
	// has no reason to hold, and the id below is enough to list and revoke the link later.
	Code      string `json:"code"`
	ID        string `json:"id"`
	ExpiresAt string `json:"expiresAt"`
	Scope     string `json:"scope"`
	Model     string `json:"model,omitempty"`
	RecordID  string `json:"recordId,omitempty"`
}

func mintPreviewLink(w http.ResponseWriter, req *http.Request, c Config) {
	var body previewRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8<<10)).Decode(&body); err != nil {
		utilities.WriteJSON(w, http.StatusBadRequest, errorBody("could not read the request body"))
		return
	}

	settings, err := PublishingSettingsFromConfigFile(c.AdminConfigPath)
	if err != nil {
		utilities.WriteJSON(w, http.StatusNotImplemented, errorBody(
			"this project has no versioned models, so there are no drafts to preview"))
		return
	}

	scope := body.Scope
	if scope == "" {
		scope = PreviewScopeRecord
	}
	if scope != PreviewScopeRecord && scope != PreviewScopeProject {
		utilities.WriteJSON(w, http.StatusBadRequest, errorBody(
			"scope must be \"record\" or \"project\""))
		return
	}

	_, sub, ok := requirePreviewMinter(w, req, c, scope, settings)
	if !ok {
		return
	}

	if scope == PreviewScopeRecord {
		if strings.TrimSpace(body.Model) == "" || strings.TrimSpace(body.RecordID) == "" {
			utilities.WriteJSON(w, http.StatusBadRequest, errorBody(
				"a record link needs a model and a recordId"))
			return
		}
	}

	ttl, err := resolveTTL(body.TTL, scope, settings)
	if err != nil {
		utilities.WriteJSON(w, http.StatusBadRequest, errorBody(err.Error()))
		return
	}

	epoch, err := previewEpoch(req.Context(), c)
	if err != nil {
		// Fail closed. Minting without the epoch would produce a link that no
		// policy can match, which reads to its holder as "the draft does not
		// exist" rather than as an outage.
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody(
			"could not read the preview revocation counter"))
		return
	}

	expires := time.Now().Add(time.Duration(ttl) * time.Second)

	id, secret, code, err := newPreviewCode()
	if err != nil {
		utilities.WriteJSON(w, http.StatusInternalServerError, errorBody("could not mint a link"))
		return
	}

	link := PreviewLink{ID: id, Scope: string(scope), CreatedBy: sub}
	if scope == PreviewScopeRecord {
		link.Model = body.Model
		link.RecordID = body.RecordID
	}
	if err := storePreviewLink(req.Context(), c, link, hashPreviewSecret(secret), epoch, expires); err != nil {
		// Fail loudly rather than returning a code nothing can resolve, which would read to its
		// holder as a draft that does not exist.
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody("could not record the link"))
		return
	}

	response := previewResponse{
		Code:      code,
		ID:        id,
		ExpiresAt: expires.UTC().Format(time.RFC3339),
		Scope:     string(scope),
	}
	if scope == PreviewScopeRecord {
		response.Model = body.Model
		response.RecordID = body.RecordID
	}
	utilities.WriteJSON(w, http.StatusOK, response)
}

// requirePreviewMinter admits a caller who may hand this link out.
//
// **A record link needs only draft visibility**: if you can read the draft in
// Studio, you can show it to someone. **A project link is `admin` or `developer`
// only**, and the reason is not that an editor cannot see every draft — since the
// visibility default widened, they can. It is that handing a URL to someone with
// no account is a distinct authority from editing content, and one project link
// covers every unpublished draft there is.
func requirePreviewMinter(
	w http.ResponseWriter,
	req *http.Request,
	c Config,
	scope PreviewScope,
	settings PublishingSettings,
) (string, string, bool) {
	if c.DevBypass() {
		return "dev-bypass", "", true
	}

	result := ResolveAccess(req, c)
	if !result.Allowed {
		status := http.StatusForbidden
		if result.Message == "Authentication required" {
			status = http.StatusUnauthorized
		}
		utilities.WriteJSON(w, status, errorBody(result.Message))
		return "", "", false
	}

	if scope == PreviewScopeProject {
		if !settings.AllowProjectScope {
			utilities.WriteJSON(w, http.StatusForbidden, errorBody(
				"this project does not allow project-wide preview links"))
			return "", "", false
		}
		// The elevated roles, read from the same capability set every other
		// Studio decision uses rather than from a list of role names kept here.
		perms := legacyAdminPermissions()
		if result.Permissions != nil {
			perms = *result.Permissions
		}
		if !perms.SchemaView {
			utilities.WriteJSON(w, http.StatusForbidden, errorBody(
				"Studio role \""+result.Role+"\" cannot mint a project-wide preview link. "+
					"Mint a link for the record instead."))
			return "", "", false
		}
		return result.Role, result.Sub, true
	}

	if !settings.RoleSeesDrafts(result.Role) {
		utilities.WriteJSON(w, http.StatusForbidden, errorBody(
			"Studio role \""+result.Role+"\" cannot see drafts, so it cannot share one"))
		return "", "", false
	}
	return result.Role, result.Sub, true
}

// resolveTTL applies the project's default and refuses anything past the ceiling.
//
// Refuses rather than clamps. A caller who asked for a week and silently received
// fifteen minutes would hand out a link they believed in, and find out when the
// person they sent it to said it had stopped working.
func resolveTTL(requested int, scope PreviewScope, settings PublishingSettings) (int, error) {
	max := settings.MaxRecordTTL
	if scope == PreviewScopeProject {
		max = settings.MaxProjectTTL
	}
	if requested <= 0 {
		if settings.DefaultTTL > max {
			return max, nil
		}
		return settings.DefaultTTL, nil
	}
	if requested > max {
		return 0, &previewError{
			"a " + string(scope) + " link may live at most " +
				time.Duration(max*int(time.Second)).String()}
	}
	return requested, nil
}

type previewError struct{ message string }

func (e *previewError) Error() string { return e.message }

type previewClaims struct {
	Scope    PreviewScope
	Model    string
	RecordID string
	Epoch    int
	IssuedBy string
	Expires  time.Time
}

// signPreviewToken builds the JWT the generated policies read.
//
// `role: authenticated` is the role that holds `USAGE` on the `draft` schema;
// `anon` never does, so a request with no token cannot reach it at all. There is
// deliberately no `sub`.
func signPreviewToken(secret string, claims previewClaims) (string, error) {
	preview := map[string]interface{}{
		"scope": string(claims.Scope),
		"epoch": claims.Epoch,
	}
	if claims.Scope == PreviewScopeRecord {
		preview["model"] = claims.Model
		preview["record_id"] = claims.RecordID
	}

	payload := jwt.MapClaims{
		"role":    "authenticated",
		"preview": preview,
		"exp":     claims.Expires.Unix(),
		"iat":     time.Now().Unix(),
	}
	// Who handed the link out, so a leak is attributable. Not `sub`: this must
	// never make the bearer that person.
	if claims.IssuedBy != "" {
		payload["iss_by"] = claims.IssuedBy
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, payload).SignedString([]byte(secret))
}

// previewEpoch reads the revocation counter every link is stamped with.
func previewEpoch(ctx context.Context, c Config) (int, error) {
	if c.Resources == nil {
		return 0, errNoDatabase
	}
	pool, err := c.Resources.AdminPool()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var epoch int
	err = pool.QueryRow(ctx,
		`SELECT preview_epoch FROM _supatype.publishing_settings WHERE singleton`).Scan(&epoch)
	if err != nil {
		return 0, err
	}
	return epoch, nil
}

// revokePreviewLinks withdraws every outstanding link by bumping the epoch.
//
// All of them, not one: a signed token carries no identity a server could revoke
// individually without keeping a list of every link ever minted, which is a
// database of secrets to protect in exchange for a finer control nobody asked
// for. Outstanding links are short-lived by construction, so re-sharing after a
// revocation costs a click.
func revokePreviewLinks(w http.ResponseWriter, req *http.Request, c Config) {
	settings, err := PublishingSettingsFromConfigFile(c.AdminConfigPath)
	if err != nil {
		utilities.WriteJSON(w, http.StatusNotImplemented, errorBody(
			"this project has no versioned models, so there are no preview links"))
		return
	}
	// Revoking is an elevated act for the same reason minting a project link is:
	// it changes what everyone else's outstanding links do.
	if _, _, ok := requirePreviewMinter(w, req, c, PreviewScopeProject, PublishingSettings{
		DefaultTTL:        settings.DefaultTTL,
		MaxRecordTTL:      settings.MaxRecordTTL,
		MaxProjectTTL:     settings.MaxProjectTTL,
		AllowProjectScope: true,
		DraftVisibility:   settings.DraftVisibility,
	}); !ok {
		return
	}

	if c.Resources == nil {
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody("no database"))
		return
	}
	pool, err := c.Resources.AdminPool()
	if err != nil {
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody("no database"))
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	var epoch int
	err = pool.QueryRow(ctx,
		`UPDATE _supatype.publishing_settings
            SET preview_epoch = preview_epoch + 1, updated_at = now()
          WHERE singleton
      RETURNING preview_epoch`).Scan(&epoch)
	if err != nil {
		utilities.WriteJSON(w, http.StatusServiceUnavailable, errorBody(
			"could not revoke preview links"))
		return
	}

	utilities.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"revoked": true,
		"epoch":   epoch,
	})
}
