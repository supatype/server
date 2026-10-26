package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/utilities"
)

// dbEndpoint is one Postgres address as the operator's own machine reaches it. It never carries a
// password: Studio fills one in from the credentials routes, which have their own rules about when
// a password may be shown.
type dbEndpoint struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	// Params is the query string without its "?", such as "sslmode=require", or empty.
	Params string `json:"params,omitempty"`
}

// connectionResponse answers GET /admin/v1/database/connection.
//
// Direct is null when this deployment has not said where its database is reachable from, and
// Unavailable then says why, so Studio can explain itself instead of printing a string that points
// at the API gateway. Pooler is null unless a pooler was configured: self-host ships none, and a
// pooler string that reaches no pooler is worse than no pooler string.
type connectionResponse struct {
	Direct      *dbEndpoint `json:"direct"`
	Pooler      *dbEndpoint `json:"pooler"`
	Unavailable string      `json:"unavailable,omitempty"`
}

// notPublished is what Studio shows when no host-side address was configured.
const notPublished = "This deployment does not publish its database to the host, so there is no " +
	"connection string to show. Set SUPATYPE_DB_PUBLIC_URL to the address your machine reaches it at."

// managedElsewhere is the same answer on Cloud, where one server pod serves many projects and the
// connection string belongs to the control plane, so an environment variable is no remedy.
const managedElsewhere = "This project's connection string is in its Cloud project settings."

func unavailableReason(mode string) string {
	if mode == "managed" {
		return managedElsewhere
	}
	return notPublished
}

// connectionHandler serves the connection details the deployment declared.
//
// The server cannot work these out for itself. Its own DSN names the database as the server
// reaches it, which inside compose is the `db` service on the stack's private network: correct for
// the server and useless on a laptop. The published port, the host, and whether a port is
// published at all are decided by whoever started the stack, so the CLI states them in
// SUPATYPE_DB_PUBLIC_URL and this route reports them.
func connectionHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp, err := connectionDetails(cfg)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		utilities.WriteJSON(w, http.StatusOK, resp)
	}
}

func connectionDetails(cfg *config.Config) (connectionResponse, error) {
	direct, err := parseEndpoint("SUPATYPE_DB_PUBLIC_URL", cfg.DBPublicURL)
	if err != nil {
		return connectionResponse{}, err
	}
	pooler, err := parseEndpoint("SUPATYPE_DB_POOLER_URL", cfg.DBPoolerURL)
	if err != nil {
		return connectionResponse{}, err
	}
	resp := connectionResponse{Direct: direct, Pooler: pooler}
	if direct == nil {
		resp.Unavailable = unavailableReason(cfg.Mode)
	}
	return resp, nil
}

// parseEndpoint reads one connection URL into its parts, dropping any password it carries. An
// empty value is "not configured", which is not an error.
func parseEndpoint(name, raw string) (*dbEndpoint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" {
		return nil, fmt.Errorf("%s is not a postgres:// URL with a host", name)
	}
	port, err := endpointPort(u)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	database := strings.TrimPrefix(u.Path, "/")
	if database == "" {
		return nil, fmt.Errorf("%s names no database", name)
	}
	return &dbEndpoint{
		Host:     u.Hostname(),
		Port:     port,
		Database: database,
		User:     u.User.Username(),
		Params:   u.RawQuery,
	}, nil
}

// endpointPort is the URL's port, or 5432 when it names none, which is what libpq assumes too.
func endpointPort(u *url.URL) (int, error) {
	if u.Port() == "" {
		return 5432, nil
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %q is not a TCP port", u.Port())
	}
	return port, nil
}
