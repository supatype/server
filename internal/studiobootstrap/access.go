// Package studiobootstrap answers "what may this caller see and do" from the
// access rules the schema declared, so Studio does not have to guess.
//
// Studio used to infer affordances from role names in the browser. That is a
// second implementation of the access rules living in a place where it cannot be
// enforced, and the two drift: the UI offers an action the database then refuses,
// or hides one it would have allowed. The rules are compiled to policies for
// enforcement and evaluated here for the interface, from the same AST.
//
// Only *row-independent* rules can be answered without a row. Everything else is
// reported as row-dependent rather than guessed — an honest "it depends" is what
// lets Studio ask per row, and a guess is what makes a UI lie.
package studiobootstrap

import (
	"encoding/json"
	"strings"
)

// Verdict is what can be said about an operation before a row is in hand.
type Verdict string

const (
	// VerdictAllow — every caller in this session passes, whatever the row.
	VerdictAllow Verdict = "allow"
	// VerdictDeny — no row can satisfy it, so Studio should not offer the action.
	VerdictDeny Verdict = "deny"
	// VerdictRow — depends on the row. Studio must ask per row (or let the
	// database refuse) rather than assume either way.
	VerdictRow Verdict = "row"
)

// Caller is the identity an evaluation is relative to.
type Caller struct {
	// UserID is the token's subject; empty for an unauthenticated caller.
	UserID string
	// AppRole is the *developer's* application role, which is what `auth.role()`
	// returns and what a `Role<>` rule tests. Not the Studio role: those are
	// separate namespaces and conflating them here would report access the
	// database will not grant.
	AppRole string
	// Claims is the token's claim set, for `Claim<>` operands.
	Claims map[string]any
}

// rule is the on-the-wire shape of an access rule in the AST snapshot.
type rule struct {
	Type       string          `json:"type"`
	Roles      []string        `json:"roles"`
	Rules      []rule          `json:"rules"`
	Rule       *rule           `json:"rule"`
	Left       *operand        `json:"left"`
	Right      *operand        `json:"right"`
	Op         string          `json:"op"`
	Operand    *operand        `json:"operand"`
	IsNull     bool            `json:"isNull"`
	Column     string          `json:"column"`
	Source     json.RawMessage `json:"source"`
	Expression string          `json:"expression"`
}

type operand struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// Evaluate reports what can be determined about a rule without a row.
func Evaluate(raw json.RawMessage, caller Caller) Verdict {
	if len(raw) == 0 {
		// No rule for an operation means denied — deny-by-default is the whole
		// premise, so an absent rule must never read as permission.
		return VerdictDeny
	}
	var r rule
	if err := json.Unmarshal(raw, &r); err != nil {
		// An AST we cannot read is not an AST we may interpret generously.
		return VerdictRow
	}
	return evaluateRule(&r, caller)
}

func evaluateRule(r *rule, caller Caller) Verdict {
	switch r.Type {
	case "public":
		return VerdictAllow
	case "private":
		return VerdictDeny

	case "authenticated":
		if strings.TrimSpace(caller.UserID) != "" {
			return VerdictAllow
		}
		return VerdictDeny

	case "role":
		for _, want := range r.Roles {
			if want == caller.AppRole {
				return VerdictAllow
			}
		}
		return VerdictDeny

	// Ownership, membership and column comparisons are about a particular row.
	case "owner", "in":
		return VerdictRow

	case "compare":
		return evaluateCompare(r, caller)

	case "nullCheck":
		// A null check on a column is row data; on a claim it is decidable.
		if r.Operand != nil && r.Operand.Kind != "column" {
			value, ok := resolveOperand(r.Operand, caller)
			if ok {
				isNull := value == nil
				return boolVerdict(isNull == r.IsNull)
			}
		}
		return VerdictRow

	case "any":
		return combine(r.Rules, caller, VerdictAllow)
	case "all":
		return combine(r.Rules, caller, VerdictDeny)

	case "not":
		if r.Rule == nil {
			return VerdictRow
		}
		switch evaluateRule(r.Rule, caller) {
		case VerdictAllow:
			return VerdictDeny
		case VerdictDeny:
			return VerdictAllow
		default:
			return VerdictRow
		}

	// Raw SQL cannot be interpreted here, and pretending otherwise is exactly the
	// client-side reimplementation this package exists to avoid.
	case "custom":
		return VerdictRow

	default:
		// An unrecognised rule is not an excuse to assume access.
		return VerdictRow
	}
}

