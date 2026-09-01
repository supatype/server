package validateclient

import (
	"context"
	"errors"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/supatype/server/internal/conf"
)

// fakeResolver answers lookups from a fixture instead of the internet.
//
// Anything not in the fixture is reported as genuinely absent, with the
// IsNotFound DNSError that isHostNotFound looks for, because "no such domain"
// is the answer most of the table depends on.
type fakeResolver struct {
	mx    map[string][]*net.MX
	hosts map[string][]string

	// err, when set for a name, is returned instead of an answer, for the
	// temporary and timeout paths that a real resolver rarely produces on cue.
	err map[string]error

	mu   sync.Mutex
	seen []string
}

func notFound(name string) error {
	return &net.DNSError{
		Err:        "no such host",
		Name:       name,
		IsNotFound: true,
	}
}

func (r *fakeResolver) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, name)
}

// Queried returns the names looked up, sorted and deduplicated.
func (r *fakeResolver) Queried() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	uniq := make(map[string]bool, len(r.seen))
	for _, n := range r.seen {
		uniq[n] = true
	}
	out := make([]string, 0, len(uniq))
	for n := range uniq {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (r *fakeResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	r.record(name)
	if err, ok := r.err[name]; ok {
		return nil, err
	}
	if mxs, ok := r.mx[name]; ok {
		return mxs, nil
	}
	return nil, notFound(name)
}

func (r *fakeResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	r.record(host)
	if err, ok := r.err[host]; ok {
		return nil, err
	}
	if addrs, ok := r.hosts[host]; ok {
		return addrs, nil
	}
	return nil, notFound(host)
}

// useResolver swaps the package resolver for the duration of the test.
func useResolver(t *testing.T, r dnsResolver) {
	t.Helper()
	previous := validateEmailResolver
	validateEmailResolver = r
	t.Cleanup(func() { validateEmailResolver = previous })
}

// These are the only names the validation table reaches DNS for, and the
// answers here are what the real resolver gives for them.
//
// They live under dnstest.supatype.io, a subtree that exists for no other
// reason, so nothing here depends on production mail or a website: changing
// the mail provider, or moving a site behind a CDN that adds an MX, can no
// longer fail a test about email addresses.
//
// Three of them are real records. TestLiveDNSStillMatchesTheFixture checks
// them against the authoritative nameservers, so this cannot quietly drift.
// The three failure names below are answered only by the fake, because a
// resolver cannot be asked to time out on demand.
const (
	// MX 10 mail.dnstest.supatype.io, and no A record: deliverable.
	fixtureHostWithMX = "mx.dnstest.supatype.io."

	// An A record and deliberately no MX. RFC 5321 says treat the host as its
	// own MX, so this must still be accepted.
	fixtureHostAOnly = "a.dnstest.supatype.io."

	// Deliberately absent. Its NXDOMAIN is the fixture, so nothing must ever
	// be created at this name.
	fixtureHostAbsent = "nx.dnstest.supatype.io."

	// A lookup that fails in a way that says nothing about deliverability. A
	// timeout must never be read as "this address is invalid".
	fixtureHostTimeout = "slow.dnstest.supatype.io."

	// Likewise a temporary failure, such as a SERVFAIL from an upstream.
	fixtureHostTemporary = "temp.dnstest.supatype.io."

	// A failure that is not a DNS error at all. The resolver can return one,
	// and it tells us nothing about the domain, so the address gets the
	// benefit of the doubt rather than being rejected.
	fixtureHostOpaqueError = "opaque.dnstest.supatype.io."
)

// fixtureResolver answers the validation table's lookups from recorded fact.
func fixtureResolver() *fakeResolver {
	return &fakeResolver{
		mx: map[string][]*net.MX{
			fixtureHostWithMX: {{Host: "mail.dnstest.supatype.io.", Pref: 10}},
		},
		hosts: map[string][]string{
			// RFC 5737 TEST-NET-1, so the address can never be routed to.
			fixtureHostAOnly: {"192.0.2.1"},
		},
		err: map[string]error{
			fixtureHostTimeout: &net.DNSError{
				Err:         "i/o timeout",
				Name:        fixtureHostTimeout,
				IsTimeout:   true,
				IsTemporary: true,
			},
			fixtureHostTemporary: &net.DNSError{
				Err:         "server misbehaving",
				Name:        fixtureHostTemporary,
				IsTemporary: true,
			},
			fixtureHostOpaqueError: errors.New("resolver exploded"),
		},
	}
}

// liveDNS reports whether the opt-in tests that use the real resolver should
// run. They need outbound DNS, which a sandboxed or offline CI will not have,
// so they are off unless asked for.
func liveDNS(t *testing.T) bool {
	t.Helper()
	if os.Getenv("SUPATYPE_TEST_LIVE_DNS") == "" {
		t.Skip("set SUPATYPE_TEST_LIVE_DNS=1 to run tests that query real DNS")
		return false
	}
	return true
}

// TestLiveDNSStillMatchesTheFixture checks the three claims fixtureResolver
// makes about the outside world.
//
// The unit tests are hermetic, which is what makes them trustworthy, and also
// what lets them keep passing after reality changes underneath them. This is
// the test that notices: it is the only place the real resolver is used, and
// it is opt-in so it cannot break an offline build.
func TestLiveDNSStillMatchesTheFixture(t *testing.T) {
	if !liveDNS(t) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Deliverable: MX records present.
	mxs, err := net.DefaultResolver.LookupMX(ctx, fixtureHostWithMX)
	if err != nil || len(mxs) == 0 {
		t.Errorf("%s is the fixture's deliverable host and should still have MX records: %d found, err %v",
			fixtureHostWithMX, len(mxs), err)
	}

	// The RFC 5321 fallback: no MX, but the host resolves.
	if _, err := net.DefaultResolver.LookupMX(ctx, fixtureHostAOnly); !isHostNotFound(err) {
		t.Errorf("%s is the fixture's no-MX host, but the lookup no longer says not-found: %v",
			fixtureHostAOnly, err)
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, fixtureHostAOnly)
	if err != nil || len(addrs) == 0 {
		t.Errorf("%s must still resolve for the RFC 5321 fallback case: %d addresses, err %v",
			fixtureHostAOnly, len(addrs), err)
	}

	// Absent, and it has to stay that way: its NXDOMAIN is the fixture, so
	// creating any record at this name would silently delete a test case.
	if _, err := net.DefaultResolver.LookupHost(ctx, fixtureHostAbsent); !isHostNotFound(err) {
		t.Errorf("%s must not exist, but the lookup no longer says not-found: %v",
			fixtureHostAbsent, err)
	}
}

// TestLiveResolverAcceptsARealAddress drives the real resolver through the
// validator, so the seam cannot hide a break in the actual lookup path.
func TestLiveResolverAcceptsARealAddress(t *testing.T) {
	if !liveDNS(t) {
		return
	}

	cfg := conf.MailerConfiguration{EmailValidationExtended: true}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validating configuration: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ev := newEmailValidator(cfg)
	if err := ev.Validate(ctx, "a@"+strings.TrimSuffix(fixtureHostWithMX, ".")); err != nil {
		t.Errorf("a real deliverable domain should validate: %v", err)
	}
}
