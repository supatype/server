package storage

import (
	"net/url"
)

// EnsurePostgresSearchPathInURL ensures postgres / postgresql URLs include search_path
// matching the auth schema (DB_NAMESPACE). Pop issues unqualified DDL for schema_migrations;
// PostgreSQL 15+ rejects CREATE when the session search_path is empty. The namespace is the
// configured one, which config load has already trimmed and checked is a plain lower-case
// identifier, so it is sent as given.
func EnsurePostgresSearchPathInURL(connURL, ns string) (string, error) {
	u, err := url.Parse(connURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "postgres", "postgresql":
	default:
		return connURL, nil
	}
	q := u.Query()
	if q.Get("search_path") != ns {
		q.Set("search_path", ns)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
