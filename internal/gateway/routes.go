package gateway

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/modelhooks"
)

// The gateway's routes, as data.
//
// This was 328 lines of imperative mounting in which a route's existence, its
// prefix handling, the condition it depended on and the order it had to come in
// were all expressed by where its code sat. Order still matters here, and it is
// still the order of this list, but everything else about a route is now a field
// you can read rather than control flow you have to follow.

// Verb is how a route attaches to the router.
type Verb uint8

const (
	// MountAll attaches a sub-tree, matching the pattern and everything under it.
	MountAll Verb = iota
	// HandleExact attaches one pattern for every method.
	HandleExact
	// GET and POST attach a single method.
	GET
	// POST attaches a single method.
	POST
)

// Route is one entry in the gateway's table.
type Route struct {
	// Pattern is the path it answers on.
	Pattern string
	// Verb is how it attaches.
	Verb Verb
	// Strip removes Pattern from the request path before the handler sees it.
	Strip bool
	// When decides whether the route exists at all. Nil means always.
	When func(*Deps) bool
	// Build returns the handler. Returning nil skips the route, for the cases
	// where the decision cannot be made until the handler is constructed.
	Build func(*Deps) http.Handler
	// Log is written at info when the route is mounted, to keep the startup
	// output the operators read unchanged.
	Log string
}

// Routes is the table, in mount order.
//
// Order is load-bearing in one place: the Studio routes must register before the
// application catch-all at "/", or the catch-all shadows them.
func Routes() []Route {
	return []Route{
		{
			Pattern: "/internal/v0hooks/send-email",
			Verb:    POST,
			When:    func(d *Deps) bool { return d.SendEmail != nil },
			Build:   func(d *Deps) http.Handler { return d.SendEmail },
			Log:     "mux: send-email hook receiver mounted at POST /internal/v0hooks/send-email",
		},
		{
			Pattern: "/admin/v1",
			Verb:    MountAll,
			Strip:   true,
			Build:   buildAdminAPI,
			Log:     "mux: admin API mounted at /admin/v1",
		},
		{
			Pattern: "/studio-config",
			Verb:    POST,
			Build:   buildStudioConfig,
		},
		// Studio membership assignment. Mounted outside /admin/v1, the
		// service-role admin API, because this is authenticated as a project user
		// with a Studio role rather than with the service role key.
		{Pattern: "/admin/studio-roles", Verb: HandleExact, Build: buildStudioMembers},
		{
			Pattern: "/admin/studio-members",
			Verb:    HandleExact,
			Build:   buildStudioMembers,
			Log:     "mux: Studio membership API mounted at /admin/studio-members",
		},
		{Pattern: "/admin/studio-members/*", Verb: HandleExact, Build: buildStudioMembers},
		{
			Pattern: "/sql",
			Verb:    POST,
			Build:   buildSQLRunner,
		},
		{
			Pattern: "/auth/v1",
			Verb:    MountAll,
			Strip:   true,
			Build:   func(d *Deps) http.Handler { return d.Auth },
		},
		{
			// previous() reads the rows a write is about to change. A failure to
			// build it is not fatal: hooks still run, and the context simply has
			// no previous.
			Pattern: strings.TrimSuffix(modelhooks.PreviousPathPrefix, "/"),
			Verb:    MountAll,
			When:    func(d *Deps) bool { return d.HookCallback != nil },
			Build:   func(d *Deps) http.Handler { return d.HookCallback.Handler() },
			Log:     "mux: hook callback mounted at " + modelhooks.PreviousPathPrefix,
		},
		{
			Pattern: "/rest/v1",
			Verb:    MountAll,
			Strip:   true,
			Build:   buildREST,
			Log:     "mux: PostgREST proxy mounted at /rest/v1",
		},
		{
			Pattern: "/graphql/v1",
			Verb:    MountAll,
			Build:   buildGraphQL,
			Log:     "mux: GraphQL proxy mounted at /graphql/v1",
		},
		{
			Pattern: "/storage/v1",
			Verb:    MountAll,
			Strip:   true,
			Build:   buildStorage,
		},
		{
			Pattern: "/functions/v1/admin",
			Verb:    MountAll,
			When:    func(d *Deps) bool { return d.Config.DenoFunctionsDir != "" },
			Build:   buildFunctionsAdmin,
		},
		{
			Pattern: "/functions/v1",
			Verb:    MountAll,
			Strip:   true,
			When: func(d *Deps) bool {
				return d.Config.DenoFunctionsDir != "" || strings.TrimSpace(d.Config.FunctionsWorkerURL) != ""
			},
			Build: buildFunctions,
			Log:   "mux: Functions invocation proxy mounted at /functions/v1",
		},
		{
			Pattern: "/platform/v1",
			Verb:    MountAll,
			Strip:   true,
			Build:   buildPlatform,
			Log:     "mux: Platform control plane proxy mounted at /platform/v1",
		},
		{
			Pattern: "/realtime/v1",
			Verb:    MountAll,
			Strip:   true,
			When: func(d *Deps) bool {
				return d.Baseline.RealtimeEnabled || strings.TrimSpace(d.Config.RealtimeURL) != ""
			},
			Build: buildRealtime,
			Log:   "mux: Realtime invocation proxy mounted at /realtime/v1",
		},
		{
			Pattern: "/_vite/*",
			Verb:    HandleExact,
			Strip:   true,
			When:    func(d *Deps) bool { return strings.EqualFold(strings.TrimSpace(d.Config.Mode), "dev") },
			Build:   buildVite,
		},
		// Studio must register before the catch-all below, or "/" shadows it.
		{Pattern: "/studio/auth/verify", Verb: GET, Build: buildStudioVerify},
		// Studio's bootstrap: the schema filtered to what the caller may reach,
		// and what they may do with it. Both answered from the database per
		// request.
		{Pattern: "/studio/schema", Verb: GET, Build: buildStudioSchema},
		{Pattern: "/studio/session", Verb: GET, Build: buildStudioSession},
		{
			Pattern: "/studio/proxy",
			Verb:    MountAll,
			Strip:   true,
			Build:   buildStudioProxy,
			Log:     "mux: Studio auth mounted at /studio/auth/verify and /studio/proxy",
		},
		{
			Pattern: "/",
			Verb:    MountAll,
			Build:   buildApp,
		},
	}
}

// attach adds one route to the router, honouring its verb and prefix handling.
func attach(r chi.Router, route Route, d *Deps) {
	if route.When != nil && !route.When(d) {
		return
	}
	handler := route.Build(d)
	if handler == nil {
		return
	}
	if route.Strip {
		handler = http.StripPrefix(strings.TrimSuffix(route.Pattern, "/*"), handler)
	}
	switch route.Verb {
	case MountAll:
		r.Mount(route.Pattern, handler)
	case HandleExact:
		r.Handle(route.Pattern, handler)
	case GET:
		r.Get(route.Pattern, handler.ServeHTTP)
	case POST:
		r.Post(route.Pattern, handler.ServeHTTP)
	}
	if route.Log != "" {
		logrus.Info(route.Log)
	}
}
