package apiconfig

import (
	"context"
	"errors"
	"testing"
)

// The bug this type fixes is a silent forget: `.supatype/api-config.json` lives on a managed pod's
// own filesystem with no volume mounted, so a restart brought the process back with
// DefaultApiConfig() and turned the REST response cache off for the whole project, with no event
// anywhere. So the test that matters is not "Set then Get on the same object" — it is "a brand new
// store, as a restarted process would build, still sees what was written".

type fakeKV struct {
	data      map[string][]byte
	available bool
	getErr    error
	setErr    error
	lastTTL   int
	sets      int
}

func newKV() *fakeKV { return &fakeKV{data: map[string][]byte{}, available: true} }

func (f *fakeKV) Available() bool { return f.available }

func (f *fakeKV) GetBytes(_ context.Context, key string) ([]byte, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.data[key], nil
}

func (f *fakeKV) SetBytes(_ context.Context, key string, value []byte, ttl int) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.sets++
	f.lastTTL = ttl
	f.data[key] = value
	return nil
}

func configWithCachedTable(table string) ApiConfig {
	cfg := DefaultApiConfig()
	cfg.Rest.CacheMaxTTL = 60
	cfg.Rest.CacheTables = map[string]RestTableCacheConfig{table: {Enabled: true}}
	return cfg
}

func TestTheConfigSurvivesARestart(t *testing.T) {
	kv := newKV()
	if err := NewKeyspaceStore(kv, "abc").Set(context.Background(), configWithCachedTable("posts")); err != nil {
		t.Fatal(err)
	}

	// A new store over the same keyspace: what a restarted pod builds.
	restarted := NewKeyspaceStore(kv, "abc")
	got, err := restarted.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Rest.CacheTables["posts"].Enabled {
		t.Fatalf("the cache allowlist did not survive: %+v", got.Rest.CacheTables)
	}
	if got.Rest.CacheMaxTTL != 60 {
		t.Errorf("cache_max_ttl = %d, want 60", got.Rest.CacheMaxTTL)
	}
}

func TestEachProjectGetsItsOwnKey(t *testing.T) {
	// One shared key would hand every tenant the last one's settings.
	kv := newKV()
	if err := NewKeyspaceStore(kv, "aaa").Set(context.Background(), configWithCachedTable("posts")); err != nil {
		t.Fatal(err)
	}
	other, err := NewKeyspaceStore(kv, "bbb").Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Rest.CacheTables) != 0 {
		t.Fatalf("another project saw %+v", other.Rest.CacheTables)
	}
	if _, ok := kv.data["tenant:aaa:api_config"]; !ok {
		t.Fatalf("keys = %v, want tenant:aaa:api_config", keysOf(kv))
	}
}

func keysOf(kv *fakeKV) []string {
	out := make([]string, 0, len(kv.data))
	for k := range kv.data {
		out = append(out, k)
	}
	return out
}

func TestAProjectNeverConfiguredReadsTheDefault(t *testing.T) {
	// An absent key is the same thing to this store as an absent file is to FileStore: not an error.
	got, err := NewKeyspaceStore(newKV(), "fresh").Get(context.Background())
	if err != nil {
		t.Fatalf("an unconfigured project should not error: %v", err)
	}
	if got.Rest.Schema != "public" || got.Rest.MaxRows != 1000 {
		t.Fatalf("config = %+v, want the default", got.Rest)
	}
}

func TestItIsWrittenWithNoTTL(t *testing.T) {
	// Configuration, not cache. An expiring API config would turn caching off on a timer, which is
	// this very bug wearing a different hat.
	kv := newKV()
	if err := NewKeyspaceStore(kv, "abc").Set(context.Background(), DefaultApiConfig()); err != nil {
		t.Fatal(err)
	}
	if kv.lastTTL != 0 {
		t.Fatalf("ttl = %d, want none", kv.lastTTL)
	}
}

func TestAnUnavailableKeyspaceIsReportedRatherThanSilentlyDefaulting(t *testing.T) {
	// Reporting matters more than the value here. Returning the default with a nil error is what
	// the file store did on a managed pod, and it is why the bug went unnoticed: the config simply
	// looked like a project that had never configured anything.
	kv := newKV()
	kv.available = false
	s := NewKeyspaceStore(kv, "abc")

	if _, err := s.Get(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Get err = %v, want ErrUnavailable", err)
	}
	if err := s.Set(context.Background(), DefaultApiConfig()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Set err = %v, want ErrUnavailable", err)
	}
	if kv.sets != 0 {
		t.Error("wrote to an unavailable keyspace")
	}
}

func TestAReadFailureIsNotMistakenForAnEmptyConfig(t *testing.T) {
	kv := newKV()
	kv.getErr = errors.New("connection reset")
	if _, err := NewKeyspaceStore(kv, "abc").Get(context.Background()); err == nil {
		t.Fatal("a failed read reported success")
	}
}

func TestAWriteFailureIsReported(t *testing.T) {
	kv := newKV()
	kv.setErr = errors.New("connection reset")
	if err := NewKeyspaceStore(kv, "abc").Set(context.Background(), DefaultApiConfig()); err == nil {
		t.Fatal("a failed write reported success")
	}
}

func TestCorruptStoredJSONIsReported(t *testing.T) {
	kv := newKV()
	kv.data["tenant:abc:api_config"] = []byte("{not json")
	if _, err := NewKeyspaceStore(kv, "abc").Get(context.Background()); err == nil {
		t.Fatal("unparseable config reported success")
	}
}

func TestANilStoreOrClientDoesNotPanic(t *testing.T) {
	// The selection in the gateway can hand this a nil client if the keyspace wiring changes; a
	// panic in a constructor path takes the whole pod down at boot.
	var nilStore *KeyspaceStore
	if _, err := nilStore.Get(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil store Get = %v", err)
	}
	if err := nilStore.Set(context.Background(), DefaultApiConfig()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("nil store Set = %v", err)
	}
	if _, err := NewKeyspaceStore(nil, "abc").Get(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Error("nil client Get should report unavailable")
	}
}

func TestItSatisfiesTheStoreInterface(t *testing.T) {
	var _ Store = (*KeyspaceStore)(nil)
	var _ Store = (*FileStore)(nil)
}

func TestAnUnmarshallableConfigIsReportedRatherThanWrittenTruncated(t *testing.T) {
	// The same seam FileStore's test uses. ApiConfig is plain data so this cannot fail in
	// production, but a store that reports success having written nothing is the failure mode this
	// whole type exists to remove — it should not be reachable by any path, including this one.
	original := marshalConfig
	t.Cleanup(func() { marshalConfig = original })
	marshalConfig = func(ApiConfig) ([]byte, error) { return nil, errors.New("nope") }

	kv := newKV()
	if err := NewKeyspaceStore(kv, "abc").Set(context.Background(), DefaultApiConfig()); err == nil {
		t.Fatal("a failed marshal reported success")
	}
	if kv.sets != 0 {
		t.Error("wrote despite the marshal failing")
	}
}