// combine folds an OR (`decisive` = allow) or an AND (`decisive` = deny).
//
// One decisive branch settles the whole thing regardless of what the others need
// a row for: an `Any` containing `Role<"admin">` an admin satisfies is allowed
// even if a sibling is row-dependent. Getting this wrong in the cautious
// direction would report "it depends" for rules that plainly do not.
func combine(rules []rule, caller Caller, decisive Verdict) Verdict {
	if len(rules) == 0 {
		// `Any<[]>` grants nothing, `All<[]>` restricts nothing. Both are refused
		// at extract time; if one reaches here, follow the SQL meaning.
		if decisive == VerdictAllow {
			return VerdictDeny
		}
		return VerdictAllow
	}

	sawRow := false
	for i := range rules {
		switch evaluateRule(&rules[i], caller) {
		case decisive:
			return decisive
		case VerdictRow:
			sawRow = true
		}
	}
	if sawRow {
		return VerdictRow
	}
	// Nothing was decisive and nothing needs a row, so the opposite holds.
	if decisive == VerdictAllow {
		return VerdictDeny
	}
	return VerdictAllow
}

func evaluateCompare(r *rule, caller Caller) Verdict {
	if r.Left == nil || r.Right == nil {
		return VerdictRow
	}
	left, leftOK := resolveOperand(r.Left, caller)
	right, rightOK := resolveOperand(r.Right, caller)
	if !leftOK || !rightOK {
		// At least one side is a column, so the row decides.
		return VerdictRow
	}

	// Only equality and inequality are settled here. The ordering operators would
	// need type-aware comparison of two claim values, which is rare enough that a
	// row-dependent answer costs nothing and a wrong one costs a misleading UI.
	switch r.Op {
	case "eq":
		return boolVerdict(sameValue(left, right))
	case "neq":
		return boolVerdict(!sameValue(left, right))
	default:
		return VerdictRow
	}
}

// resolveOperand returns an operand's value, and false when it names a column —
// which only a row can supply.
func resolveOperand(o *operand, caller Caller) (any, bool) {
	switch o.Kind {
	case "authUid":
		if caller.UserID == "" {
			return nil, true
		}
		return caller.UserID, true
	case "authRole":
		return caller.AppRole, true
	case "literal":
		return o.Value, true
	case "claim":
		return claimByPath(caller.Claims, o.Path), true
	default:
		return nil, false
	}
}

// claimByPath walks a dotted path, mirroring `auth.claim_text` in SQL. A missing
// segment yields nil, so comparing against an absent claim denies rather than
// erroring — the same behaviour the policy has.
func claimByPath(claims map[string]any, path string) any {
	if claims == nil || path == "" {
		return nil
	}
	var current any = claims
	for _, segment := range strings.Split(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = obj[segment]
		if !ok {
			return nil
		}
	}
	return current
}

// sameValue compares two claim/literal values the way Postgres would after the
// text cast a policy applies, so `1` and `"1"` are the same value here as there.
func sameValue(a, b any) bool {
	if a == nil || b == nil {
		// SQL null comparison is never true.
		return false
	}
	return scalarText(a) == scalarText(b)
}

func scalarText(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case bool:
		if value {
			return "true"
		}
		return "false"
	case float64:
		// Whole numbers render without a trailing ".0", matching the SQL side.
		if value == float64(int64(value)) {
			return strings.TrimSuffix(strings.TrimRight(formatFloat(value), "0"), ".")
		}
		return formatFloat(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

func formatFloat(v float64) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func boolVerdict(ok bool) Verdict {
	if ok {
		return VerdictAllow
	}
	return VerdictDeny
}
