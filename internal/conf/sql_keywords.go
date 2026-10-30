package conf

// reservedSQLKeywords are the words Postgres will not take as a bare schema name everywhere
// the migrations write the namespace: its reserved keywords, those reserved except as a type
// or function name, and column-name keywords, which cannot qualify a type name such as
// <ns>.factor_type (pg_get_keywords catcode R, T and C, Postgres 17). Generated with:
//
//	SELECT string_agg(word, ' ' ORDER BY word) FROM pg_get_keywords() WHERE catcode IN ('R', 'T', 'C')
var reservedSQLKeywords = map[string]struct{}{
	"all": {}, "analyse": {}, "analyze": {}, "and": {}, "any": {}, "array": {}, "as": {},
	"asc": {}, "asymmetric": {}, "authorization": {}, "between": {}, "bigint": {}, "binary": {},
	"bit": {}, "boolean": {}, "both": {}, "case": {}, "cast": {}, "char": {}, "character": {},
	"check": {}, "coalesce": {}, "collate": {}, "collation": {}, "column": {}, "concurrently": {},
	"constraint": {}, "create": {}, "cross": {}, "current_catalog": {}, "current_date": {},
	"current_role": {}, "current_schema": {}, "current_time": {}, "current_timestamp": {},
	"current_user": {}, "dec": {}, "decimal": {}, "default": {}, "deferrable": {}, "desc": {},
	"distinct": {}, "do": {}, "else": {}, "end": {}, "except": {}, "exists": {}, "extract": {},
	"false": {}, "fetch": {}, "float": {}, "for": {}, "foreign": {}, "freeze": {}, "from": {},
	"full": {}, "grant": {}, "greatest": {}, "group": {}, "grouping": {}, "having": {},
	"ilike": {}, "in": {}, "initially": {}, "inner": {}, "inout": {}, "int": {}, "integer": {},
	"intersect": {}, "interval": {}, "into": {}, "is": {}, "isnull": {}, "join": {}, "json": {},
	"json_array": {}, "json_arrayagg": {}, "json_exists": {}, "json_object": {},
	"json_objectagg": {}, "json_query": {}, "json_scalar": {}, "json_serialize": {},
	"json_table": {}, "json_value": {}, "lateral": {}, "leading": {}, "least": {}, "left": {},
	"like": {}, "limit": {}, "localtime": {}, "localtimestamp": {}, "merge_action": {},
	"national": {}, "natural": {}, "nchar": {}, "none": {}, "normalize": {}, "not": {},
	"notnull": {}, "null": {}, "nullif": {}, "numeric": {}, "offset": {}, "on": {}, "only": {},
	"or": {}, "order": {}, "out": {}, "outer": {}, "overlaps": {}, "overlay": {}, "placing": {},
	"position": {}, "precision": {}, "primary": {}, "real": {}, "references": {}, "returning": {},
	"right": {}, "row": {}, "select": {}, "session_user": {}, "setof": {}, "similar": {},
	"smallint": {}, "some": {}, "substring": {}, "symmetric": {}, "system_user": {}, "table": {},
	"tablesample": {}, "then": {}, "time": {}, "timestamp": {}, "to": {}, "trailing": {},
	"treat": {}, "trim": {}, "true": {}, "union": {}, "unique": {}, "user": {}, "using": {},
	"values": {}, "varchar": {}, "variadic": {}, "verbose": {}, "when": {}, "where": {},
	"window": {}, "with": {}, "xmlattributes": {}, "xmlconcat": {}, "xmlelement": {},
	"xmlexists": {}, "xmlforest": {}, "xmlnamespaces": {}, "xmlparse": {}, "xmlpi": {},
	"xmlroot": {}, "xmlserialize": {}, "xmltable": {},
}
