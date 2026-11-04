// Package changelog feeds after hooks from the Postgres change log.
//
// A listener reads committed changes from its own logical replication slot with wal2json, keeps the
// changes on tables whose after hooks it owns, and calls those hooks through the functions worker.
// So an after hook sees every writer: the API, Studio, functions and plain SQL. One replica reads at
// a time, chosen by a Postgres advisory lock; the rest stand by and take over when it goes. The
// slot moves only after a change is delivered or its retries are spent, so a crash or a scale to
// zero re-reads from where it stopped rather than losing anything.
package changelog

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/sirupsen/logrus"
	"github.com/supatype/server/internal/modelhooks"
)

// Path is where after hooks get their rows.
type Path string

const (
	// PathUnknown: not decided yet, because no after hook exists or the database has not answered.
	// The request path serves until it is decided.
	PathUnknown Path = "unknown"
	// PathChangeLog: the listener delivers after hooks from the change log.
	PathChangeLog Path = "changelog"
	// PathRequest: logical replication is unavailable, so after hooks stay on the request path and
	// miss writes made outside the API.
	PathRequest Path = "request"
)

// Status is what /health reports about after hooks, for `supatype doctor`.
type Status struct {
	Path Path `json:"path"`
	// Reason says why the request path is in use.
	Reason string `json:"reason,omitempty"`
	// Role is "reader" on the replica holding the lock and "standby" elsewhere.
	Role string `json:"role,omitempty"`
	Slot string `json:"slot"`
	// RetainedWALBytes is the WAL the slot holds on disk, as of the reader's last batch.
	RetainedWALBytes int64  `json:"retained_wal_bytes,omitempty"`
	LastError        string `json:"last_error,omitempty"`
}

// Config wires a listener to the server.
type Config struct {
	DSN string
	// Schema is the schema the hook map's table names live in.
	Schema func() string
	Hooks  func() map[string]modelhooks.TableHooksView
	Shapes func() modelhooks.ShapeFunc
	// Resolve returns the URL to POST a named function to.
	Resolve    func(function string) (string, error)
	Dispatcher caller
	// Workers caps hook calls in flight. Default 8.
	Workers int
	// Poll is how long to wait when there is nothing to read. Default one second.
	Poll time.Duration
	// Slot overrides SlotName, for tests.
	Slot string
	// FiredOnRequest verifies a request-path mark from the actor marker and names the table whose
	// after hook the request path already fired. Wired to modelhooks.RequestPathMarks.Verify.
	FiredOnRequest func(token string) (string, bool)
}

// Listener is the change-log reader. Safe to share; Run it once.
type Listener struct {
	cfg    Config
	sender *sender

	mu     sync.RWMutex
	status Status
}

// New builds a listener with defaults filled in.
func New(cfg Config) *Listener {
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.Poll <= 0 {
		cfg.Poll = time.Second
	}
	if cfg.Slot == "" {
		cfg.Slot = SlotName
	}
	return &Listener{
		cfg:    cfg,
		sender: &sender{call: cfg.Dispatcher, resolve: cfg.Resolve, workers: cfg.Workers, attempts: 5, backoff: defaultBackoff},
		status: Status{Path: PathUnknown, Slot: cfg.Slot},
	}
}

// Status is a snapshot for health reporting.
func (l *Listener) Status() Status {
	if l == nil {
		return Status{Path: PathUnknown}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.status
}

// Active reports whether after hooks come from the change log. True on every replica once the
// database is known to support it, reader or standby alike, because then no replica may also fire
// them from the request path.
func (l *Listener) Active() bool {
	return l.Status().Path == PathChangeLog
}

// Run reads until ctx ends, reconnecting after failures, and returns early only when the database
// cannot support the listener at all.
func (l *Listener) Run(ctx context.Context) {
	for attempt := 0; ctx.Err() == nil; attempt++ {
		err := l.session(ctx)
		var unsupported *UnsupportedError
		if errors.As(err, &unsupported) {
			l.update(func(s *Status) { s.Path, s.Reason, s.Role = PathRequest, unsupported.Reason, "" })
			return
		}
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			l.update(func(s *Status) { s.LastError, s.Role = err.Error(), "" })
			Log().WithError(err).Warn("change-log session ended; reconnecting")
		}
		sleep(ctx, defaultBackoff(min(attempt, 4)))
	}
}

// Log is the logger every part of the after-hook listener writes through, its bootstrap included.
func Log() *logrus.Entry {
	return logrus.WithField("component", "after_hook_listener")
}

