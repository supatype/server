package oauthserver

import (
	"github.com/supatype/server/internal/conf"
	"github.com/supatype/server/internal/storage"
	"github.com/supatype/server/internal/tokens"
)

// Server represents the OAuth 2.1 server functionality
type Server struct {
	config       *conf.GlobalConfiguration
	db           *storage.Connection
	tokenService *tokens.Service
}

// NewServer creates a new OAuth server instance
func NewServer(config *conf.GlobalConfiguration, db *storage.Connection, tokenService *tokens.Service) *Server {
	return &Server{
		config:       config,
		db:           db,
		tokenService: tokenService,
	}
}
