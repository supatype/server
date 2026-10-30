package conf

// reservedSQLKeywords are the words Postgres will not take as a bare schema name: its reserved
// keywords and those reserved except as a type or function name (pg_get_keywords catcode R
// and T, Postgres 17). Generated with:
//
//	SELECT string_agg(word, ' ' ORDER BY word) FROM pg_get_keywords() WHERE catcode IN ('R', 'T')
var reservedSQLKeywords = map[string]struct{}{
	"all": {}, "analyse": {}, "analyze": {}, "and": {}, "any": {}, "array": {}, "as": {},
	"asc": {}, "asymmetric": {}, "authorization": {}, "binary": {}, "both": {}, "case": {},
	"cast": {}, "check": {}, "collate": {}, "collation": {}, "column": {}, "concurrently": {},
	"constraint": {}, "create": {}, "cross": {}, "current_catalog": {}, "current_date": {},
	"current_role": {}, "current_schema": {}, "current_time": {}, "current_timestamp": {},
	"current_user": {}, "default": {}, "deferrable": {}, "desc": {}, "distinct": {}, "do": {},
	"else": {}, "end": {}, "except": {}, "false": {}, "fetch": {}, "for": {}, "foreign": {},
	"freeze": {}, "from": {}, "full": {}, "grant": {}, "group": {}, "having": {}, "ilike": {},
	"in": {}, "initially": {}, "inner": {}, "intersect": {}, "into": {}, "is": {}, "isnull": {},
	"join": {}, "lateral": {}, "leading": {}, "left": {}, "like": {}, "limit": {},
	"localtime": {}, "localtimestamp": {}, "natural": {}, "not": {}, "notnull": {}, "null": {},
	"offset": {}, "on": {}, "only": {}, "or": {}, "order": {}, "outer": {}, "overlaps": {},
	"placing": {}, "primary": {}, "references": {}, "returning": {}, "right": {}, "select": {},
	"session_user": {}, "similar": {}, "some": {}, "symmetric": {}, "system_user": {},
	"table": {}, "tablesample": {}, "then": {}, "to": {}, "trailing": {}, "true": {}, "union": {},
	"unique": {}, "user": {}, "using": {}, "variadic": {}, "verbose": {}, "when": {}, "where": {},
	"window": {}, "with": {},
}
