package cmd

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/config"
)

// startAppEntrance serves handler, the project's static site and public env, on SUPATYPE_APP_ADDR. It
// returns the server to shut down, or nil when no app address is set. The caller has already checked
// the settings with gateway.ValidateAppListener, which refuses anything but a managed tenant gateway.
//
// A listener that cannot start is fatal, not a warning: Kong routes the project's own host to it, and
// a pod that restarts says so where a log line alone would not.
func startAppEntrance(srvCfg *config.Config, handler http.Handler, baseCtx context.Context, wg *sync.WaitGroup) *http.Server {
	addr := strings.TrimSpace(srvCfg.AppAddr)
	if addr == "" || handler == nil {
		return nil
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}
	log := logrus.WithFields(logrus.Fields{"component": "app-entrance", "addr": addr})
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Info("serve: app listener for the project's static site")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Fatal("serve: app listener failed")
		}
	}()
	return srv
}
