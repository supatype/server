// Package maskedfields tells a REST caller which columns in a response may have been
// masked.
//
// Postgres cannot omit a column: the result-set shape is fixed by the target list and
// identical for every row, so a column the caller may not read comes back as NULL — and on
// the wire that is indistinguishable from a value that is genuinely null. The masking is
// correct; the ambiguity is what this closes.
//
// Advisory only. Nothing here is an authorization input: the enforcement is the security
// labels and the planner rewrite in `supatype_mask`, and a caller who tampers with or
// ignores this header changes nothing about what they can read.
package maskedfields

import (
	"net/http"
	"strings"

	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/studiobootstrap"
)

// Header names the columns on the requested table that carry a read restriction.
//
// Each entry is `column=identity` or `column=row`:
//
//	X-Supatype-Masked-Fields: salary=row, ssn=identity
//
// `identity` means the verdict is the same for every row in the response, so a null in that
// column is explicable by masking for the whole result set. `row` means it varies row by
// row, so only some nulls are masked values — honest imprecision rather than a claim the
// header cannot support.
//
// Absent header means "not stated": either the table has no restricted columns or the
// classification could not be read. It never means "nothing is masked".
const Header = "X-Supatype-Masked-Fields"

// Middleware annotates `/rest/v1` responses with the masked-field header.
//
// Mounted outside the response cache deliberately, so the header is present on hits as well
// as misses. That is safe because the value is caller-independent — it describes the
// schema's restrictions, not one caller's verdicts — so a shared cache entry carrying it
// cannot disclose anything about the caller who happened to populate it.
func Middleware(resources *data.Resources, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if value, ok := headerFor(req, resources); ok {
			// Set before the proxy writes, since headers cannot be added afterwards.
			w.Header().Set(Header, value)
		}
		next.ServeHTTP(w, req)
	})
}

func headerFor(req *http.Request, resources *data.Resources) (string, bool) {
	table := tableFromPath(req.URL.Path)
	if table == "" {
		return "", false
	}

	tables, ok := studiobootstrap.MaskedFields(req.Context(), resources)
	if !ok {
		return "", false
	}

	masks := tables[table]
	if len(masks) == 0 {
		return "", false
	}

	entries := make([]string, 0, len(masks))
	for _, mask := range masks {
		scope := "identity"
		if mask.RowDependent {
			scope = "row"
		}
		entries = append(entries, mask.Column+"="+scope)
	}
	return strings.Join(entries, ", "), true
}

// tableFromPath reads the table from a path the `/rest/v1` prefix has already been stripped
// from, e.g. `/posts`.
//
// Only a bare table path is annotated. An RPC call (`/rpc/…`) returns whatever its function
// returns, which is not this table's column set, and a nested path is not a PostgREST table
// route at all — in both cases saying nothing is better than saying something misleading.
func tableFromPath(path string) string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return ""
	}
	return trimmed
}
