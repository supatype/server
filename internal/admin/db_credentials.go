package admin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/supatype/server/internal/config"
	"github.com/supatype/server/internal/data/valkey"
	"github.com/supatype/server/internal/utilities"
)

type dbCredMeta struct {
	Status            string `json:"status"`
	Generation        int    `json:"generation"`
	LastRotatedAt     string `json:"last_rotated_at,omitempty"`
	FirstViewConsumed string `json:"first_view_consumed_at,omitempty"`
}

type encryptedSecret struct {
	Algorithm  string `json:"algorithm"`
	KeyVersion int    `json:"key_version"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type statusResponse struct {
	Mode              string `json:"mode"`
	PasswordStatus    string `json:"password_status"`
	CanReveal         bool   `json:"can_reveal"`
	Generation        int    `json:"generation"`
	LastRotatedAt     string `json:"last_rotated_at,omitempty"`
	FirstViewConsumed string `json:"first_view_consumed_at,omitempty"`
	Message           string `json:"message,omitempty"`
}

func credentialStatusHandler(cfg *config.Config, vc valkey.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode := cfg.Mode
		switch mode {
		case "managed":
			meta, err := loadMeta(r.Context(), vc, tenantRef(r))
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			utilities.WriteJSON(w, http.StatusOK, statusResponse{
				Mode:              "cloud",
				PasswordStatus:    meta.Status,
				CanReveal:         meta.Status == "available_once",
				Generation:        meta.Generation,
				LastRotatedAt:     meta.LastRotatedAt,
				FirstViewConsumed: meta.FirstViewConsumed,
			})
		case "standalone":
			utilities.WriteJSON(w, http.StatusOK, statusResponse{
				Mode:           "self_host",
				PasswordStatus: "operator_managed",
				CanReveal:      cfg.AllowSecretReadback && cfg.PostgresPassword != "",
				Generation:     1,
				Message:        "Database password is managed by your deployment secrets.",
			})
		default:
			utilities.WriteJSON(w, http.StatusOK, statusResponse{
				Mode:           "local",
				PasswordStatus: "available",
				CanReveal:      true,
				Generation:     1,
				Message:        "Database password is available in local environment config.",
			})
		}
	}
}

func credentialFirstViewHandler(cfg *config.Config, vc valkey.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch cfg.Mode {
		case "managed":
			ref := tenantRef(r)
			meta, err := loadMeta(r.Context(), vc, ref)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if meta.Status != "available_once" {
				writeErr(w, http.StatusConflict, "password is not available for first-view")
				return
			}
			pw, err := loadManagedSecret(r.Context(), vc, cfg.DBCredentialsKEK, ref, meta.Generation)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			meta.Status = "hidden"
			meta.FirstViewConsumed = time.Now().UTC().Format(time.RFC3339)
			if err := saveMeta(r.Context(), vc, ref, meta); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			// Best effort. The metadata already says hidden, so a second first-view
			// is refused whether or not the ciphertext goes.
			_ = vc.Del(r.Context(), secretKey(ref, meta.Generation))
			utilities.WriteJSON(w, http.StatusOK, map[string]string{"password": pw})
		case "standalone":
			if !cfg.AllowSecretReadback {
				writeErr(w, http.StatusForbidden, "secret readback disabled")
				return
			}
			pw := cfg.PostgresPassword
			if pw == "" {
				writeErr(w, http.StatusNotFound, "POSTGRES_PASSWORD is not set")
				return
			}
			utilities.WriteJSON(w, http.StatusOK, map[string]string{"password": pw})
		default:
			pw := cfg.PostgresPassword
			if pw == "" {
				pw = "postgres"
			}
			utilities.WriteJSON(w, http.StatusOK, map[string]string{"password": pw})
		}
	}
}

func credentialRotateHandler(cfg *config.Config, vc valkey.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Mode != "managed" {
			writeErr(w, http.StatusNotImplemented, "rotation is managed by your runtime/environment in this mode")
			return
		}
		ref := tenantRef(r)
		meta, err := loadMeta(r.Context(), vc, ref)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// loadMeta already floors the generation at 1, so incrementing cannot
		// produce a value below it.
		meta.Generation++
		newPassword := randomPassword(32)
		if err := saveManagedSecret(r.Context(), vc, cfg.DBCredentialsKEK, ref, meta.Generation, newPassword); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		meta.Status = "available_once"
		meta.LastRotatedAt = time.Now().UTC().Format(time.RFC3339)
		meta.FirstViewConsumed = ""
		if err := saveMeta(r.Context(), vc, ref, meta); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		utilities.WriteJSON(w, http.StatusOK, map[string]any{
			"password_status": "available_once",
			"generation":      meta.Generation,
			"last_rotated_at": meta.LastRotatedAt,
		})
	}
}

// marshalJSON is a seam. dbCredMeta and encryptedSecret are both plain data, so
// neither can fail to encode and the error branches below are unreachable in
// production. They are kept because writing a truncated record would lose the
// only note of which generation a tenant's password belongs to.
var marshalJSON = json.Marshal

func loadMeta(ctx context.Context, vc valkey.Client, ref string) (dbCredMeta, error) {
	if !vc.Available() {
		return dbCredMeta{}, valkey.ErrUnavailable
	}
	data, err := vc.GetBytes(ctx, metaKey(ref))
	if err != nil {
		return dbCredMeta{}, fmt.Errorf("read credentials metadata: %w", err)
	}
	// Nothing stored is a tenant that has never had a password generated, which
	// is the state "pending" describes.
	if len(data) == 0 {
		return dbCredMeta{Status: "pending", Generation: 1}, nil
	}
	var meta dbCredMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return dbCredMeta{}, fmt.Errorf("decode credentials metadata: %w", err)
	}
	if meta.Status == "" {
		meta.Status = "pending"
	}
	if meta.Generation < 1 {
		meta.Generation = 1
	}
	return meta, nil
}

func saveMeta(ctx context.Context, vc valkey.Client, ref string, meta dbCredMeta) error {
	payload, err := marshalJSON(meta)
	if err != nil {
		return err
	}
	return vc.SetBytes(ctx, metaKey(ref), payload, 0)
}

func saveManagedSecret(ctx context.Context, vc valkey.Client, kekBase64, ref string, generation int, password string) error {
	secret, err := encryptManagedSecret(kekBase64, ref, generation, password)
	if err != nil {
		return err
	}
	data, err := marshalJSON(secret)
	if err != nil {
		return err
	}
	return vc.SetBytes(ctx, secretKey(ref, generation), data, 0)
}

func loadManagedSecret(ctx context.Context, vc valkey.Client, kekBase64, ref string, generation int) (string, error) {
	data, err := vc.GetBytes(ctx, secretKey(ref, generation))
	if err != nil {
		return "", fmt.Errorf("read managed password for generation %d: %w", generation, err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("managed password not found for generation %d", generation)
	}
	var secret encryptedSecret
	if err := json.Unmarshal(data, &secret); err != nil {
		return "", err
	}
	return decryptManagedSecret(kekBase64, ref, generation, secret)
}

// aeadFor builds the cipher both directions use, from the key-encryption key
// the deployment configures.
func aeadFor(kekBase64 string) (cipher.AEAD, error) {
	key, err := base64.StdEncoding.DecodeString(kekBase64)
	if err != nil || len(key) != 32 {
		return nil, errors.New("SUPATYPE_DB_CREDENTIALS_KEK must be base64-encoded 32-byte key")
	}
	// A 32-byte key is always a valid AES-256 key, and a valid block always
	// makes a GCM, so neither can fail once the length above is checked.
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	return gcm, nil
}

// aad binds a ciphertext to the tenant and generation it was made for, so a
// secret cannot be moved between tenants or replayed from an older rotation.
func aad(ref string, generation int) []byte {
	return []byte(fmt.Sprintf("%s:%d", ref, generation))
}

func encryptManagedSecret(kekBase64, ref string, generation int, password string) (encryptedSecret, error) {
	gcm, err := aeadFor(kekBase64)
	if err != nil {
		return encryptedSecret{}, err
	}
	// crypto/rand.Read does not fail, for the same reason randomPassword does
	// not check it: a short read would be a broken runtime, not a condition to
	// handle here.
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	cipherText := gcm.Seal(nil, nonce, []byte(password), aad(ref, generation))
	return encryptedSecret{
		Algorithm:  "aes-256-gcm",
		KeyVersion: 1,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(cipherText),
	}, nil
}

func decryptManagedSecret(kekBase64, ref string, generation int, secret encryptedSecret) (string, error) {
	gcm, err := aeadFor(kekBase64)
	if err != nil {
		return "", err
	}
	nonce, err := base64.StdEncoding.DecodeString(secret.Nonce)
	if err != nil {
		return "", err
	}
	cipherText, err := base64.StdEncoding.DecodeString(secret.Ciphertext)
	if err != nil {
		return "", err
	}
	// Checked rather than left to Open, which panics on a wrong-length nonce
	// instead of returning an error. A stored secret that was truncated, or
	// written by something else, would otherwise take down the request.
	if len(nonce) != gcm.NonceSize() {
		return "", errors.New("failed to decrypt managed password")
	}
	plain, err := gcm.Open(nil, nonce, cipherText, aad(ref, generation))
	if err != nil {
		return "", errors.New("failed to decrypt managed password")
	}
	return string(plain), nil
}

// randomPassword returns n characters from an alphabet that survives being
// pasted into a connection string.
//
// It returns no error because it cannot fail: crypto/rand.Read does not return
// one, and a short read would be a broken runtime rather than a condition a
// caller could do anything about.
func randomPassword(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	rnd := make([]byte, n)
	_, _ = rand.Read(rnd)

	buf := make([]byte, n)
	for i := range buf {
		buf[i] = alphabet[int(rnd[i])%len(alphabet)]
	}
	return string(buf)
}

func tenantRef(r *http.Request) string {
	ref := r.Header.Get("X-Supatype-Tenant")
	if ref == "" {
		ref = "default"
	}
	return ref
}

func metaKey(ref string) string { return fmt.Sprintf("tenant:%s:dbcred:meta", ref) }
func secretKey(ref string, generation int) string {
	return fmt.Sprintf("tenant:%s:dbcred:secret:v%d", ref, generation)
}
