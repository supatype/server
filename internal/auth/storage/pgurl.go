package storage

import (
	"net/url"
	"strings"
)

// EnsurePostgresSearchPathInURL ensures postgres / postgresql URLs include search_path
// matching the auth schema (DB_NAMESPACE). Pop issues unqualified DDL for schema_migrations;
// PostgreSQL 15+ rejects CREATE when the session search_path is empty.
func EnsurePostgresSearchPathInURL(connURL, namespace string) (string, error) {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		return connURL, nil
	}
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
