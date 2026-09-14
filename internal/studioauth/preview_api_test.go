package studioauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// A preview link is the only credential its holder has, so what the token does
// and does not carry is the whole security story. These pin both halves.

func TestPreviewTokenCarriesNoSubject(t *testing.T) {
	// The point of the design. `auth.uid()` reads `sub`, so a token carrying one
	// would make its bearer that person for the life of the link: they would pass
	// every `created_by = auth.uid()` and every `Owner<>` rule that person passes.
	// The preview claim must be the only thing that matches.
	token := mustSign(t, previewClaims{
		Scope:    PreviewScopeRecord,
		Model:    "posts",
		RecordID: "22222222-2222-2222-2222-222222222222",
		Epoch:    3,
		IssuedBy: "11111111-1111-1111-1111-111111111111",
		Expires:  time.Now().Add(time.Hour),
	})

	claims := parse(t, token)
	if _, present := claims["sub"]; present {
		t.Fatalf("a preview token must not carry a subject; got %v", claims["sub"])
	}
	if claims["role"] != "authenticated" {
		t.Errorf("role = %v, want authenticated: `anon` holds no USAGE on the draft schema",
			claims["role"])
	}
	if claims["iss_by"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("iss_by = %v, want the minter's id so a leak is attributable", claims["iss_by"])
	}
}

func TestPreviewTokenNamesTheOneRecordItCovers(t *testing.T) {
	token := mustSign(t, previewClaims{
		Scope:    PreviewScopeRecord,
		Model:    "posts",
		RecordID: "22222222-2222-2222-2222-222222222222",
		Epoch:    1,
		Expires:  time.Now().Add(time.Hour),
	})

	preview, ok := parse(t, token)["preview"].(map[string]interface{})
	if !ok {
		t.Fatal("no preview claim")
	}
	if preview["scope"] != "record" {
		t.Errorf("scope = %v", preview["scope"])
	}
	if preview["model"] != "posts" {
		t.Errorf("model = %v: without it, a record link for one model would open another",
			preview["model"])
	}
	if preview["record_id"] != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("record_id = %v", preview["record_id"])
	}
}

func TestPreviewTokenForAProjectNamesNoRecord(t *testing.T) {
	// A project link covers everything, so naming a record would be a claim the
	// policy would then have to ignore, and a reader would have to know that.
	token := mustSign(t, previewClaims{
		Scope:   PreviewScopeProject,
		Model:   "posts",
		Epoch:   1,
		Expires: time.Now().Add(time.Hour),
	})

	preview := parse(t, token)["preview"].(map[string]interface{})
	if preview["scope"] != "project" {
		t.Errorf("scope = %v", preview["scope"])
	}
	if _, present := preview["model"]; present {
		t.Error("a project link must not name a model")
	}
	if _, present := preview["record_id"]; present {
		t.Error("a project link must not name a record")
	}
}

func TestPreviewTokenCarriesTheRevocationEpoch(t *testing.T) {
	// A signed token cannot be recalled, only outwaited. The epoch is the only
	// thing that lets an operator pull every outstanding link at once, so a token
	// without it is a token nobody can withdraw.
	token := mustSign(t, previewClaims{
		Scope:   PreviewScopeProject,
		Epoch:   7,
		Expires: time.Now().Add(time.Hour),
	})
	preview := parse(t, token)["preview"].(map[string]interface{})
	if preview["epoch"] != float64(7) {
		t.Errorf("epoch = %v, want 7", preview["epoch"])
	}
}

func TestPreviewTokenExpires(t *testing.T) {
	expires := time.Now().Add(15 * time.Minute)
	token := mustSign(t, previewClaims{Scope: PreviewScopeProject, Expires: expires})
	got := parse(t, token)["exp"]
	if got != float64(expires.Unix()) {
		t.Errorf("exp = %v, want %v", got, expires.Unix())
	}
}

func TestPreviewTokenIsRejectedOnceExpired(t *testing.T) {
	// Expiry is enforced by whoever verifies the signature, which is PostgREST,
	// so nothing in the generated policies checks it. This asserts the property
	// those policies are relying on rather than a behaviour of ours.
	token := mustSign(t, previewClaims{
		Scope:   PreviewScopeProject,
		Expires: time.Now().Add(-time.Minute),
	})
	_, err := jwt.Parse(token, func(*jwt.Token) (interface{}, error) { return []byte("secret"), nil })
	if err == nil {
		t.Fatal("an expired preview token must not verify")
	}
}

func TestResolveTTLAppliesTheProjectDefault(t *testing.T) {
	settings := PublishingSettings{DefaultTTL: 900, MaxRecordTTL: 604800, MaxProjectTTL: 86400}
	got, err := resolveTTL(0, PreviewScopeRecord, settings)
	if err != nil || got != 900 {
		t.Fatalf("resolveTTL(0) = %d, %v; want 900", got, err)
	}
}

