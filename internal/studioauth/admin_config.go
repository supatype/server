package studioauth

import (
	"context"
	"errors"
	"strings"

	"github.com/supatype/server/internal/studiobootstrap"
)

// StoredAdminConfig reads the admin config a push recorded in the database. It is a function so
// the choice below can be tested without one.
type StoredAdminConfig func(ctx context.Context) ([]byte, error)

// ResolveAdminConfig returns the admin config Studio should render. The database copy wins: a
// push linked from another machine records the config there and cannot write this server's
// admin-config.json, so the file alone leaves Studio saying nothing has been pushed. The file is
// the answer when the database holds none, which is a project pushed from this server's own folder
// before the database copy existed. os.ErrNotExist means neither has one.
func ResolveAdminConfig(ctx context.Context, stored StoredAdminConfig, path string) ([]byte, error) {
	if stored != nil {
		body, err := stored(ctx)
		switch {
		case err == nil && len(strings.TrimSpace(string(body))) > 0:
			return body, nil
		case err != nil && !errors.Is(err, studiobootstrap.ErrNoSchemaState):
			// An unreachable database must not hide a config the file can still serve.
			if fileBody, fileErr := ReadAdminConfigFile(path); fileErr == nil {
				return fileBody, nil
			}
			return nil, err
		}
	}
	return ReadAdminConfigFile(path)
}

// StoredAdminConfigFrom adapts the snapshot loader to StoredAdminConfig.
func StoredAdminConfigFrom(load func(ctx context.Context) (*studiobootstrap.Snapshot, error)) StoredAdminConfig {
	return func(ctx context.Context) ([]byte, error) {
		snap, err := load(ctx)
		if err != nil {
			return nil, err
		}
		return snap.AdminConfig, nil
	}
}

