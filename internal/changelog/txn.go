package changelog

import (
	"encoding/json"
	"fmt"
)

// Actor is who made the write, from the first supatype.actor marker of the transaction.
type Actor struct {
	Sub       string `json:"sub"`
	Role      string `json:"role"`
	Email     string `json:"email"`
	RequestID string `json:"requestId"`
	// Depth is how many hooks deep the request already was, so a hook whose own write fires
	// another after hook is counted like it is on the request path.
	Depth int `json:"depth"`
	// RequestPath is set when the request path fired this request's after hook itself, which it
	// does while the listener's status is still unsettled. A signed token naming the table, so the
	// listener skips that table here and does not deliver the write a second time.
	RequestPath string `json:"requestPath"`
}

// Change is one row change on a hooked table.
type Change struct {
	Schema string
	Table  string
	// Op is insert, update or delete.
	Op string
	// Row is the new row, nil for a delete.
	Row map[string]json.RawMessage
	// Old is the row before, for an update or delete. Complete only under REPLICA IDENTITY FULL,
	// which the engine sets on hooked tables.
	Old map[string]json.RawMessage
	// Key names the primary key columns, as wal2json reported them.
	Key []string
}

// Txn is one committed transaction, reduced to what the hooks need.
type Txn struct {
	// CommitLSN is the transaction's commit position, which names its deliveries.
	CommitLSN string
	// EndLSN is where the slot may move once this transaction is handled.
	EndLSN  string
	Actor   *Actor
	Changes []Change

	actorSeen bool
	skipSeen  bool
}

// Skipped reports whether the transaction asked to skip hooks and may.
//
// Any role can emit a message, so a marker proves nothing about who wrote it. The actor marker is
// trusted because the pre-request function emits it before any user code runs. A skip is honoured
// only where there is no actor at all, so an API request can never switch its own hooks off.
func (t *Txn) Skipped() bool {
	return t.skipSeen && !t.actorSeen
}

// assembler groups wal2json rows into transactions.
type assembler struct {
	wants func(schema, table string) bool
	open  *Txn
}

func newAssembler(wants func(schema, table string) bool) *assembler {
	return &assembler{wants: wants}
}

// feed takes one row and returns the transaction it completes, if it is a commit.
func (a *assembler) feed(data []byte) (*Txn, error) {
	var rec record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("decoding wal2json row: %w", err)
	}
	switch rec.Action {
	case "B":
		a.open = &Txn{CommitLSN: rec.LSN, EndLSN: rec.NextLSN}
		return nil, nil
	case "C":
		return a.commit(rec)
	case "M":
		return nil, a.message(rec)
	case "I", "U", "D":
		return nil, a.change(rec)
	}
	// Truncate ("T") and anything newer: nothing a row hook can be shown.
	return nil, nil
}

func (a *assembler) commit(rec record) (*Txn, error) {
	if a.open == nil {
		return nil, fmt.Errorf("commit at %s with no open transaction", rec.LSN)
	}
	txn := a.open
	a.open = nil
	if rec.LSN != "" {
		txn.CommitLSN = rec.LSN
	}
	if rec.NextLSN != "" {
		txn.EndLSN = rec.NextLSN
	}
	return txn, nil
}

func (a *assembler) message(rec record) error {
	if !rec.Transactional {
		// Not part of any transaction, so it cannot speak for one.
		return nil
	}
	if a.open == nil {
		return fmt.Errorf("transactional message at %s with no open transaction", rec.LSN)
	}
	switch rec.Prefix {
	case PrefixActor:
		a.actor(rec.Content)
	case PrefixHooks:
		if rec.Content == skipContent {
			a.open.skipSeen = true
		}
	}
	return nil
}

// actor records the first actor marker and ignores the rest, which only user code can have sent.
func (a *assembler) actor(content string) {
	if a.open.actorSeen {
		return
	}
	a.open.actorSeen = true
	var actor Actor
	if err := json.Unmarshal([]byte(content), &actor); err != nil {
		// Still counted as seen, so a garbled marker cannot open the door to a skip.
		return
	}
	a.open.Actor = &actor
}

func (a *assembler) change(rec record) error {
	if a.open == nil {
		return fmt.Errorf("%s on %s.%s with no open transaction", rec.Action, rec.Schema, rec.Table)
	}
	if !a.wants(rec.Schema, rec.Table) {
		return nil
	}
	c := Change{Schema: rec.Schema, Table: rec.Table, Op: opOf(rec.Action), Key: keyNames(rec.PK)}
	if rec.Action != "D" {
		c.Row = rowOf(rec.Columns)
	}
	if rec.Action != "I" {
		c.Old = rowOf(rec.Identity)
	}
	a.open.Changes = append(a.open.Changes, c)
	return nil
}