func TestResolveTTLRefusesRatherThanClamps(t *testing.T) {
	// A caller who asked for a week and silently received fifteen minutes would
	// hand out a link they believed in, and find out when the person they sent it
	// to said it had stopped working.
	settings := PublishingSettings{DefaultTTL: 900, MaxRecordTTL: 3600, MaxProjectTTL: 600}
	if _, err := resolveTTL(7200, PreviewScopeRecord, settings); err == nil {
		t.Error("a record TTL past the ceiling must be refused")
	}
	if _, err := resolveTTL(1800, PreviewScopeProject, settings); err == nil {
		t.Error("a project link has its own, shorter ceiling")
	}
	if _, err := resolveTTL(1800, PreviewScopeRecord, settings); err != nil {
		t.Errorf("the same TTL is fine for a record link: %v", err)
	}
}

func TestResolveTTLClampsADefaultAboveTheCeiling(t *testing.T) {
	// The one place clamping is right: the caller asked for nothing, so there is
	// nobody to disappoint, and minting a link the same config refuses to honour
	// would be worse.
	settings := PublishingSettings{DefaultTTL: 604800, MaxRecordTTL: 3600, MaxProjectTTL: 600}
	got, err := resolveTTL(0, PreviewScopeProject, settings)
	if err != nil || got != 600 {
		t.Fatalf("resolveTTL(0, project) = %d, %v; want 600", got, err)
	}
}

func TestPublishingSettingsComeFromTheAdminConfig(t *testing.T) {
	path := writeAdminConfig(t, `{
      "adminRoles": ["admin"],
      "publishing": {
        "draftVisibility": ["admin", "developer", "editor"],
        "previewDefaultTtl": 900,
        "previewMaxRecordTtl": 604800,
        "previewMaxProjectTtl": 86400,
        "previewAllowProjectScope": true
      }
    }`)

	settings, err := PublishingSettingsFromConfigFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if !settings.RoleSeesDrafts("editor") {
		t.Error("editor must be on the visibility list this config states")
	}
	if settings.RoleSeesDrafts("viewer") {
		t.Error("a role the config does not name must not see drafts")
	}
	if settings.MaxProjectTTL != 86400 {
		t.Errorf("MaxProjectTTL = %d", settings.MaxProjectTTL)
	}
	if !settings.AllowProjectScope {
		t.Error("AllowProjectScope should be true")
	}
}

func TestPublishingSettingsFillInMissingBounds(t *testing.T) {
	// This process reads a file a *previous* push wrote, so an older engine sends
	// fewer keys. A zero ceiling would mint links that have already expired.
	path := writeAdminConfig(t, `{"publishing": {"draftVisibility": ["admin"]}}`)
	settings, err := PublishingSettingsFromConfigFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if settings.DefaultTTL != defaultPreviewTTL ||
		settings.MaxRecordTTL != defaultMaxRecordTTL ||
		settings.MaxProjectTTL != defaultMaxProjectTTL {
		t.Errorf("bounds not defaulted: %+v", settings)
	}
	if !settings.AllowProjectScope {
		t.Error("an absent allow flag means allowed, matching the CLI's default")
	}
}

func TestPublishingSettingsAbsentOnAProjectWithNoVersionedModel(t *testing.T) {
	// Not an error condition so much as an answer, and the endpoints turn it into
	// "there are no drafts to preview" rather than a 404 that reads as a broken
	// route.
	path := writeAdminConfig(t, `{"adminRoles": ["admin"]}`)
	if _, err := PublishingSettingsFromConfigFile(path); err == nil {
		t.Fatal("a config with no publishing block must report that")
	}
	if _, err := PublishingSettingsFromConfigFile("nowhere.json"); err == nil {
		t.Fatal("a missing config must report that too")
	}
}

func TestProjectScopeCanBeTurnedOff(t *testing.T) {
	path := writeAdminConfig(t,
		`{"publishing": {"draftVisibility": ["admin"], "previewAllowProjectScope": false}}`)
	settings, err := PublishingSettingsFromConfigFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	if settings.AllowProjectScope {
		t.Error("a project that switched project-wide links off must get them off")
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func mustSign(t *testing.T, claims previewClaims) string {
	t.Helper()
	token, err := signPreviewToken("secret", claims)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func parse(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	parsed, err := jwt.Parse(token,
		func(*jwt.Token) (interface{}, error) { return []byte("secret"), nil },
		jwt.WithoutClaimsValidation())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("claims are not a map")
	}
	return claims
}

// writeAdminConfig writes a config into the working directory, because
// ReadAdminConfigFile deliberately refuses an absolute path and anything that
// escapes the directory it runs in.
func writeAdminConfig(t *testing.T, body string) string {
	t.Helper()
	if !json.Valid([]byte(body)) {
		t.Fatalf("test fixture is not valid JSON: %s", body)
	}
	dir := t.TempDir()
	name := "admin-config.json"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	previous := openWorkingDirectory
	openWorkingDirectory = func() (*os.Root, error) { return os.OpenRoot(dir) }
	t.Cleanup(func() { openWorkingDirectory = previous })
	return name
}
