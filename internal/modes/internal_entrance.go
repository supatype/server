package modes

import (
	"context"
	"net"
	"net/http"
	"time"
)

// TenantHeader names the tenant a request is for, and TenantSigHeader carries Kong's signature of it.
const (
	TenantHeader    = "X-Supatype-Tenant"
	TenantSigHeader = "X-Supatype-Tenant-Sig"
)

// internalEntranceKey marks a connection accepted by the internal entrance's own listener. Only that
// server's ConnContext sets it, so the trust belongs to the listener: no handler wrapping, and nothing
// a caller sends, can turn it on for a request that came in by the public port.
type internalEntranceKey struct{}

// NewInternalEntranceServer is the way in for a project's own pods, such as its functions and hooks,
// which call their project's API from inside the cluster. They cannot sign as Kong does, and going out
// to the public address and back in would cost a round trip through the edge for every call.
//
// It serves one project, managedRef, whatever the caller sends: the tenant header is set to it and any
// signature header is dropped. TenantMiddlewareKeys then lets the request through without a signature,
// but only on the tenant gateway of that same project. Everything else in front of the API still
// applies: metering wraps the whole handler, and the API key check comes after the tenant check.
//
// The network policy must keep addr reachable from the project's own namespace only.
func NewInternalEntranceServer(addr, managedRef string, next http.Handler, base context.Context) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           asOwnTenant(managedRef, next),
		ReadHeaderTimeout: 2 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, internalEntranceKey{}, managedRef)
		},
	}
}

// asOwnTenant makes every request name managedRef and carry no signature, so a caller on the internal
// entrance cannot choose the project it acts as.
func asOwnTenant(managedRef string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set(TenantHeader, managedRef)
		r.Header.Del(TenantSigHeader)
		next.ServeHTTP(w, r)
	})
}

// fromOwnInternalEntrance reports whether r came in by the internal entrance of the project this
// gateway serves. A gateway with no project of its own, the platform server, trusts no entrance.
func fromOwnInternalEntrance(r *http.Request, managedRef string) bool {
	if managedRef == "" {
		return false
	}
	ref, ok := r.Context().Value(internalEntranceKey{}).(string)
	return ok && ref == managedRef
}
