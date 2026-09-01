// Package hibp checks passwords against the Have I Been Pwned "Pwned
// Passwords" corpus without ever sending the password anywhere.
//
// It works by k-anonymity: the password is hashed with SHA-1, only the first
// five hex characters of that hash go to the API, and the remaining
// thirty-five are matched locally against the range the API returns. Neither
// the password nor its full hash leaves this process.
//
// Vendored from github.com/supabase/hibp (MIT, see LICENSE in this directory)
// rather than imported, so the service carries no dependency named after the
// project it was forked from. The upstream is a few hundred lines with no
// dependencies of its own and has not changed since 2023.
package hibp

import (
	"bytes"
	"context"
	// SHA-1 is the hash the Pwned Passwords protocol is defined in terms of.
	// It is not protecting anything here: the corpus is indexed by SHA-1, so a
	// lookup has to speak SHA-1 or it cannot ask the question at all.
	"crypto/sha1" // #nosec G505 -- required by the Pwned Passwords API, not used as a security primitive
	"encoding/hex"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// PwnedPasswordsURL returns the URL for the prefix.
func PwnedPasswordsURL(prefix string) string {
	return "https://api.pwnedpasswords.com/range/" + prefix
}

// DefaultUserAgent is the User-Agent header sent to the Pwned Passwords API if
// it has not been explicitly set.
var DefaultUserAgent = "https://github.com/supatype/server"

// requestTimeout bounds a shared request. No single caller can cancel one any
// more, so it needs a limit of its own.
const requestTimeout = 30 * time.Second

// PwnedCache is the interface with which you can cache responses from the
// Pwned Passwords API.
type PwnedCache interface {
	// Add records the provided prefix and suffixes in the cache.
	Add(ctx context.Context, prefix []byte, suffixes [][]byte) error

	// Contains checks if the provided prefix and suffix are in the cache.
	Contains(ctx context.Context, prefix, suffix []byte) (bool, error)
}

// PwnedClient can be used to send requests to the Pwned Passwords API. Zero
// value is safe to use, though it is highly recommended you configure the
// UserAgent property per the HaveIBeenPwned.org API rules.
type PwnedClient struct {
	// UserAgent is sent as the User-Agent header to HTTP requests.
	UserAgent string

	// Cache, when set, will be used to cache and lookup results.
	Cache PwnedCache

	// HTTP allows you to override the HTTP client used. If not set http.DefaultClient is used.
	HTTP interface {
		Do(*http.Request) (*http.Response, error)
	}

	// lock is used to synchronize access when needed.
	lock sync.Mutex

	// requests holds the in-flight request per SHA1 prefix, so that
	// concurrent checks sharing a prefix share one call to the API. Guarded
	// by lock, as are the reference counts inside each entry.
	requests map[string]*inflightRequest
}

// pwnedResultBuffer is used on res.Body to hold the original response body
// from the Pwned Passwords API as well as the parsed suffixes.
type pwnedResultBuffer struct {
	Buffer         *bytes.Buffer
	SuffixesSorted bool
	Suffixes       [][]byte
}

func (b *pwnedResultBuffer) Read(into []byte) (int, error) {
	return b.Buffer.Read(into)
}

func (b *pwnedResultBuffer) Close() error {
	// do nothing
	return nil
}

// pwnedLinePattern encodes the regular expression for parsing lines returned
// from the Pwned Passwords API. Excerpt:
//
// > When a password hash with the same first 5 characters is found in the Pwned
// > Passwords repository, the API will respond with an HTTP 200 and include the
// > suffix of every hash beginning with the specified prefix, followed by a
// > count of how many times it appears in the data set. The API consumer can
// > then search the results of the response for the presence of their source
// > hash and if not found, the password does not exist in the data set. A
// > sample SHA-1 response for the hash prefix "21BD1" would be as follows:
// >
// > ```
// > 0018A45C4D1DEF81644B54AB7F969B88D65:1
// > 00D4F6E8FA6EECAD2A3AA415EEC418D38EC:2
// > 011053FD0102E94D6AE2F8B83D76FAF94F6:1
// > 012A7CA357541F0AC487871FEEC1891C49C:2
// > 0136E006E24E7D152139815FB0FC6A50B15:2
// > ...
// > ```
var pwnedLinePattern = regexp.MustCompile(`^([0-9A-F]{35}):([0-9]+)\s*$`)

// Parse parses the password suffixes from the buffer.
func (buf *pwnedResultBuffer) Parse() {
	defer buf.Buffer.Reset()

	buf.SuffixesSorted = true

	running := true

	for running {
		line, err := buf.Buffer.ReadBytes('\n')
		if err != nil {
			// err can only be io.EOF here, Buffer does not return
			// any other errors
			running = false
		}

		matches := pwnedLinePattern.FindSubmatch(line)
		if matches == nil {
			continue
		}

		suffix := matches[1]
		occurrence := matches[2]

		if buf.SuffixesSorted && len(buf.Suffixes) > 0 {
			if bytes.Compare(buf.Suffixes[len(buf.Suffixes)-1], suffix) >= 0 {
				buf.SuffixesSorted = false
			}
		}

		if len(occurrence) > 1 || (len(occurrence) == 1 && occurrence[0] != '0') {
			buf.Suffixes = append(buf.Suffixes, suffix)
		}
	}
}

// Lookup searches through the parsed suffixes.
func (buf *pwnedResultBuffer) Lookup(suffix []byte) bool {
	if !buf.SuffixesSorted {
		// Because the Pwned Passwords API does not explicitly claim
		// that the returned suffixes are sorted (though in practice
		// this appears to be the case), if parsing detected that
		// they're not sorted, the quickest way is to loop through all
		// suffixes.

		for _, s := range buf.Suffixes {
			if bytes.Equal(s, suffix) {
				return true
			}
		}

		return false
	}

	// Suffixes are sorted, so we can use binary search to quickly find
	// whether the suffix is in buf.Suffixes.

	suffixBytes := []byte(suffix)

	index := sort.Search(len(buf.Suffixes), func(i int) bool {
		return bytes.Compare(buf.Suffixes[i], suffixBytes) >= 0
	})

	if index < len(buf.Suffixes) {
		return bytes.Equal(suffixBytes, buf.Suffixes[index])
	}

	return false
}

// doRequest finally sends a request to the Pwned Passwords API and uses buf to
// read and parse the result into.
func (c *PwnedClient) doRequest(ctx context.Context, buf *pwnedResultBuffer, prefix []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PwnedPasswordsURL(string(prefix)), nil)
	if err != nil {
		return nil, err
	}

	userAgent := c.UserAgent

	if userAgent == "" {
		userAgent = DefaultUserAgent
	}

	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	res, err := client.Do(req)
	if err != nil {
		return res, err
	}

	originalBody := res.Body
	defer originalBody.Close()

	if res.StatusCode == http.StatusOK {
		_, err = buf.Buffer.ReadFrom(originalBody)
		if err != nil {
			return res, err
		}

		defer buf.Buffer.Reset()

		buf.Parse()
		if c.Cache != nil && len(buf.Suffixes) > 0 {
			if err := c.Cache.Add(ctx, prefix, buf.Suffixes); err != nil {
				return res, err
			}
		}

		res.Body = buf
	}

	return res, nil
}

