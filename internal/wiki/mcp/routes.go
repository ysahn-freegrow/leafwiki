// Package mcp provides an MCP (Model Context Protocol) server embedded in the
// LeafWiki binary.  It exposes wiki pages as agentic-accessible resources over
// an HTTP/SSE transport at the /mcp path, authenticated via per-user JWT Bearer
// tokens.
package mcp

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
	httpinternal "github.com/perber/wiki/internal/http"
	corelinks "github.com/perber/wiki/internal/links"
	"github.com/perber/wiki/internal/tags"
	wikipages "github.com/perber/wiki/internal/wiki/pages"
	wikisearch "github.com/perber/wiki/internal/wiki/search"
)

// Routes is the RouteRegistrar for the MCP domain.
type Routes struct {
	server      *Server
	authService *coreauth.AuthService
}

// RoutesConfig holds the dependencies required to build a Routes instance.
// All services are injected in-process (no REST self-calls) so the MCP module
// reuses the existing domain logic, FTS5 index, and optimistic-locking rules.
type RoutesConfig struct {
	SearchUseCase *wikisearch.SearchUseCase
	TreeService   *tree.TreeService
	SlugService   *tree.SlugService
	TagsService   *tags.TagsService
	LinkService   *corelinks.LinkService
	CreatePage    *wikipages.CreatePageUseCase
	UpdatePage    *wikipages.UpdatePageUseCase
	MovePage      *wikipages.MovePageUseCase
	AuthService   *coreauth.AuthService
}

// NewRoutes constructs the MCP RouteRegistrar.
func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		server:      newServer(cfg),
		authService: cfg.AuthService,
	}
}

// RegisterRoutes implements httpinternal.RouteRegistrar.
// It registers POST /mcp (Streamable HTTP) and GET /mcp (SSE) endpoints,
// both protected by JWT Bearer authentication.
func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	group := ctx.Base.Group("/mcp")
	group.Use(requireBearerAuth(r.authService))
	group.POST("", r.server.handlePost)
	group.GET("", r.server.handleSSE)
}

// requireBearerAuth validates a JWT Bearer token supplied in the
// Authorization header.  Unlike the cookie-based auth used by REST routes,
// MCP clients authenticate with per-user tokens.
func requireBearerAuth(authService *coreauth.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "Bearer token required"})
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		if authService == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "Authentication not configured"})
			return
		}

		user, err := authService.ValidateToken(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "Invalid or expired token"})
			return
		}
		c.Set("user", user)
		c.Next()
	}
}
