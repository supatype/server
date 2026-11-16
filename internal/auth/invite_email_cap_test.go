package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/ratelimit"
)

// invite sends one /admin/invite request for email as the super admin.
func (ts *InviteTestSuite) invite(email string) int {
	var body bytes.Buffer
	require.NoError(ts.T(), json.NewEncoder(&body).Encode(map[string]interface{}{"email": email}))
	req := httptest.NewRequest(http.MethodPost, "http://localhost/invite", &body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ts.token))
	w := httptest.NewRecorder()
	ts.API.handler.ServeHTTP(w, req)
	return w.Code
}

// The per-environment email cap counts every send, whether or not the project
// asks for confirmations: a tenant that turns them off still sends invitations,
// recovery and magic links through the platform's sender.
func (ts *InviteTestSuite) TestInviteIsCappedWhetherOrNotConfirmationsAreOn() {
	for _, autoconfirm := range []bool{true, false} {
		name := fmt.Sprintf("autoconfirm=%v", autoconfirm)
		ts.Run(name, func() {
			previousLimiter, previousAutoconfirm := ts.API.limiterOpts.Email, ts.Config.Mailer.Autoconfirm
			defer func() {
				ts.API.limiterOpts.Email, ts.Config.Mailer.Autoconfirm = previousLimiter, previousAutoconfirm
			}()
			ts.Config.Mailer.Autoconfirm = autoconfirm
			ts.API.limiterOpts.Email = ratelimit.New(conf.Rate{Events: 2, OverTime: time.Hour})

			ts.Equal(http.StatusOK, ts.invite(fmt.Sprintf("cap-a-%v@example.com", autoconfirm)))
			ts.Equal(http.StatusOK, ts.invite(fmt.Sprintf("cap-b-%v@example.com", autoconfirm)))
			ts.Equal(http.StatusTooManyRequests, ts.invite(fmt.Sprintf("cap-c-%v@example.com", autoconfirm)))
		})
	}
}
