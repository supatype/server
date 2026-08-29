package test

import (
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
)

func SetupDBConnection(globalConfig *conf.GlobalConfiguration) (*storage.Connection, error) {
	return storage.Dial(globalConfig)
}
