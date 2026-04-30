package admin

import (
	"encoding/base64"
	"testing"
)

func TestRandomPasswordLength(t *testing.T) {
	t.Parallel()
	pw, err := randomPassword(24)
	if err != nil {
		t.Fatalf("randomPassword returned error: %v", err)
	}
	if len(pw) != 24 {
		t.Fatalf("expected length 24, got %d", len(pw))
	}
}

func TestManagedSecretRoundTrip(t *testing.T) {
	t.Parallel()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	kek := base64.StdEncoding.EncodeToString(key)

	// Unit-level sanity check by encrypting/decrypting with fixed project/generation.
	// This mirrors the exact cipher/AAD path used for Valkey payloads.
	ref := "tenant_123"
	gen := 2
	pw := "MyS3cretPassw0rd!"

	enc, err := encryptManagedSecret(kek, ref, gen, pw)
	if err != nil {
		t.Fatalf("encryptManagedSecret error: %v", err)
	}
	dec, err := decryptManagedSecret(kek, ref, gen, enc)
	if err != nil {
		t.Fatalf("decryptManagedSecret error: %v", err)
	}
	if dec != pw {
		t.Fatalf("expected %q, got %q", pw, dec)
	}
}