// Check uses the Pwned Passwords API to check if the provided password is
// found in a breach. If two concurrent calls are made with passwords that
// share the same SHA1 prefix, only a single request will be sent.
//
// Cancelling ctx abandons this call. It does not cancel the shared request,
// because other callers may be waiting on it.
//
// Unexpected HTTPS responses will return ErrorUnexpectedResponse.
func (c *PwnedClient) Check(ctx context.Context, password string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	// #nosec G401 -- see the import: the API is indexed by SHA-1, and only the
	// first five characters of this digest ever leave the process.
	sum := sha1.Sum([]byte(password))
	hexsum := []byte(strings.ToUpper(hex.EncodeToString(sum[:])))
	prefix := hexsum[:5]
	suffix := hexsum[5:]

	if c.Cache != nil {
		contains, err := c.Cache.Contains(ctx, prefix, suffix)
		if err != nil {
			return contains, err
		}

		if contains {
			return true, nil
		}
	}

	key := string(prefix)
	req := c.acquire(ctx, key, prefix)
	defer c.release(key, req)

	select {
	case <-ctx.Done():
		// This caller gives up. The request carries on for whoever else is
		// waiting on it, and the last one out returns the buffers.
		return false, ctx.Err()
	case <-req.done:
	}

	if req.err != nil {
		return false, req.err
	}

	if req.res.StatusCode != http.StatusOK {
		return false, &ErrorUnexpectedResponse{
			Response: req.res,
		}
	}

	buf := req.res.Body.(*pwnedResultBuffer)

	return buf.Lookup(suffix), nil
}

