package cmd

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/modes"
)

// startInternalEntrance serves handler on SUPATYPE_INTERNAL_ADDR as modes.NewInternalEntranceServer,
// so a project's own functions and hooks reach its API from inside the cluster without a Kong
// signature. It returns the server to shut down, or nil when no internal address is set. The caller
// has already checked the settings with gateway.ValidateInternalEntrance.
//
// A listener that cannot start is fatal, not a warning: the project's functions depend on it, and a
// pod that restarts says so where a log line alone would not.
func startInternalEntrance(srvCfg *config.Config, handler http.Handler, baseCtx context.Context, wg *sync.WaitGroup) *http.Server {
	addr := strings.TrimSpace(srvCfg.InternalAddr)
	if addr == "" {
		return nil
	}
	srv := modes.NewInternalEntranceServer(addr, strings.TrimSpace(srvCfg.ManagedProjectRef), handler, baseCtx)
	log := logrus.WithFields(logrus.Fields{"component": "internal-entrance", "addr": addr})
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("serve: internal entrance for the project's own pods")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Fatal("serve: internal entrance listen failed")
		}
	}()
	return srv
}
