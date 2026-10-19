package auth

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gofrs/uuid"
	"github.com/supatype/server/internal/auth/apierrors"
	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
)

// adminSession is one active session as the admin API shows it. models.Session
// hides its id from JSON, and a session is revoked by id.
type adminSession struct {
	ID        uuid.UUID  `json:"id"`
	UserID    uuid.UUID  `json:"user_id"`
	IP        *string    `json:"ip"`
	UserAgent *string    `json:"user_agent"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	NotAfter  *time.Time `json:"not_after"`
}

// adminSessions serves GET /admin/sessions: every session that has not run
// out, most recently used first, for Studio's Users view.
func (a *API) adminSessions(w http.ResponseWriter, r *http.Request) error {
	sessions, err := models.FindActiveSessions(a.db.WithContext(r.Context()), time.Now())
	return sendAdminSessions(w, sessions, err)
}

// adminUserSessions serves GET /admin/users/{user_id}/sessions: the sessions of that user that
// have not run out, most recently used first, in the same shape as GET /admin/sessions.
func (a *API) adminUserSessions(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sessions, err := models.FindActiveSessionsForUser(a.db.WithContext(ctx), getUser(ctx).ID, time.Now())
	return sendAdminSessions(w, sessions, err)
}

// sendAdminSessions answers {"sessions": [...]} with the sessions found, or the error finding them.
func sendAdminSessions(w http.ResponseWriter, sessions []*models.Session, err error) error {
	if err != nil {
		return apierrors.NewInternalServerError("Database error finding sessions").WithInternalError(err)
	}
	out := make([]adminSession, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, adminSession{
			ID: s.ID, UserID: s.UserID, IP: s.IP, UserAgent: s.UserAgent,
			CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, NotAfter: s.NotAfter,
		})
	}
	return sendJSON(w, http.StatusOK, map[string]interface{}{"sessions": out})
}

// adminSessionRevoke serves DELETE /admin/sessions/{session_id}: it signs that
// session out, as the user's own logout would, and records who did it.
func (a *API) adminSessionRevoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	sessionID, err := uuid.FromString(chi.URLParam(r, "session_id"))
	if err != nil {
		return apierrors.NewNotFoundError(apierrors.ErrorCodeValidationFailed, "session_id must be an UUID")
	}
	err = a.db.WithContext(ctx).Transaction(func(tx *storage.Connection) error {
		session, terr := models.FindSessionByID(tx, sessionID, false)
		if models.IsNotFoundError(terr) {
			return apierrors.NewNotFoundError(apierrors.ErrorCodeSessionNotFound, "Session not found")
		} else if terr != nil {
			return apierrors.NewInternalServerError("Database error finding session").WithInternalError(terr)
		}
		if terr := models.NewAuditLogEntry(a.config.AuditLog, r, tx, getAdminUser(ctx), models.TokenRevokedAction, "", map[string]interface{}{
			"session_id": session.ID,
			"user_id":    session.UserID,
		}); terr != nil {
			return terr
		}
		if terr := models.LogoutSession(tx, session.ID); terr != nil {
			return apierrors.NewInternalServerError("Database error revoking session").WithInternalError(terr)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return sendJSON(w, http.StatusOK, map[string]interface{}{})
}
