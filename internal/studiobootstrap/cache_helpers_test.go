package studiobootstrap

import "time"

// The two memoised classifications are package state, so a test has to be able
// to clear one and to age one.
//
// ResetIdentityScopeCache used to be exported from identity_scope.go with the
// comment "For tests", which put a test helper in the binary and on the
// package's public surface. Nothing outside this package called it.

func ResetIdentityScopeCache() {
	identityScope.Lock()
	defer identityScope.Unlock()
	identityScope.tables = nil
	identityScope.loaded = false
	identityScope.loadedAt = time.Time{}
}

func ResetFieldScopeCache() {
	fieldScope.Lock()
	defer fieldScope.Unlock()
	fieldScope.tables = nil
	fieldScope.loaded = false
	fieldScope.loadedAt = time.Time{}
}

// expireIdentityScope ages the memoised answer past its TTL without clearing
// it, which is what a stale-but-present cache looks like when the next load
// fails.
func expireIdentityScope() {
	identityScope.Lock()
	defer identityScope.Unlock()
	identityScope.loadedAt = time.Now().Add(-identityScopeTTL - time.Second)
}

func expireFieldScope() {
	fieldScope.Lock()
	defer fieldScope.Unlock()
	fieldScope.loadedAt = time.Now().Add(-fieldScopeTTL - time.Second)
}
