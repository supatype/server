package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/supatype/server/internal/auth/storage/test"
	"github.com/supatype/server/internal/conf"
)

func TestCleanup(t *testing.T) {
	globalConfig, err := conf.LoadGlobal(modelsTestConfig)
	require.NoError(t, err)
	conn, err := test.SetupDBConnection(globalConfig)
	require.NoError(t, err)

	timebox := 10 * time.Second
	inactivityTimeout := 5 * time.Second
	globalConfig.Sessions.Timebox = &timebox
	globalConfig.Sessions.InactivityTimeout = &inactivityTimeout
	globalConfig.External.AnonymousUsers.Enabled = true

	cleanup := NewCleanup(globalConfig)

	for i := 0; i < 100; i += 1 {
		_, err := cleanup.Clean(conn)
		require.NoError(t, err)
	}
}
