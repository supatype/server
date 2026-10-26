package studiobootstrap

// The memoised classifications are package state, so a test has to be able to
// clear one and to age one.
//
// ResetIdentityScopeCache used to be exported from identity_scope.go with the
// comment "For tests", which put a test helper in the binary and on the
// package's public surface. Nothing outside this package called it.

func ResetIdentityScopeCache() { identityScope.reset() }

func ResetFieldScopeCache() { fieldScope.reset() }

func resetDraftTables() { draftTables.reset() }

// expireIdentityScope ages the memoised answer past its TTL without clearing
// it, which is what a stale-but-present cache looks like when the next load
// fails.
func expireIdentityScope() { identityScope.expire() }

func expireFieldScope() { fieldScope.expire() }