// update changes the status and logs once whenever the path changes.
func (l *Listener) update(fn func(*Status)) {
	l.mu.Lock()
	before := l.status.Path
	fn(&l.status)
	after := l.status
	l.mu.Unlock()
	if after.Path == before {
		return
	}
	log := Log().WithFields(logrus.Fields{"path": after.Path, "slot": after.Slot})
	switch after.Path {
	case PathChangeLog:
		log.Info("after hooks are delivered from the change log, so they see every writer")
	case PathRequest:
		log.WithField("reason", after.Reason).Warn("after hooks fall back to the request path and miss writes made outside the API; run `supatype db check`")
	}
}

// session is one connection's life: wait for work, make sure the slot exists, take the reader lock,
// read.
func (l *Listener) session(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, l.cfg.DSN)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	s := slot{conn: conn, name: l.cfg.Slot}

	if err := l.waitForWork(ctx, s); err != nil {
		return err
	}
	if err := s.ensure(ctx); err != nil {
		return err
	}
	l.update(func(st *Status) { st.Path, st.Reason, st.Role, st.LastError = PathChangeLog, "", "standby", "" })
	if err := l.waitForLock(ctx, s); err != nil {
		return err
	}
	l.update(func(st *Status) { st.Role = "reader" })
	return l.read(ctx, s)
}

// waitForWork holds off creating a slot until some table has an after hook the listener owns,
// because an unread slot only piles up WAL. A slot that already exists is drained regardless, so
// removing the last hook does not leave WAL stranded behind it.
func (l *Listener) waitForWork(ctx context.Context, s slot) error {
	for {
		if l.wanted() {
			return nil
		}
		exists, err := s.exists(ctx)
		if err != nil || exists {
			return err
		}
		if !sleep(ctx, 5*l.cfg.Poll) {
			return ctx.Err()
		}
	}
}

func (l *Listener) wanted() bool {
	b := l.builder()
	for table := range b.hooks {
		if b.handles(b.schema, table) {
			return true
		}
	}
	return false
}

// waitForLock stands by until this replica holds the reader lock.
func (l *Listener) waitForLock(ctx context.Context, s slot) error {
	for {
		held, err := s.tryLock(ctx)
		if err != nil || held {
			return err
		}
		if !sleep(ctx, 5*l.cfg.Poll) {
			return ctx.Err()
		}
	}
}

// read is the reader loop: one batch at a time, pausing while a schema push runs.
func (l *Listener) read(ctx context.Context, s slot) error {
	for {
		pushing, err := s.pushLockHeld(ctx)
		if err != nil {
			return err
		}
		n := 0
		if !pushing {
			if n, err = l.readBatch(ctx, s); err != nil {
				return err
			}
		}
		if wal, err := s.retainedWAL(ctx); err == nil {
			l.update(func(st *Status) { st.RetainedWALBytes = wal })
		}
		if n == 0 && !sleep(ctx, l.cfg.Poll) {
			return ctx.Err()
		}
	}
}

// readBatch delivers one batch and then moves the slot past it, returning how many deliveries it
// made. The slot does not move if delivery was cut short.
func (l *Listener) readBatch(ctx context.Context, s slot) (int, error) {
	b := l.builder()
	asm := newAssembler(b.handles)
	var plans []plan
	last, err := s.peek(ctx, func(data []byte) error {
		txn, err := asm.feed(data)
		if txn != nil {
			plans = append(plans, b.plan(txn))
		}
		return err
	})
	if err != nil || last == "" {
		return 0, err
	}
	// Rendered after the read, since the session cannot run a query while the slot is streaming.
	var owed []Delivery
	for _, p := range plans {
		p.render(ctx, pgRenderer{conn: s.conn}, renderFailed)
		owed = append(owed, p.deliveries()...)
	}
	if err := l.sender.sendAll(ctx, owed); err != nil {
		return 0, err
	}
	return len(owed) + 1, s.advance(ctx, last)
}

// builder snapshots the hook map and shapes for one batch, so a manifest reload mid-batch cannot
// judge half a transaction by one map and half by another.
func (l *Listener) builder() builder {
	schema := "public"
	if l.cfg.Schema != nil && l.cfg.Schema() != "" {
		schema = l.cfg.Schema()
	}
	shapes := modelhooks.ShapeFunc(func(string) (modelhooks.TableShape, bool) { return modelhooks.TableShape{}, false })
	if l.cfg.Shapes != nil {
		shapes = l.cfg.Shapes()
	}
	var hooks map[string]modelhooks.TableHooksView
	if l.cfg.Hooks != nil {
		hooks = l.cfg.Hooks()
	}
	return builder{schema: schema, hooks: hooks, shapes: shapes, newID: newRequestID, firedOnRequest: l.cfg.FiredOnRequest}
}

// renderFailed logs a group delivered with decoded rather than rendered values.
func renderFailed(table string, err error) {
	Log().WithField("table", table).WithError(err).Warn("could not render rows as PostgREST would; delivering them as decoded")
}

func newRequestID() string {
	return uuid.Must(uuid.NewV4()).String()
}
