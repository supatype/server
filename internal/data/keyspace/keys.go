package keyspace

import (
	"fmt"
	"strconv"
	"strings"
)

// KeyLayout is how this process names the keys it keeps in the platform
// keyspace. It follows from who the process logs in as, which is why it travels
// with the credentials in Auth.
//
// pg_keyspace stores every key a tenant-scoped login sends as `{tenant}:{key}`,
// and checks its ACL against the key as sent. So a login scoped to one tenant
// names that tenant's keys relative to it, and an exempt login reading the same
// keyspace names them in the stored form. A keyspace with no per-tenant logins
// keeps the layout every deployment used before them.
type KeyLayout string

const (
	// LayoutLegacy files a tenant's keys under `tenant:{ref}:` and leaves the
	// shared ones (the MAU day-sets) as named. Self-host, dev, and any keyspace
	// without per-tenant logins. The zero value, so nothing that never sets a
	// layout changes.
	LayoutLegacy KeyLayout = ""

	// LayoutScoped sends every key relative: the login is scoped to one tenant,
	// and the keyspace prefixes the tenant itself. A cloud tenant gateway.
	LayoutScoped KeyLayout = "scoped"

	// LayoutPrefixed names a tenant's keys `{ref}:{name}`, the form the keyspace
	// stores a scoped login's keys in. An exempt login serving many tenants
	// from a keyspace whose gateways log in scoped.
	LayoutPrefixed KeyLayout = "prefixed"
)

// The names of the keys a tenant has, relative to the tenant.
const (
	KeyConfig      = "config"
	KeyManifest    = "manifest"
	KeyAPIConfig   = "api_config"
	KeyDBCredMeta  = "dbcred:meta"
	keyDBCredShelf = "dbcred:secret:v"
)

// KeyDBCredSecret names the wrapped database password of one generation.
func KeyDBCredSecret(generation int) string {
	return keyDBCredShelf + strconv.Itoa(generation)
}

// ParseKeyLayout reads SUPATYPE_KEYSPACE_KEY_LAYOUT. Empty and "legacy" are
// the legacy layout; anything unrecognised is an error, because a guessed
// layout reads every key from the wrong place and looks like a tenant that was
// never published.
func ParseKeyLayout(s string) (KeyLayout, error) {
	switch v := KeyLayout(strings.ToLower(strings.TrimSpace(s))); v {
	case LayoutLegacy, "legacy":
		return LayoutLegacy, nil
	case LayoutScoped, LayoutPrefixed:
		return v, nil
	default:
		return LayoutLegacy, fmt.Errorf("keyspace: unknown key layout %q (want legacy, scoped or prefixed)", s)
	}
}

// Tenant names the key called name that belongs to tenant ref.
func (l KeyLayout) Tenant(ref, name string) string {
	switch l {
	case LayoutScoped:
		return name
	case LayoutPrefixed:
		return ref + ":" + name
	default:
		return "tenant:" + ref + ":" + name
	}
}

// Shared names a key the legacy layout keeps outside every tenant, such as an
// organisation's MAU day-set. Under a scoped login there is no outside, so it
// belongs to the tenant that wrote it like everything else.
func (l KeyLayout) Shared(ref, name string) string {
	if l == LayoutLegacy {
		return name
	}
	return l.Tenant(ref, name)
}
