package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
		{
			name:           "dev mode bypasses auth",
			mode:           "dev",
			serviceKey:     "",
			authHeader:     "",
			wantStatus:     http.StatusNoContent,
			wantNextCalled: true,
		},
		{
			name:           "missing key denied in non-dev",
			mode:           "managed",
			serviceKey:     "",
			authHeader:     "",
			wantStatus:     http.StatusForbidden,
			wantNextCalled: false,
		},
		{
			name:           "malformed bearer header denied",
			mode:           "managed",
			serviceKey:     "secret",
			authHeader:     "Token secret",
			wantStatus:     http.StatusForbidden,
			wantNextCalled: false,
		},
		{
			name:           "wrong key denied",
			mode:           "managed",
			serviceKey:     "secret",
			authHeader:     "Bearer wrong",
			wantStatus:     http.StatusForbidden,
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
			t.Setenv("SUPATYPE_MODE", tc.mode)
			t.Setenv("SUPATYPE_SERVICE_ROLE_KEY", tc.serviceKey)

			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/admin/v1/config/rest", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rr := httptest.NewRecorder()

			RequireServiceRole(next).ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rr.Code)
			}
			if nextCalled != tc.wantNextCalled {
				t.Fatalf("expected next called=%v, got %v", tc.wantNextCalled, nextCalled)
			}
		})
	}
}
