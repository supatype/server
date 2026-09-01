package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/gateway"
	"github.com/supatype/server/internal/modes"
	"github.com/supatype/server/internal/utilities"
)

var serveCmd = cobra.Command{
	Use:  "serve",
	Long: "Start API server",
	Run: func(cmd *cobra.Command, args []string) {
		serve(cmd.Context())
	},
}

func serve(ctx context.Context) {
	// Build the full server surface + background workers. gateway.New performs
	// the bootstrap (authCfg, DB, API, manifest, Valkey, Deno, mux); this binary
	// owns the listener/TLS/graceful-shutdown loop below.
	gateway.ConfigFile = configFile
	gateway.WatchDir = watchDir
	handler, drain, err := gateway.New(ctx)
	if err != nil {
		logrus.WithError(err).Fatal("unable to start server")
	}
	defer drain()

	// Listener parameters. gateway.New has already loaded authCfg files and
	// `.env` into the process environment, so these reads are consistent.
	authCfg, err := conf.LoadGlobalFromEnv()
	if err != nil {
		logrus.WithError(err).Fatal("unable to load authCfg")
	}
	srvCfg, err := config.Load()
	if err != nil {
		logrus.WithError(err).Fatal("serve: failed to load server authCfg")
	}

	addr := net.JoinHostPort(authCfg.API.Host, authCfg.API.Port)
	logrus.WithField("version", utilities.Version).Infof("supatype-server API listening on: %s", addr)

	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	var wg sync.WaitGroup
	defer wg.Wait() // Do not return to caller until ACME/shutdown goroutines are done.

	var acmeHTTPSrv *http.Server

	// Determine TLS authCfg for standalone mode.
	var tlsCfg *tls.Config
	if srvCfg.Mode == "standalone" && srvCfg.TLSDomain != "" {
		acm, err := modes.NewACMEManager(srvCfg.TLSDomain, srvCfg.TLSACMECacheDir)
		if err != nil {
			logrus.WithError(err).Fatal("serve: ACME setup failed")
		}
		tlsCfg = modes.StandaloneTLSConfig(acm)

		acmeAddr := strings.TrimSpace(srvCfg.ACMEHTTPAddr)
		if acmeAddr == "" {
			acmeAddr = ":80"
		}
		acmeHTTPSrv = &http.Server{
			Addr:              acmeAddr,
			Handler:           acm.HTTPHandler(nil),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       2 * time.Minute,
			WriteTimeout:      2 * time.Minute,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			logrus.WithField("addr", acmeAddr).Info("serve: ACME HTTP-01 challenge listener")
			if err := acmeHTTPSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logrus.WithError(err).Warn("serve: ACME HTTP challenge server error")
			}
		}()
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 2 * time.Second, // to mitigate a Slowloris attack
		BaseContext: func(net.Listener) context.Context {
			return baseCtx
		},
	}
	log := logrus.WithField("component", "api")

	wg.Add(1)
	go func() { // #nosec G118 -- Cleanup goroutine intentionally outlives the request; context.Background() is required for shutdown after parent context is cancelled.
		defer wg.Done()

		<-ctx.Done()

		// This must be done after httpSrv exits, otherwise you may potentially
		// have 1 or more inflight http requests blocked until the shutdownCtx
		// is canceled.
		defer baseCancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Minute)
		defer shutdownCancel()

		if err := httpSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.WithError(err).Error("shutdown failed")
		}
		if acmeHTTPSrv != nil {
			acmeShutdown, acmeCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer acmeCancel()
			if err := acmeHTTPSrv.Shutdown(acmeShutdown); err != nil && !errors.Is(err, context.Canceled) {
				log.WithError(err).Warn("serve: ACME HTTP challenge server shutdown")
			}
		}
	}()

	lc := reusePortListenConfig()
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		log.WithError(err).Fatal("http server listen failed")
	}
	fmt.Fprintf(os.Stderr, "[supatype-server] listening on %s (mode=%s)\n", addr, srvCfg.Mode)
	err = httpSrv.Serve(listener)
	if err == http.ErrServerClosed {
		log.Info("http server closed")
	} else if err != nil {
		log.WithError(err).Fatal("http server serve failed")
	}
}
