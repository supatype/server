package changelog

import (
	"context"
	"encoding/json"

	"github.com/supatype/server/internal/modelhooks"
)

// Delivery is one hook call: every change of one operation on one table in one transaction.
type Delivery struct {
	Event    string
	Function string
	View     modelhooks.HookConfigView
	// Depth is stamped on the call, so a hook whose own write fires another hook is counted.
	Depth      int
	DeliveryID string
	Body       []byte
}

// payload is what the functions worker receives, per the build contract.
type payload struct {
	Table      string                       `json:"table"`
	Operation  string                       `json:"operation"`
	User       *modelhooks.Claims           `json:"user"`
	RequestID  string                       `json:"requestId"`
	DeliveryID string                       `json:"deliveryId"`
	Rows       []map[string]json.RawMessage `json:"rows,omitempty"`
	Old        []map[string]json.RawMessage `json:"old,omitempty"`
}

// builder turns a transaction into the deliveries it owes.
type builder struct {
	// schema is the one the hook map's table names live in.
	schema string
	hooks  map[string]modelhooks.TableHooksView
	shapes modelhooks.ShapeFunc
	newID  func() string
	// firedOnRequest reads a request-path token from the actor marker and returns the table whose
	// after hook the request path already fired, if the token is genuine. Nil means none are.
	firedOnRequest func(token string) (string, bool)
}

// handles reports whether the listener owns a table's after hooks.
//
// A table with no shape in the manifest is left to the request path. Without its restricted
// columns the listener cannot strip them, and guessing would mean either leaking a column or
// sending a row with holes nobody asked for.
func (b builder) handles(schema, table string) bool {
	if schema != b.schema {
		return false
	}
	view, ok := b.hooks[table]
	if !ok || !hasAfterHook(view) {
		return false
	}
	_, known := b.shapes(table)
	return known
}

func hasAfterHook(view modelhooks.TableHooksView) bool {
	return view[modelhooks.EventAfterChange].Function != "" || view[modelhooks.EventAfterDelete].Function != ""
}

// group is the changes of one operation on one table in one transaction, which is one hook call.
type group struct {
	schema, table, op, event string
	cfg                      modelhooks.HookConfigEntry
	shape                    modelhooks.TableShape
	// rows and old are aligned by index: old[i] is the row rows[i] replaced.
	rows, old []map[string]json.RawMessage
}

// plan is what a committed transaction owes, before its values are rendered.
type plan struct {
	commitLSN string
	who       identity
	groups    []*group
}

// plan groups a transaction's changes by table and operation, in the order each first appears.
func (b builder) plan(txn *Txn) plan {
	p := plan{commitLSN: txn.CommitLSN}
	if txn.Skipped() || len(txn.Changes) == 0 {
		return p
	}
	p.who = b.identity(txn.Actor)
	skipTable := b.requestPathTable(txn.Actor)
	index := map[string]*group{}
	for _, c := range txn.Changes {
		if c.Table == skipTable {
			continue
		}
		g := index[c.Table+"\x00"+c.Op]
		if g == nil {
			if g = b.newGroup(c); g == nil {
				continue
			}
			index[c.Table+"\x00"+c.Op] = g
			p.groups = append(p.groups, g)
		}
		g.add(c)
	}
	return p
}

// requestPathTable is the table whose after hook the request path already fired for this
// transaction, or "" for none. The request path fires while the listener's status is still being
// settled, and without this the same write would be delivered twice.
func (b builder) requestPathTable(actor *Actor) string {
	if actor == nil || actor.RequestPath == "" || b.firedOnRequest == nil {
		return ""
	}
	table, ok := b.firedOnRequest(actor.RequestPath)
	if !ok {
		return ""
	}
	return table
}

func (b builder) newGroup(c Change) *group {
	event := eventFor(c.Op)
	cfg := b.hooks[c.Table][event]
	shape, known := b.shapes(c.Table)
	if cfg.Function == "" || !known || c.Schema != b.schema {
		return nil
	}
	return &group{schema: c.Schema, table: c.Table, op: c.Op, event: event, cfg: cfg, shape: shape}
}

