package functions

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/supatype/server/internal/config"
)

func TestRequireServiceRole(t *testing.T) {
	tests := []struct {
		name           string
		mode           string
		serviceKey     string
		authHeader     string
		wantStatus     int
		wantNextCalled bool
	}{
		// Dev mode used to wave every request through. A function deployed to a
		// dev stack could then reach the control plane through the gateway,
		// which presents the control plane secret on the caller's behalf.
		{
			name:           "dev mode without a key denied",
			mode:           "dev",
			serviceKey:     "secret",
			authHeader:     "",
			wantStatus:     http.StatusUnauthorized,
			wantNextCalled: false,
		},
		{
			name:           "dev mode with the wrong key denied",
			mode:           "dev",
			serviceKey:     "secret",
			authHeader:     "Bearer wrong",
			wantStatus:     http.StatusUnauthorized,
			wantNextCalled: false,
		},
		{
			name:           "dev mode with no key configured denied",
			mode:           "dev",
			serviceKey:     "",
			authHeader:     "Bearer anything",
			wantStatus:     http.StatusUnauthorized,
			wantNextCalled: false,
		},
		{
			name:           "dev mode with the correct key allowed",
			mode:           "dev",
			serviceKey:     "secret",
			authHeader:     "Bearer secret",
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
		{
			name:           "missing key denied in non-dev",
			mode:           "managed",
			serviceKey:     "",
			authHeader:     "",
			wantStatus:     http.StatusUnauthorized,
			wantNextCalled: false,
		},
		{
			name:           "malformed bearer header denied",
			mode:           "managed",
			serviceKey:     "secret",
			authHeader:     "Token secret",
			wantStatus:     http.StatusUnauthorized,
			wantNextCalled: false,
		},
		{
			name:           "wrong key denied",
			mode:           "managed",
			serviceKey:     "secret",
			authHeader:     "Bearer wrong",
			wantStatus:     http.StatusUnauthorized,
			wantNextCalled: false,
		},
		{
			name:           "correct key allowed",
			mode:           "managed",
			serviceKey:     "secret",
			authHeader:     "Bearer secret",
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Mode: tc.mode, ServiceRoleKey: tc.serviceKey}

			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/functions/v1/list", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()

			requireServiceRole(cfg, next).ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rr.Code)
			}
			if nextCalled != tc.wantNextCalled {
				t.Fatalf("expected next called=%v, got %v", tc.wantNextCalled, nextCalled)
			}
		})
	}
}
