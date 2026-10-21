package tokens

import (
	"fmt"
	"iter"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/supatype/server/internal/auth/apierrors"
)

// reservedClaimPrefix marks top-level claims that Supatype itself issues and
// trusts, such as supatype_studio, which grants Studio row access. A custom
// access token hook must never be able to mint them.
const reservedClaimPrefix = "supatype_"

// isReservedClaim reports whether a top-level claim name belongs to Supatype.
// The prefix match uses Unicode case folding, so look-alikes such as
// "SUPATYPE_studio" or a leading U+017F (long s) are reserved too. The prefix
// is taken rune by rune because a folded rune can be wider than its ASCII twin.
func isReservedClaim(name string) bool {
	end := 0
	for i := 0; i < utf8.RuneCountInString(reservedClaimPrefix); i++ {
		if end >= len(name) {
			return false
		}
		_, size := utf8.DecodeRuneInString(name[end:])
		end += size
	}
	return strings.EqualFold(name[:end], reservedClaimPrefix)
}

// rejectReservedClaims refuses hook output whose claim names include a reserved one.
func rejectReservedClaims(names iter.Seq[string]) error {
	var reserved []string
	for name := range names {
		if isReservedClaim(name) {
			reserved = append(reserved, fmt.Sprintf("%q", name))
		}
	}
	if len(reserved) == 0 {
		return nil
	}
	sort.Strings(reserved)
	return &apierrors.HTTPError{
		HTTPStatus: http.StatusInternalServerError,
		Message: fmt.Sprintf(
			"output claims must not set reserved %s* claims: %s",
			reservedClaimPrefix, strings.Join(reserved, ", ")),
	}
}
