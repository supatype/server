package admin

import (
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/supatype/server/internal/static"
	"github.com/supatype/server/internal/utilities"
)

// maxSiteWalk bounds the file count walk, so a mis-pointed directory (a home folder, a drive root)
// cannot hold the request open. The route says when it stopped early.
const maxSiteWalk = 100_000

// SiteSettings is what the app mount at "/" is set to serve, as the gateway resolves it for one
// request: the manifest over the config, the same lookups buildApp makes. It lives here, not in the
// gateway, because the gateway imports this package.
type SiteSettings struct {
	// Mode is none, static or proxy.
	Mode string
	// StaticDir is the served directory, when Mode is static.
	StaticDir string
	// Upstream is the proxied address, when Mode is proxy.
	Upstream string
	// SPAFallback is whether a missing path answers with index.html, when Mode is static.
	// Cache is the Cache-Control the static handler sends, defaults filled in (static.CacheOpts.Effective).
	Cache       static.CacheOpts
	SPAFallback bool
	// SPAFallbackFromConfig is whether the project's config set SPAFallback, not its default.
	SPAFallbackFromConfig bool
}

// siteStatusResponse answers GET /admin/v1/site/status. It is read-only: Studio shows it, and the
// settings are changed in supatype.config.ts or the deployment's environment.
//
// Serving is the answer to "would a request for / get something", judged from the same inputs
// buildApp mounts on: a static directory with at least one file, or a proxy with a usable address.
// It does not dial the upstream, because a dev server that is restarting is still the configured
// upstream and a status page should not flap with it.
type siteStatusResponse struct {
	Mode        string `json:"mode"`
	StaticDir   string `json:"static_dir,omitempty"`
	Upstream    string `json:"upstream,omitempty"`
	SPAFallback bool   `json:"spa_fallback"`
	// SPAFallbackFromConfig is whether the project's config set SPAFallback, so Studio can say so.
	SPAFallbackFromConfig bool  `json:"spa_fallback_from_config"`
	Serving               bool  `json:"serving"`
	FileCount             int   `json:"file_count"`
	TotalBytes            int64 `json:"total_bytes"`
	// CacheControl is the Cache-Control the static app is served with, defaults included. Only for static.
	CacheControl *cacheControlResponse `json:"cache_control,omitempty"`
	// Truncated is true when the directory held more than maxSiteWalk entries and the count stopped.
	Truncated bool `json:"truncated,omitempty"`
}

type cacheControlResponse struct {
	HTML         string            `json:"html"`
	HashedAssets string            `json:"hashed_assets"`
	Prefixes     map[string]string `json:"prefixes"`
}

func siteStatusHandler(site func(*http.Request) SiteSettings) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings := SiteSettings{Mode: "none"}
		if site != nil {
			settings = site(r)
		}
		utilities.WriteJSON(w, http.StatusOK, describeSite(settings))
	}
}

func describeSite(s SiteSettings) siteStatusResponse {
	out := siteStatusResponse{Mode: strings.TrimSpace(s.Mode)}
	switch out.Mode {
	case "static":
		out.StaticDir = s.StaticDir
		out.SPAFallback = s.SPAFallback
		out.SPAFallbackFromConfig = s.SPAFallbackFromConfig
		out.CacheControl = &cacheControlResponse{HTML: s.Cache.HTML, HashedAssets: s.Cache.HashedAssets, Prefixes: s.Cache.Prefixes}
		if out.CacheControl.Prefixes == nil {
			out.CacheControl.Prefixes = map[string]string{}
		}
		if dir := strings.TrimSpace(s.StaticDir); dir != "" {
			out.FileCount, out.TotalBytes, out.Truncated = measureDir(dir, maxSiteWalk)
			out.Serving = out.FileCount > 0
		}
	case "proxy":
		out.Upstream = s.Upstream
		out.Serving = usableUpstream(s.Upstream)
	default:
		out.Mode = "none"
	}
	return out
}

func usableUpstream(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme != "" && u.Host != ""
}

// measureDir counts regular files under dir and their bytes. An unreadable or missing directory
// counts as empty, which is what a request for it would find.
func measureDir(dir string, limit int) (files int, bytes int64, truncated bool) {
	seen := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		seen++
		if seen > limit {
			truncated = true
			return fs.SkipAll
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil {
			files++
			bytes += info.Size()
		}
		return nil
	})
	return files, bytes, truncated
}
