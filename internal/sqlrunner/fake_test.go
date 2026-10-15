package sqlrunner

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// A pool, a transaction and a result set that can be told to fail wherever a
// real one can.
//
// The runner's failure paths are the interesting ones: a pool that will not
// begin, a commit that will not land, a result set that stops halfway. Arranging
// each of those against a live Postgres means either racing it or
// not covering it. Stating them is both faster and exact.
//
// Everything pgx's interfaces require but this package never calls is present
// only to satisfy the compiler, and panics if it is ever reached, so a future
// change that starts using one of them is not silently given a zero value.

type fakePool struct {
	tx       *fakeTx
	beginErr error
	begins   int
}

func (p *fakePool) Begin(context.Context) (pgx.Tx, error) {
	p.begins++
	if p.beginErr != nil {
		return nil, p.beginErr
	}
	if p.tx == nil {
		p.tx = &fakeTx{}
	}
	return p.tx, nil
}

type fakeTx struct {
	execErr   error
	queryErr  error
	commitErr error
	rows      *fakeRows
	// reassignErr is returned by the Exec that hands objects to the owner.
	reassignErr error
	// queryRowErr is what the row QueryRow returns fails to Scan with.
	queryRowErr error

	execs      []execCall
	querySQL   string
	queryArgs  []any
	committed  bool
	rolledBack bool
	// calls is every statement and the commit, in the order they reached the
	// transaction, so a test can say what must come before what.
	calls []string
}

// execCall is one Exec as the transaction received it.
type execCall struct {
	sql  string
	args []any
}

func (t *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, execCall{sql: sql, args: args})
	t.calls = append(t.calls, sql)
	if sql == reassignToOwner && t.reassignErr != nil {
		return pgconn.CommandTag{}, t.reassignErr
	}
	return pgconn.CommandTag{}, t.execErr
}

func (t *fakeTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	t.querySQL = sql
	t.queryArgs = args
	t.calls = append(t.calls, sql)
	if t.queryErr != nil {
		return nil, t.queryErr
	}
	if t.rows == nil {
		t.rows = &fakeRows{}
	}
	return t.rows, nil
}

func (t *fakeTx) Commit(context.Context) error {
	t.committed = true
	t.calls = append(t.calls, "COMMIT")
	return t.commitErr
}

func (t *fakeTx) Rollback(context.Context) error {
	t.rolledBack = true
	return nil
}

func (t *fakeTx) Begin(context.Context) (pgx.Tx, error) { panic("not used") }
func (t *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("not used")
}
func (t *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { panic("not used") }
func (t *fakeTx) LargeObjects() pgx.LargeObjects                         { panic("not used") }
func (t *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("not used")
}
func (t *fakeTx) Conn() *pgx.Conn { panic("not used") }

// QueryRow answers the one single-row lookup this package makes, whether an
// extension is one the image ships, with a row whose Scan fails with
// queryRowErr. A lookup that succeeds is the live database's to answer, so
// without queryRowErr set this is a test that reached it by mistake.
func (t *fakeTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	t.calls = append(t.calls, sql)
	if t.queryRowErr == nil {
		panic("not used")
	}
	return fakeRow{err: t.queryRowErr}
}

type fakeRow struct{ err error }

func (r fakeRow) Scan(...any) error { return r.err }

type fakeRows struct {
	columns []string
	values  [][]any

	// valuesErr is returned by Values on the row at valuesErrAt.
	valuesErr   error
	valuesErrAt int
	// err is what Err reports once the set is exhausted.
	err error

	index  int
	closed bool
}

func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription {
	descriptions := make([]pgconn.FieldDescription, len(r.columns))
	for i, name := range r.columns {
		descriptions[i] = pgconn.FieldDescription{Name: name}
	}
	return descriptions
}

func (r *fakeRows) Next() bool {
	if r.index >= len(r.values) {
		return false
	}
	r.index++
	return true
}

func (r *fakeRows) Values() ([]any, error) {
	if r.valuesErr != nil && r.index-1 == r.valuesErrAt {
		return nil, r.valuesErr
	}
	return r.values[r.index-1], nil
}

func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) Close()     { r.closed = true }
func (r *fakeRows) Scan(...any) error {
	return errors.New("not used")
}
func (r *fakeRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *fakeRows) RawValues() [][]byte           { panic("not used") }
func (r *fakeRows) Conn() *pgx.Conn               { panic("not used") }

// The fakes have to be the things pgx's interfaces describe, or the tests below
// prove nothing about the real ones.
var (
	_ Pool     = (*fakePool)(nil)
	_ pgx.Tx   = (*fakeTx)(nil)
	_ pgx.Rows = (*fakeRows)(nil)
	_ pgx.Row  = fakeRow{}
)