func (g *group) add(c Change) {
	if c.Row != nil {
		g.rows = append(g.rows, c.Row)
	}
	if c.Op != "insert" {
		g.old = append(g.old, oldRow(c))
	}
}

// renderer turns decoded rows into the JSON PostgREST would return for them.
type renderer interface {
	render(ctx context.Context, schema, table string, rows []map[string]json.RawMessage) ([]map[string]json.RawMessage, error)
}

// render rewrites every group's rows through r. A group that cannot be rendered, say its table was
// dropped since the change, keeps the values as decoded rather than losing its delivery.
func (p plan) render(ctx context.Context, r renderer, onError func(table string, err error)) {
	for _, g := range p.groups {
		if rows, err := renderAll(ctx, r, g, g.rows); err == nil {
			g.rows = rows
		} else {
			onError(g.table, err)
		}
		if old, err := renderAll(ctx, r, g, g.old); err == nil {
			g.old = old
		} else {
			onError(g.table, err)
		}
	}
}

func renderAll(ctx context.Context, r renderer, g *group, rows []map[string]json.RawMessage) ([]map[string]json.RawMessage, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	return r.render(ctx, g.schema, g.table, rows)
}

// deliveries encodes the plan's groups as hook calls.
func (p plan) deliveries() []Delivery {
	out := make([]Delivery, 0, len(p.groups))
	for _, g := range p.groups {
		if d, ok := p.encode(g); ok {
			out = append(out, d)
		}
	}
	return out
}

func (p plan) encode(g *group) (Delivery, bool) {
	// deliveryId names one call: commit LSN, table and operation. A transaction has one call per
	// table and operation, so the id is unique, and stable on redelivery.
	id := p.commitLSN + ":" + g.schema + "." + g.table + ":" + g.op
	body, err := json.Marshal(payload{
		Table: g.table, Operation: g.op, User: p.who.user, RequestID: p.who.requestID, DeliveryID: id,
		Rows: g.shape.StripAll(g.event, g.rows), Old: g.shape.StripAll(g.event, g.old),
	})
	if err != nil {
		// Built from rows already encoded as JSON, so this cannot fail. Skipping beats sending a
		// hook half a payload.
		return Delivery{}, false
	}
	return Delivery{
		Event:      g.event,
		Function:   g.cfg.Function,
		View:       modelhooks.HookConfigView{TimeoutMs: g.cfg.TimeoutMs, OnUnavailable: g.cfg.OnUnavailable},
		Depth:      p.who.depth + 1,
		DeliveryID: id,
		Body:       body,
	}, true
}

// deliveries is plan, unrendered, then encoded. Used where values need no rendering.
func (b builder) deliveries(txn *Txn) []Delivery {
	return b.plan(txn).deliveries()
}

// identity is who a transaction's deliveries say made the write.
type identity struct {
	user      *modelhooks.Claims
	requestID string
	depth     int
}

// identity reads the actor marker into what the deliveries carry.
//
// A marker with no subject is an anonymous caller, which the request path also reports as null.
func (b builder) identity(actor *Actor) identity {
	if actor == nil {
		return identity{requestID: b.newID()}
	}
	who := identity{requestID: actor.RequestID, depth: max(actor.Depth, 0)}
	if who.requestID == "" {
		who.requestID = b.newID()
	}
	if actor.Sub != "" {
		who.user = &modelhooks.Claims{Sub: actor.Sub, Role: actor.Role, Email: actor.Email}
	}
	return who
}

// oldRow is the row before the change. Without REPLICA IDENTITY FULL an update that kept its key
// carries no old row at all, and the key is all Postgres can say about it.
func oldRow(c Change) map[string]json.RawMessage {
	if c.Old != nil {
		return c.Old
	}
	key := make(map[string]json.RawMessage, len(c.Key))
	for _, name := range c.Key {
		if v, ok := c.Row[name]; ok {
			key[name] = v
		}
	}
	return key
}

func eventFor(op string) string {
	if op == "delete" {
		return modelhooks.EventAfterDelete
	}
	return modelhooks.EventAfterChange
}
