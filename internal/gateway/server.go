// Package server exposes the full supatype-server HTTP surface (auth, admin,
// rest, graphql, storage, functions, realtime, studio, platform) as an
// importable handler so it can be embedded in-process by other binaries
// (e.g. the Supatype Cloud metering/liveness gateway) without re-implementing
// the bootstrap or adding a network hop.
//
// New performs the same bootstrap that `supatype-server serve` does, minus the
// listener/TLS/graceful-shutdown loop (which the caller owns). Stock binary
// behaviour is preserved: cmd/serve_cmd.go calls New and then runs the
// unchanged listen loop, so routes and authCfg handling are identical.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/auth"
	"github.com/supatype/server/internal/auth/apiworker"
	"github.com/supatype/server/internal/auth/mailer/templatemailer"
	"github.com/supatype/server/internal/auth/provider"
	smsprovider "github.com/supatype/server/internal/auth/sms_provider"
	"github.com/supatype/server/internal/auth/storage"
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/deno"
	"github.com/supatype/server/internal/observability"
	"github.com/supatype/server/internal/outerhealth"
	"github.com/supatype/server/internal/platform"
	"github.com/supatype/server/internal/proxy"
	"github.com/supatype/server/internal/reloader"
	"github.com/supatype/server/internal/security"
	"github.com/supatype/server/internal/utilities"
)

// ConfigFile and WatchDir mirror the `-c` / `-d` CLI flags. The stock binary
// sets these from cobra before calling New; embedders (cloud gateway) leave
// them empty to load configuration from the environment only.
var (
	ConfigFile = ""
	WatchDir   = ""
)

