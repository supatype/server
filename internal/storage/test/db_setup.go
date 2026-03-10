package test

import (
	"github.com/supatype/auth/internal/conf"
	"github.com/supatype/auth/internal/storage"
)

func SetupDBConnection(globalConfig *conf.GlobalConfiguration) (*storage.Connection, error) {
	return storage.Dial(globalConfig)
}
