package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	vkgo "github.com/valkey-io/valkey-go"
)

// Cloud gateway wrap: every-request activity + auth MAU + non-prod robots.
// Enabled when SUPATYPE_CLOUD_ACTIVITY_ENABLED=true (tenant gateway pods).

type cloudGatewayCfg struct {
	enabled          bool
	activityBaseURL  string
	internalSecret   string
	tenantID         string
	nonprod          bool
	blockBots        bool
	valkeyAddr       string
	emailSalt        string
	controlPlaneURL  string
}

func loadCloudGatewayCfg() cloudGatewayCfg {
	return cloudGatewayCfg{
		enabled:         strings.EqualFold(os.Getenv("SUPATYPE_CLOUD_ACTIVITY_ENABLED"), "true"),
		activityBaseURL: strings.TrimRight(firstNonEmpty(os.Getenv("SUPATYPE_CLOUD_ACTIVITY_URL"), "http://control-plane:4001"), "/"),
		internalSecret:  os.Getenv("SUPATYPE_INTERNAL_HMAC_SECRET"),
		tenantID:        strings.TrimSpace(os.Getenv("SUPATYPE_MANAGED_PROJECT_REF")),
		nonprod:         strings.EqualFold(os.Getenv("SUPATYPE_NONPROD"), "true"),
		blockBots:       strings.EqualFold(os.Getenv("SUPATYPE_BLOCK_BOT_UA"), "true"),
		valkeyAddr:      firstNonEmpty(os.Getenv("SUPATYPE_VALKEY_ADDR"), os.Getenv("VALKEY_ADDR")),
		emailSalt:       os.Getenv("MAU_EMAIL_SALT"),
		controlPlaneURL: strings.TrimRight(firstNonEmpty(os.Getenv("SUPATYPE_CLOUD_ACTIVITY_URL"), "http://control-plane:4001"), "/"),
	}
}

var botUASubstrings = []string{
	"googlebot", "bingbot", "slurp", "duckduckbot", "baiduspider",
	"yandexbot", "gptbot", "bytespider", "claudebot", "anthropic",
	"ccbot", "chatgpt", "petalbot", "semrush", "ahrefs",
}

func isBotUA(ua string) bool {
	l := strings.ToLower(ua)
	for _, s := range botUASubstrings {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}

func isPrefetch(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Sec-Purpose"), "prefetch") ||
		strings.EqualFold(r.Header.Get("Purpose"), "prefetch")
}

func wrapCloudGateway(next http.Handler) http.Handler {
	cfg := loadCloudGatewayCfg()
	if !cfg.enabled {
		return next
	}

	var (
		vk     vkgo.Client
		vkOnce sync.Once
	)
	getVK := func() vkgo.Client {
		vkOnce.Do(func() {
			if cfg.valkeyAddr == "" {
				return
			}
			c, err := vkgo.NewClient(vkgo.ClientOption{InitAddress: []string{cfg.valkeyAddr}})
			if err != nil {
				logrus.WithError(err).Warn("cloud-gateway: valkey client failed")
				return
			}
			vk = c
		})
		return vk
	}

	orgCache := newCloudOrgCache(cfg)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.nonprod {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
			if r.URL.Path == "/robots.txt" {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
				return
			}
			if cfg.blockBots && isBotUA(r.UserAgent()) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}

		countActivity := !isBotUA(r.UserAgent()) && !isPrefetch(r) && r.URL.Path != "/health" && r.URL.Path != "/healthz"
		if countActivity && cfg.tenantID != "" {
			go touchActivity(cfg, cfg.tenantID)
		}

		if !strings.HasPrefix(r.URL.Path, "/auth/v1/") {
			next.ServeHTTP(w, r)
			return
		}

		grant := r.URL.Query().Get("grant_type")
		buf := &bytes.Buffer{}
		rec := &captureResponse{ResponseWriter: w, status: 200, buf: buf}
		next.ServeHTTP(rec, r)

		if rec.status < 200 || rec.status >= 300 {
			return
		}
		if !meterEligibleAuth(r.Method, r.URL.Path, grant) {
			return
		}
		var payload map[string]any
		if json.Unmarshal(buf.Bytes(), &payload) != nil {
			return
		}
		user, _ := payload["user"].(map[string]any)
		if user == nil {
			return
		}
		go emitMAU(cfg, getVK, orgCache, cfg.tenantID, user)
	})
}

type captureResponse struct {
	http.ResponseWriter
	status int
	buf    *bytes.Buffer
}

func (c *captureResponse) WriteHeader(code int) {
	c.status = code
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureResponse) Write(b []byte) (int, error) {
	_, _ = c.buf.Write(b)
	return c.ResponseWriter.Write(b)
}