// New builds the full supatype-server outer handler and starts its background
// workers (apiworker + optional authCfg reloader), which stop when ctx is
// cancelled. The returned drain func waits for those workers and releases
// resources (Deno subprocess, Valkey, database); call it after cancelling ctx.
//
// New does not bind a listener or configure TLS — the caller serves the
// returned handler however it likes (stock binary: cmd/serve_cmd.go; cloud:
// the metering gateway on :9999).
func New(ctx context.Context) (http.Handler, func(), error) {
	if err := conf.LoadFile(ConfigFile); err != nil {
		return nil, nil, fmt.Errorf("unable to load authCfg: %w", err)
	}

	if err := conf.LoadDirectory(WatchDir); err != nil {
		logrus.WithError(err).Error("unable to load authCfg from watch dir")
	}

	authCfg, err := conf.LoadGlobalFromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("unable to load authCfg: %w", err)
	}

	// Include serve ctx which carries cancelation signals so DialContext does
	// not hang indefinitely at startup.
	//
	// Retried while the database is merely unreachable. Returning the error here exits the binary,
	// which was survivable only because the Compose `db` healthcheck held the container back until
	// Postgres answered — a stack pointed at an external database has no such container, and no
	// healthcheck ever covered a database that restarts later. A wrong password or a missing database
	// still fails immediately; see internal/auth/storage/dial_retry.go for where that line sits.
	db, err := storage.DialWithRetry(ctx, func(ctx context.Context) (*storage.Connection, error) {
		return storage.DialContext(ctx, authCfg)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error opening database: %w", err)
	}

	// Add the serve context to the db, this is so during the shutdown sequence
	// the DB will be available while connections drain.
	db = db.WithContext(ctx)

	var (
		wg sync.WaitGroup
		dm *deno.Manager
	)
	// Never nil, so every consumer and both shutdown paths can use it without
	// asking whether a cache exists.
	var resources *data.Resources
	// Nil everywhere except a hosted tenant pod. Declared here because the drain
	// closure below is built before it is assigned, and a nil Gateway closes and
	// wraps as a no-op.
	var tenantGateway *platform.Gateway
	vkShared := valkey.Unavailable()

	// fail releases what has been acquired so far and returns the bootstrap error.
	fail := func(err error) (http.Handler, func(), error) {
		_ = resources.Close()
		_ = db.Close()
		return nil, nil, err
	}

	mrCache := templatemailer.NewCache()
	limiterOpts := auth.NewLimiterOptions(authCfg)
	initialAPI := auth.NewAPIWithVersion(
		authCfg, db, utilities.Version,
		limiterOpts,
		auth.WithMailer(templatemailer.FromConfig(authCfg, mrCache)),
	)
	logrus.WithField("version", initialAPI.Version()).Info("auth API initialised")

	ah := reloader.NewAtomicHandler(initialAPI)

	// ── supatype-server outer layer ───────────────────────────────────────────
	// Load `.env` / `.env.local` from --authCfg dir, cwd, then manifest project root (A22).
	if cwd, err := os.Getwd(); err == nil {
		if err := config.LoadDotEnvForServe(cwd, ConfigFile); err != nil {
			logrus.WithError(err).Warn("serve: .env load failed")
		}
	} else {
		logrus.WithError(err).Debug("serve: getwd failed; skipping .env")
	}

	srvCfg, err := config.Load()
	if err != nil {
		return fail(fmt.Errorf("serve: failed to load server authCfg: %w", err))
	}
	configureOuterAccessLogging(srvCfg.OuterLogLevel)
	// The structured logger used to read LOG_LEVEL at package initialisation.
	// It is set from configuration here instead, before the listener opens.
	observability.SetStructuredLevel(srvCfg.LogLevel)
	// Timeouts that used to be parsed from the environment in package inits,
	// before configuration existed and with log.Fatalf as the error path.
	provider.SetHTTPTimeout(authCfg.InternalHTTPTimeout)
	smsprovider.SetHTTPTimeout(authCfg.InternalHTTPTimeout)
	security.SetHTTPTimeout(authCfg.Security.Captcha.Timeout)
	// Every address this deployment answers on, joined from both configs. Studio
	// refuses to open without authentication unless all of them are local.
	srvCfg.PublicURLs = []string{
		strings.TrimSpace(authCfg.API.ExternalURL),
		strings.TrimSpace(authCfg.SiteURL),
		strings.TrimSpace(srvCfg.SupatypeURL),
	}
	if strings.TrimSpace(srvCfg.Mode) == "managed" && strings.TrimSpace(srvCfg.TenantHMACSecret) == "" {
		return fail(errors.New("serve: SUPATYPE_TENANT_HMAC_SECRET must be set in managed mode"))
	}
	if strings.TrimSpace(srvCfg.Mode) != "dev" && strings.TrimSpace(srvCfg.ServiceRoleKey) == "" {
		return fail(errors.New("serve: SUPATYPE_SERVICE_ROLE_KEY must be set when SUPATYPE_MODE is not dev"))
	}

	manifest, err := proxy.Load(srvCfg.ManifestPath)
	if err != nil {
		return fail(fmt.Errorf("serve: failed to load route manifest: %w", err))
	}

	ref := strings.TrimSpace(srvCfg.ManagedProjectRef)
	managed := strings.TrimSpace(srvCfg.Mode) == "managed"

	// Every connection this process holds, acquired and released in one place.
	var resErr error
	resources, resErr = data.Open(ctx, srvCfg)
	if resErr != nil {
		return fail(resErr)
	}
	vkShared = resources.Cache()

	mergeFromValkey := managed && vkShared.Available() && ref != ""
	perTenantManifest := managed && vkShared.Available() && ref == ""

	var fileManifestAt atomic.Value
	fileManifestAt.Store(manifest)

	var manifestLive atomic.Value
	manifestLive.Store(manifest)

	var tenantCache *valkey.TenantManifestCache
	if perTenantManifest {
		tenantCache = valkey.NewTenantManifestCache(vkShared, 0, func() *proxy.RouteManifest {
			v := fileManifestAt.Load()
			if v == nil {
				return &proxy.RouteManifest{Schema: "public"}
			}
			return proxy.CloneRouteManifest(v.(*proxy.RouteManifest))
		})
		logrus.Info("serve: per-tenant route manifests from Valkey (SUPATYPE_MANAGED_PROJECT_REF unset)")
	}

	reapplyFileManifest := func(fileM *proxy.RouteManifest) {
		fileManifestAt.Store(fileM)
		if tenantCache != nil {
			tenantCache.Flush()
		}
		if mergeFromValkey {
			merged, mergeErr := valkey.LoadMergedManagedManifest(context.Background(), vkShared, ref, fileM)
			if mergeErr != nil {
				logrus.WithError(mergeErr).Warn("serve: Valkey manifest merge failed — keeping previous live manifest")
				return
			}
			manifestLive.Store(merged)
			return
		}
		manifestLive.Store(fileM)
	}

	if mergeFromValkey {
		reapplyFileManifest(manifest)
		logrus.WithField("project_ref", ref).Info("serve: route manifest merged from Valkey")
	}

	if watchErr := proxy.Watch(srvCfg.ManifestPath, func(m *proxy.RouteManifest) {
		reapplyFileManifest(m)
		logrus.Info("serve: route manifest reloaded")
	}); watchErr != nil {
		logrus.WithError(watchErr).Debug("serve: manifest watch not started")
	}

	// Start Deno edge functions subprocess when no external worker URL is configured.
	// The functions admin API still mounts when DenoFunctionsDir is set (Studio list).
	workerURL := strings.TrimSpace(srvCfg.FunctionsWorkerURL)
	if workerURL != "" {
		logrus.WithField("url", workerURL).Info("serve: using external functions worker (in-process Deno disabled)")
	}
	if workerURL == "" && srvCfg.DenoFunctionsDir != "" && srvCfg.DenoPath != "" {
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
				dm = deno.New(
					srvCfg.DenoPath,
					serveEntry,
					denoPortInt,
					deno.EdgeSubprocessEnv(srvCfg, strings.TrimSpace(authCfg.API.ExternalURL)),
					strings.TrimSpace(srvCfg.Mode) == "dev",
				)
				dm.Start(ctx)
			}
		}
	}

	denoBaseStr := ""
	if srvCfg.DenoFunctionsDir != "" {
		if workerURL != "" {
			denoBaseStr = workerURL
		} else if dm != nil {
			denoBaseStr = "http://127.0.0.1:" + utilities.FirstNonEmpty(srvCfg.DenoPort, "8001")
		}
	}

	healthProbes := func() outerhealth.ProbeConfig {
		fm := fileManifestAt.Load()
		var pc outerhealth.ProbeConfig
		if fm == nil {
			pc = outerhealth.ProbeConfigFrom(srvCfg, &proxy.RouteManifest{Schema: "public"}, denoBaseStr)
		} else {
			pc = outerhealth.ProbeConfigFrom(srvCfg, fm.(*proxy.RouteManifest), denoBaseStr)
		}
		pc.SelfBaseURL = outerhealth.SelfBaseURLForRealtimeProbe(
			srvCfg.HealthSelfBaseURL,
			srvCfg.Mode,
			srvCfg.TLSDomain,
			authCfg.API.Host,
			authCfg.API.Port,
		)
		return pc
	}

	manifestFor := func(req *http.Request) *proxy.RouteManifest {
		if tenantCache != nil && req != nil {
			if t := strings.TrimSpace(req.Header.Get("X-Supatype-Tenant")); t != "" {
				m, terr := tenantCache.Get(req.Context(), t)
				if terr == nil && m != nil {
					return m
				}
				if terr != nil {
					logrus.WithError(terr).WithField("tenant", t).Debug("serve: tenant manifest from Valkey failed")
				}
			}
		}
		v := manifestLive.Load()
		if v == nil {
			return &proxy.RouteManifest{Schema: "public"}
		}
		return v.(*proxy.RouteManifest)
	}

	var sendEmailHook http.Handler
	if authCfg.Hook.SendEmail.Enabled && len(authCfg.Hook.SendEmail.HTTPHookSecrets) > 0 {
		sendEmailHook = newSendEmailHookReceiver(ah, authCfg.Hook.SendEmail.HTTPHookSecrets)
	}

	outerMux := buildOuterMux(srvCfg, manifestFor, healthProbes, ah, dm, utilities.Version, resources, sendEmailHook)

	// ── background workers (stop on ctx cancel; drained by the returned func) ──
	wrkLog := logrus.WithField("component", "apiworker")
	wrk := apiworker.New(authCfg, mrCache, db, wrkLog)
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

	if WatchDir != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()

			rc := authCfg.Reloading
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
				exitFn("authCfg reloader is exiting")
			}()

			fn := func(latestCfg *conf.GlobalConfiguration) {
				le.Info("reloading api with new configuration")

				// When authCfg is updated we notify the apiworker.
				wrk.ReloadConfig(latestCfg)

				// Create a new API version with the updated authCfg.
				latestAPI := auth.NewAPIWithVersion(
					latestCfg, db, utilities.Version,

					// Create a new mailer with existing template cache.
					auth.WithMailer(
						templatemailer.FromConfig(latestCfg, mrCache),
					),

					// Persist existing rate limiters.
					limiterOpts,
				)
				ah.Store(latestAPI)
			}

			rl := reloader.NewReloader(rc, WatchDir)
			if err = rl.Watch(ctx, fn); err != nil {
				le.WithError(err).Error("authCfg reloader is exiting")
			}
		}()
	}

	drain := func() {
		// Workers stop on ctx cancellation; wait for them before releasing
		// resources they use (db, valkey, deno subprocess).
		wg.Wait()
		if dm != nil {
			dm.Stop()
		}
		tenantGateway.Close()
		_ = resources.Close()
		_ = db.Close()
	}

	tenantGateway = platform.New(srvCfg, vkShared)
	handler := tenantGateway.Wrap(outerMux)
	return handler, drain, nil
}
