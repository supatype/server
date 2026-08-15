package studiobootstrap

import (
	"encoding/json"
	"testing"
)

func snapshotWith(t *testing.T, ast string) *Snapshot {
	t.Helper()
	return &Snapshot{AST: json.RawMessage(ast), Hash: "test"}
}

const twoModels = `{
  "models": [
    {
      "name": "Post",
      "annotations": {
        "db": { "tableName": "posts" },
        "platform": { "access": {
          "read": {"type":"public"},
          "update": {"type":"owner","field":"author_id"},
          "delete": {"type":"role","roles":["admin"]}
        } }
      }
    },
    {
      "name": "AuditLog",
      "annotations": {
        "db": { "tableName": "audit_log" },
        "platform": { "access": { "read": {"type":"role","roles":["admin"]} } }
      }
    }
  ]
}`

// Filtering happens on the server. Sending the whole schema and hiding parts in
// the browser tells every caller which tables exist — information the access rules
// said they should not have.
func TestModelsWithNoReachableOperationAreOmitted(t *testing.T) {
	models, err := FilterForCaller(snapshotWith(t, twoModels), reader)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(models) != 1 || models[0].Table != "posts" {
		t.Fatalf("a non-admin should see only posts, got %+v", models)
	}

	admins, err := FilterForCaller(snapshotWith(t, twoModels), admin)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(admins) != 2 {
		t.Fatalf("an admin should see both, got %+v", admins)
	}
}

func TestPerOperationVerdicts(t *testing.T) {
	models, err := FilterForCaller(snapshotWith(t, twoModels), reader)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	access := models[0].Access

	if access["read"] != VerdictAllow {
		t.Errorf("read: got %q", access["read"])
	}
	// Declared but row-dependent: Studio must ask per row, not assume.
	if access["update"] != VerdictRow {
		t.Errorf("update: got %q", access["update"])
	}
	// Declared and settled against this caller.
	if access["delete"] != VerdictDeny {
		t.Errorf("delete: got %q", access["delete"])
	}
	// Never declared, so denied — an operation with no rule must not read as
	// permission just because it was omitted.
	if access["create"] != VerdictDeny {
		t.Errorf("create: got %q", access["create"])
	}
}

// The table name is what the caller queries, so it has to be the mapped one.
func TestTableNameFallsBackToTheModelName(t *testing.T) {
	models, err := FilterForCaller(snapshotWith(t, `{"models":[{"name":"Widget",
		"annotations":{"platform":{"access":{"read":{"type":"public"}}}}}]}`), anon)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(models) != 1 || models[0].Table != "Widget" {
		t.Fatalf("got %+v", models)
	}
}

// Every operation reported, in a fixed order, so the payload is stable enough to
// cache on a hash.
func TestEveryOperationIsReported(t *testing.T) {
	models, _ := FilterForCaller(snapshotWith(t, twoModels), admin)
	for _, m := range models {
		for _, op := range []string{"read", "create", "update", "delete"} {
			if _, ok := m.Access[op]; !ok {
				t.Errorf("%s: %q missing", m.Table, op)
			}
		}
	}
}

func TestMalformedASTIsAnError(t *testing.T) {
	if _, err := FilterForCaller(snapshotWith(t, `not json`), admin); err == nil {
		t.Fatal("expected an error rather than an empty schema")
	}
}
