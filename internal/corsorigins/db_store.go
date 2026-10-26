package corsorigins

import (
	"context"

	"github.com/supatype/server/internal/auth/models"
	"github.com/supatype/server/internal/auth/storage"
)

// DBStore keeps the saved list in the auth schema, on the auth service's own connection: the role
// that owns the auth migrations owns the table, so no other grant is needed on any host.
type DBStore struct {
	DB *storage.Connection
}

// Load returns the saved list, or nil when none is saved.
func (s DBStore) Load(ctx context.Context) (*Saved, error) {
	origins, at, err := models.FindCorsOrigins(s.DB.WithContext(ctx))
	if err != nil || at == nil {
		return nil, err
	}
	return &Saved{Origins: origins, UpdatedAt: *at}, nil
}

// Save replaces the saved list.
func (s DBStore) Save(ctx context.Context, origins []string) (*Saved, error) {
	at, err := models.SaveCorsOrigins(s.DB.WithContext(ctx), origins)
	if err != nil {
		return nil, err
	}
	return &Saved{Origins: origins, UpdatedAt: at}, nil
}

// Delete removes the saved list.
func (s DBStore) Delete(ctx context.Context) error {
	return models.DeleteCorsOrigins(s.DB.WithContext(ctx))
}
