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
	"io"
	"net/http"
	"os"
	"time"

	"github.com/supatype/auth/internal/serverconf"
	"github.com/supatype/auth/internal/valkey"
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

func credentialStatusHandler(cfg *serverconf.ServerConfig, vc *valkey.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mode := cfg.Mode
		switch mode {
		case "managed":
			meta, err := loadMeta(r.Context(), vc, tenantRef(r))
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, 200, statusResponse{
				Mode:              "cloud",
				PasswordStatus:    meta.Status,
				CanReveal:         meta.Status == "available_once",
				Generation:        meta.Generation,
				LastRotatedAt:     meta.LastRotatedAt,
				FirstViewConsumed: meta.FirstViewConsumed,
			})
		case "standalone":
			writeJSON(w, 200, statusResponse{
				Mode:           "self_host",
				PasswordStatus: "operator_managed",
				CanReveal:      cfg.AllowSecretReadback && os.Getenv("POSTGRES_PASSWORD") != "",
				Generation:     1,
				Message:        "Database password is managed by your deployment secrets.",
			})
		default:
			writeJSON(w, 200, statusResponse{
				Mode:           "local",
				PasswordStatus: "available",
				CanReveal:      true,
				Generation:     1,
				Message:        "Database password is available in local environment config.",
			})
		}
	}
}

func credentialFirstViewHandler(cfg *serverconf.ServerConfig, vc *valkey.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch cfg.Mode {
		case "managed":
			ref := tenantRef(r)
			meta, err := loadMeta(r.Context(), vc, ref)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			if meta.Status != "available_once" {
				writeJSON(w, 409, map[string]string{"error": "password is not available for first-view"})
				return
			}
			pw, err := loadManagedSecret(r.Context(), vc, cfg.DBCredentialsKEK, ref, meta.Generation)
			if err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			meta.Status = "hidden"
			meta.FirstViewConsumed = time.Now().UTC().Format(time.RFC3339)
			if err := saveMeta(r.Context(), vc, ref, meta); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			_ = vc.Del(r.Context(), secretKey(ref, meta.Generation))
			writeJSON(w, 200, map[string]string{"password": pw})
		case "standalone":
			if !cfg.AllowSecretReadback {
				writeJSON(w, 403, map[string]string{"error": "secret readback disabled"})
				return
			}
			pw := os.Getenv("POSTGRES_PASSWORD")
			if pw == "" {
				writeJSON(w, 404, map[string]string{"error": "POSTGRES_PASSWORD is not set"})
				return
			}
			writeJSON(w, 200, map[string]string{"password": pw})
		default:
			pw := os.Getenv("POSTGRES_PASSWORD")
			if pw == "" {
				pw = "postgres"
			}
			writeJSON(w, 200, map[string]string{"password": pw})
		}
	}
}

func credentialRotateHandler(cfg *serverconf.ServerConfig, vc *valkey.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Mode != "managed" {
			writeJSON(w, 501, map[string]string{"error": "rotation is managed by your runtime/environment in this mode"})
			return
		}
		ref := tenantRef(r)
		meta, err := loadMeta(r.Context(), vc, ref)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		meta.Generation++
		if meta.Generation < 1 {
			meta.Generation = 1
		}
		newPassword, err := randomPassword(32)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if err := saveManagedSecret(r.Context(), vc, cfg.DBCredentialsKEK, ref, meta.Generation, newPassword); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		meta.Status = "available_once"
		meta.LastRotatedAt = time.Now().UTC().Format(time.RFC3339)
		meta.FirstViewConsumed = ""
		if err := saveMeta(r.Context(), vc, ref, meta); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{
			"password_status": "available_once",
			"generation":      meta.Generation,
			"last_rotated_at": meta.LastRotatedAt,
		})
	}
}

func loadMeta(ctx context.Context, vc *valkey.Client, ref string) (dbCredMeta, error) {
	if vc == nil {
		return dbCredMeta{}, errors.New("valkey client not configured")
	}
	data, err := vc.GetBytes(ctx, metaKey(ref))
	if err != nil || len(data) == 0 {
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

func saveMeta(ctx context.Context, vc *valkey.Client, ref string, meta dbCredMeta) error {
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return vc.SetBytes(ctx, metaKey(ref), payload, 0)
}

func saveManagedSecret(ctx context.Context, vc *valkey.Client, kekBase64, ref string, generation int, password string) error {
	secret, err := encryptManagedSecret(kekBase64, ref, generation, password)
	if err != nil {
		return err
	}
	data, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	return vc.SetBytes(ctx, secretKey(ref, generation), data, 0)
}

func loadManagedSecret(ctx context.Context, vc *valkey.Client, kekBase64, ref string, generation int) (string, error) {
	data, err := vc.GetBytes(ctx, secretKey(ref, generation))
	if err != nil || len(data) == 0 {
		return "", fmt.Errorf("managed password not found for generation %d", generation)
	}
	var secret encryptedSecret
	if err := json.Unmarshal(data, &secret); err != nil {
		return "", err
	}
	return decryptManagedSecret(kekBase64, ref, generation, secret)
}

func encryptManagedSecret(kekBase64, ref string, generation int, password string) (encryptedSecret, error) {
	key, err := base64.StdEncoding.DecodeString(kekBase64)
	if err != nil || len(key) != 32 {
		return encryptedSecret{}, errors.New("SUPATYPE_DB_CREDENTIALS_KEK must be base64-encoded 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return encryptedSecret{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return encryptedSecret{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return encryptedSecret{}, err
	}
	aad := []byte(fmt.Sprintf("%s:%d", ref, generation))
	cipherText := gcm.Seal(nil, nonce, []byte(password), aad)
	return encryptedSecret{
		Algorithm:  "aes-256-gcm",
		KeyVersion: 1,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(cipherText),
	}, nil
}

func decryptManagedSecret(kekBase64, ref string, generation int, secret encryptedSecret) (string, error) {
	key, err := base64.StdEncoding.DecodeString(kekBase64)
	if err != nil || len(key) != 32 {
		return "", errors.New("SUPATYPE_DB_CREDENTIALS_KEK must be base64-encoded 32-byte key")
	}
	nonce, err := base64.StdEncoding.DecodeString(secret.Nonce)
	if err != nil {
		return "", err
	}
	cipherText, err := base64.StdEncoding.DecodeString(secret.Ciphertext)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	aad := []byte(fmt.Sprintf("%s:%d", ref, generation))
	plain, err := gcm.Open(nil, nonce, cipherText, aad)
	if err != nil {
		return "", errors.New("failed to decrypt managed password")
	}
	return string(plain), nil
}

func randomPassword(n int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	buf := make([]byte, n)
	rnd := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, rnd); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(rnd[i])%len(alphabet)]
	}
	return string(buf), nil
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
