package test

import (
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/storage"
)

func SetupDBConnection(globalConfig *conf.GlobalConfiguration) (*storage.Connection, error) {
	return storage.Dial(globalConfig)
}