// inflightRequest is one shared call to the Pwned Passwords API, and the
// pooled memory it parses into.
//
// refs counts the callers waiting on it plus the goroutine performing it, and
// is guarded by PwnedClient.lock rather than by an atomic: the count reaching
// zero and the map entry going away have to be one indivisible step, or a
// caller arriving in between would be handed buffers already on their way back
// to the pool.
type inflightRequest struct {
	done chan struct{}

	// res and err are written once, before done is closed.
	res *http.Response
	err error

	refs     int
	buffer   *bytes.Buffer
	suffixes *[][]byte
}

// acquire returns the in-flight request for prefix, starting one if there is
// none, with a reference held on behalf of the caller.
func (c *PwnedClient) acquire(ctx context.Context, key string, prefix []byte) *inflightRequest {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.requests == nil {
		c.requests = make(map[string]*inflightRequest)
	}

	if req, ok := c.requests[key]; ok {
		req.refs++
		return req
	}

	req := &inflightRequest{
		done:     make(chan struct{}),
		buffer:   bufferPool.Get().(*bytes.Buffer),
		suffixes: suffixesPool.Get().(*[][]byte),
		// One for this caller, one for the goroutine started below.
		refs: 2,
	}
	c.requests[key] = req
	c.start(ctx, key, req, prefix)

	return req
}

// start performs the request in the background, releasing the reference held
// on its behalf when it is done. Called with the lock held.
func (c *PwnedClient) start(ctx context.Context, key string, req *inflightRequest, prefix []byte) {
	// Detached from the caller's context, keeping its values so tracing still
	// works, because whoever happens to create the request does not own it:
	// their cancellation used to cancel it for everybody sharing the prefix.
	// Bounded on its own, since no caller can cut a hung request short now.
	reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)

	go func() {
		// Reverse order: the result has to be visible before anyone is woken,
		// and the buffers can only go back to the pool after that.
		defer c.release(key, req)
		defer close(req.done)
		defer cancel()

		req.res, req.err = c.doRequest(reqCtx, &pwnedResultBuffer{
			Buffer:   req.buffer,
			Suffixes: *req.suffixes,
		}, prefix)
	}()
}

// release drops one reference. The last one out removes the entry and returns
// the pooled memory, both under the lock so that acquire cannot revive it.
func (c *PwnedClient) release(key string, req *inflightRequest) {
	c.lock.Lock()
	defer c.lock.Unlock()

	req.refs--
	if req.refs > 0 {
		return
	}

	// Only if it is still ours: a later request for the same prefix will have
	// put its own entry here.
	if c.requests[key] == req {
		delete(c.requests, key)
	}

	bufferPool.Put(req.buffer)
	suffixesPool.Put(req.suffixes)
}
