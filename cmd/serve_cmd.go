package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/supatype/auth/internal/api"
	"github.com/supatype/auth/internal/api/apiworker"
	"github.com/supatype/auth/internal/conf"
	"github.com/supatype/auth/internal/deno"
	"github.com/supatype/auth/internal/mailer/templatemailer"
	"github.com/supatype/auth/internal/modes"
	"github.com/supatype/auth/internal/proxy"
	"github.com/supatype/auth/internal/reloader"
	"github.com/supatype/auth/internal/serverconf"
	"github.com/supatype/auth/internal/storage"
	"github.com/supatype/auth/internal/utilities"
)

var serveCmd = cobra.Command{
	Use:  "serve",
	Long: "Start API server",
	Run: func(cmd *cobra.Command, args []string) {
		serve(cmd.Context())
	},
}

func serve(ctx context.Context) {
	if err := conf.LoadFile(configFile); err != nil {
		logrus.WithError(err).Fatal("unable to load config")
	}

	if err := conf.LoadDirectory(watchDir); err != nil {
		logrus.WithError(err).Error("unable to load config from watch dir")
	}

	config, err := conf.LoadGlobalFromEnv()
	if err != nil {
		logrus.WithError(err).Fatal("unable to load config")
	}

	// Include serve ctx which carries cancelation signals so DialContext does
	// not hang indefinitely at startup.
	db, err := storage.DialContext(ctx, config)
	if err != nil {
		logrus.Fatalf("error opening database: %+v", err)
	}
	defer db.Close()

	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	// Add the base context to the db, this is so during the shutdown sequence
	// the DB will be available while connections drain.
	db = db.WithContext(ctx)

	var wg sync.WaitGroup
	defer wg.Wait() // Do not return to caller until this goroutine is done.

	mrCache := templatemailer.NewCache()
	limiterOpts := api.NewLimiterOptions(config)
	initialAPI := api.NewAPIWithVersion(
		config, db, utilities.Version,
		limiterOpts,
		api.WithMailer(templatemailer.FromConfig(config, mrCache)),
	)

	addr := net.JoinHostPort(config.API.Host, config.API.Port)
	logrus.WithField("version", initialAPI.Version()).Infof("GoTrue API started on: %s", addr)

	ah := reloader.NewAtomicHandler(initialAPI)

	// ── supatype-server outer layer ───────────────────────────────────────────
	// Load .env from the current working directory (dev/standalone convenience).
	if cwd, err := os.Getwd(); err == nil {
		if err := serverconf.LoadDotEnv(cwd); err != nil {
			logrus.WithError(err).Debug("serve: no .env file loaded")
		}
	}

	srvCfg, err := serverconf.Load()
	if err != nil {
		logrus.WithError(err).Fatal("serve: failed to load server config")
	}
	if strings.TrimSpace(srvCfg.Mode) == "managed" && strings.TrimSpace(srvCfg.TenantHMACSecret) == "" {
		logrus.Fatal("serve: SUPATYPE_TENANT_HMAC_SECRET must be set in managed mode")
	}
	if strings.TrimSpace(srvCfg.Mode) != "dev" && strings.TrimSpace(srvCfg.ServiceRoleKey) == "" {
		logrus.Fatal("serve: SUPATYPE_SERVICE_ROLE_KEY must be set when SUPATYPE_MODE is not dev")
	}

	manifest, err := proxy.Load(srvCfg.ManifestPath)
	if err != nil {
		logrus.WithError(err).Fatal("serve: failed to load route manifest")
	}

	// Watch manifest for live reloads (best-effort — log and continue if it fails).
	if watchErr := proxy.Watch(srvCfg.ManifestPath, func(m *proxy.RouteManifest) {
		manifest = m
		logrus.Info("serve: route manifest reloaded")
	}); watchErr != nil {
		logrus.WithError(watchErr).Debug("serve: manifest watch not started")
	}

	// Start Deno edge functions subprocess only when the binary is available.
	// The functions admin API still mounts when DenoFunctionsDir is set (Studio list).
	var dm *deno.Manager
	if srvCfg.DenoFunctionsDir != "" && srvCfg.DenoPath != "" {
		if _, lookErr := exec.LookPath(srvCfg.DenoPath); lookErr != nil {
			logrus.WithError(lookErr).Warn("serve: Deno not found on PATH — edge function invocations disabled; install Deno or set SUPATYPE_DENO_PATH")
		} else {
			serveEntry := strings.TrimSpace(srvCfg.DenoServeScript)
			if serveEntry == "" {
				serveEntry = srvCfg.DenoFunctionsDir
			}
			if serveEntry != "" {
				denoPortInt := 8001 // default
				if srvCfg.DenoPort != "" {
					if p, parseErr := strconv.Atoi(srvCfg.DenoPort); parseErr == nil {
						denoPortInt = p
					}
				}
				dm = deno.New(srvCfg.DenoPath, serveEntry, denoPortInt, nil)
				dm.Start(ctx)
				defer dm.Stop()
			}
		}
	}

	outerMux := buildOuterMux(srvCfg, manifest, ah, dm)

	// Determine TLS config for standalone mode.
	var tlsCfg *tls.Config
	if srvCfg.Mode == "standalone" && srvCfg.TLSDomain != "" {
		acm, err := modes.NewACMEManager(srvCfg.TLSDomain, srvCfg.TLSACMECacheDir)
		if err != nil {
			logrus.WithError(err).Fatal("serve: ACME setup failed")
		}
		tlsCfg = modes.StandaloneTLSConfig(acm)

		// HTTP-01 challenge handler on :80.
		go func() {
			challengeSrv := &http.Server{
				Addr:    ":80",
				Handler: acm.HTTPHandler(nil),
			}
			if err := challengeSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logrus.WithError(err).Warn("serve: ACME HTTP challenge server error")
			}
		}()
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           outerMux,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 2 * time.Second, // to mitigate a Slowloris attack
		BaseContext: func(net.Listener) context.Context {
			return baseCtx
		},
	}
	log := logrus.WithField("component", "api")

	wrkLog := logrus.WithField("component", "apiworker")
	wrk := apiworker.New(config, mrCache, db, wrkLog)
	wg.Add(1)
	go func() {
		defer wg.Done()

		var err error
		defer func() {
			logFn := wrkLog.Info
			if err != nil {
				logFn = wrkLog.WithError(err).Error
			}
			logFn("background apiworker is exiting")
		}()

		// Work exits when ctx is done as in-flight requests do not depend
		// on it. If they do in the future this should be baseCtx instead.
		err = wrk.Work(ctx)
	}()

	if watchDir != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()

			rc := config.Reloading
			le := logrus.WithFields(logrus.Fields{
				"component":             "reloader",
				"notify_enabled":        rc.NotifyEnabled,
				"poller_enabled":        rc.PollerEnabled,
				"poller_interval":       rc.PollerInterval.String(),
				"signal_enabled":        rc.SignalEnabled,
				"signal_number":         rc.SignalNumber,
				"grace_period_duration": rc.GracePeriodInterval.String(),
			})
			le.Info("starting configuration reloader")

			var err error
			defer func() {
				exitFn := le.Info
				if err != nil {
					exitFn = le.WithError(err).Error
				}
				exitFn("config reloader is exiting")
			}()

			fn := func(latestCfg *conf.GlobalConfiguration) {
				le.Info("reloading api with new configuration")

				// When config is updated we notify the apiworker.
				wrk.ReloadConfig(latestCfg)

				// Create a new API version with the updated config.
				latestAPI := api.NewAPIWithVersion(
					latestCfg, db, utilities.Version,

					// Create a new mailer with existing template cache.
					api.WithMailer(
						templatemailer.FromConfig(latestCfg, mrCache),
					),

					// Persist existing rate limiters.
					//
					// TODO(cstockton): we should consider updating these, if we
					// rely on hot config reloads 100% then rate limiter changes
					// won't be picked up.
					limiterOpts,
				)
				ah.Store(latestAPI)
			}

			rl := reloader.NewReloader(rc, watchDir)
			if err = rl.Watch(ctx, fn); err != nil {
				log.WithError(err).Error("config reloader is exiting")
			}
		}()
	}

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
	}()

	lc := reusePortListenConfig()
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		log.WithError(err).Fatal("http server listen failed")
	}
	fmt.Fprintf(os.Stderr, "[supatype-server] listening on %s (mode=%s)\n", addr, os.Getenv("SUPATYPE_MODE"))
	err = httpSrv.Serve(listener)
	if err == http.ErrServerClosed {
		log.Info("http server closed")
	} else if err != nil {
		log.WithError(err).Fatal("http server serve failed")
	}
}
