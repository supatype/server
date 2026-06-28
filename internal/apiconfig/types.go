package apiconfig

// RestTableCacheConfig controls server-side REST GET caching for one table.
type RestTableCacheConfig struct {
	Enabled     bool `json:"enabled"`
	AllowPublic bool `json:"allow_public"`
}

// RestConfig holds runtime configuration for the PostgREST proxy.
type RestConfig struct {
	Schema      string                          `json:"schema"`
	MaxRows     int                             `json:"max_rows"`
	CacheMaxTTL int                             `json:"cache_max_ttl"`
	CacheTables map[string]RestTableCacheConfig `json:"cache_tables"`
}

// GraphQLConfig holds runtime configuration for the pg_graphql proxy.
type GraphQLConfig struct {
	Introspection bool `json:"introspection"`
	MaxQueryDepth int  `json:"max_query_depth"`
	MaxRows       int  `json:"max_rows"`
}

// ApiConfig is the top-level config persisted and served by the admin API.
type ApiConfig struct {
	Rest    RestConfig    `json:"rest"`
	GraphQL GraphQLConfig `json:"graphql"`
}

// DefaultApiConfig returns the out-of-the-box API configuration.
func DefaultApiConfig() ApiConfig {
	return ApiConfig{
		Rest: RestConfig{
			Schema:      "public",
			MaxRows:     1000,
			CacheTables: map[string]RestTableCacheConfig{},
		},
		GraphQL: GraphQLConfig{
			Introspection: true,
			MaxQueryDepth: 10,
			MaxRows:       1000,
		},
	}
}

// TableCacheAllowed reports whether server REST cache may run for table.
func (r RestConfig) TableCacheAllowed(table string) (RestTableCacheConfig, bool) {
	if r.CacheTables == nil || table == "" {
		return RestTableCacheConfig{}, false
	}
	tc, ok := r.CacheTables[table]
	if !ok || !tc.Enabled {
		return RestTableCacheConfig{}, false
	}
	return tc, true
}
