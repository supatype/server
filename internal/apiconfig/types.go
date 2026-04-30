package apiconfig

// RestConfig holds runtime configuration for the PostgREST proxy.
type RestConfig struct {
	Schema  string `json:"schema"`
	MaxRows int    `json:"max_rows"`
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
			Schema:  "public",
			MaxRows: 1000,
		},
		GraphQL: GraphQLConfig{
			Introspection: true,
			MaxQueryDepth: 10,
			MaxRows:       1000,
		},
	}
}