func touchActivity(cfg cloudGatewayCfg, tenantID string) {
	path := "/internal/activity/" + tenantID
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.activityBaseURL+path, nil)
	if err != nil {
		return
	}
	if cfg.internalSecret != "" {
		ts, sig := signInternal(cfg.internalSecret, "POST", path)
		req.Header.Set("X-Supatype-Internal-Ts", ts)
		req.Header.Set("X-Supatype-Internal-Sig", sig)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		logrus.WithError(err).Debug("cloud-gateway: activity touch failed")
		return
	}
	_ = res.Body.Close()
}

func signInternal(secret, method, path string) (ts, sig string) {
	t := strconv.FormatInt(time.Now().Unix(), 10)
	payload := t + "\n" + strings.ToUpper(method) + "\n" + path
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return t, "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func meterEligibleAuth(method, path, grant string) bool {
	if method == http.MethodPost && path == "/auth/v1/signup" {
		return true
	}
	if method == http.MethodPost && path == "/auth/v1/verify" {
		return true
	}
	if method == http.MethodPost && path == "/auth/v1/token" {
		switch grant {
		case "password", "id_token", "pkce":
			return true
		}
	}
	return false
}

func mauDedupeKey(emailSalt, projectRef string, user map[string]any) string {
	if ids, ok := user["identities"].([]any); ok {
		for _, raw := range ids {
			idm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			prov, _ := idm["provider"].(string)
			sub, _ := idm["identity_id"].(string)
			if sub == "" {
				sub, _ = idm["id"].(string)
			}
			if prov != "" && sub != "" {
				return "oidc:" + prov + ":" + sub
			}
		}
	}
	if em, ok := user["email"].(string); ok && strings.TrimSpace(em) != "" && emailSalt != "" {
		n := strings.ToLower(strings.TrimSpace(em))
		h := sha256.Sum256([]byte(n + "||" + emailSalt))
		return "email:" + hex.EncodeToString(h[:])
	}
	id, _ := user["id"].(string)
	if id == "" {
		id = "unknown"
	}
	return "local:" + projectRef + ":" + id
}

func emitMAU(cfg cloudGatewayCfg, getVK func() vkgo.Client, org *cloudOrgCache, tenantID string, user map[string]any) {
	if tenantID == "" {
		return
	}
	// Strip env suffix for org lookup (staging/preview share project org).
	projectRef := tenantID
	if i := strings.LastIndex(tenantID, "-"); i > 0 {
		suf := tenantID[i+1:]
		if suf == "staging" || suf == "preview" {
			projectRef = tenantID[:i]
		}
	}
	orgID, ok := org.resolve(projectRef)
	if !ok {
		return
	}
	day := time.Now().UTC().Format("2006-01-02")
	dk := mauDedupeKey(cfg.emailSalt, projectRef, user)
	key := "mau:org:" + orgID + ":d:" + day
	vk := getVK()
	if vk == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	exp := time.Now().UTC().AddDate(0, 0, 40)
	_ = vk.Do(ctx, vk.B().Sadd().Key(key).Member(dk).Build()).Error()
	_ = vk.Do(ctx, vk.B().Expireat().Key(key).Timestamp(exp.Unix()).Build()).Error()
}

type cloudOrgCache struct {
	mu   sync.RWMutex
	pos  map[string]cloudOrgEnt
	cfg  cloudGatewayCfg
	http *http.Client
}

type cloudOrgEnt struct {
	orgID string
	until time.Time
}

func newCloudOrgCache(cfg cloudGatewayCfg) *cloudOrgCache {
	return &cloudOrgCache{
		pos:  make(map[string]cloudOrgEnt),
		cfg:  cfg,
		http: &http.Client{Timeout: 150 * time.Millisecond},
	}
}

func (c *cloudOrgCache) resolve(ref string) (string, bool) {
	c.mu.RLock()
	if e, ok := c.pos[ref]; ok && time.Now().Before(e.until) {
		c.mu.RUnlock()
		return e.orgID, true
	}
	c.mu.RUnlock()

	path := "/internal/projects/" + ref + "/org"
	req, err := http.NewRequest(http.MethodGet, c.cfg.controlPlaneURL+path, nil)
	if err != nil {
		return "", false
	}
	if c.cfg.internalSecret != "" {
		ts, sig := signInternal(c.cfg.internalSecret, "GET", path)
		req.Header.Set("X-Supatype-Internal-Ts", ts)
		req.Header.Set("X-Supatype-Internal-Sig", sig)
	}
	res, err := c.http.Do(req)
	if err != nil || res.StatusCode != 200 {
		if res != nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
		return "", false
	}
	defer res.Body.Close()
	var body struct {
		OrgID string `json:"org_id"`
	}
	if json.NewDecoder(res.Body).Decode(&body) != nil || body.OrgID == "" {
		return "", false
	}
	c.mu.Lock()
	c.pos[ref] = cloudOrgEnt{orgID: body.OrgID, until: time.Now().Add(5 * time.Minute)}
	c.mu.Unlock()
	return body.OrgID, true
}
