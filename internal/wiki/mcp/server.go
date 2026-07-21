package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
	corelinks "github.com/perber/wiki/internal/links"
	"github.com/perber/wiki/internal/tags"
	wikipages "github.com/perber/wiki/internal/wiki/pages"
	wikisearch "github.com/perber/wiki/internal/wiki/search"
)

const (
	mcpProtocolVersion = "2024-11-05"
	serverName         = "leafwiki-mcp"
	serverVersion      = "1.0"

	// Standard JSON-RPC 2.0 error codes.
	errCodeParseError     = -32700
	errCodeInvalidRequest = -32600
	errCodeMethodNotFound = -32601
	errCodeInvalidParams  = -32602
	errCodeInternalError  = -32603
)

// Server handles the MCP JSON-RPC 2.0 protocol.
type Server struct {
	search *wikisearch.SearchUseCase
	tree   *tree.TreeService
	slug   *tree.SlugService
	tags   *tags.TagsService
	links  *corelinks.LinkService
	create *wikipages.CreatePageUseCase
	update *wikipages.UpdatePageUseCase
	move   *wikipages.MovePageUseCase
}

func newServer(cfg RoutesConfig) *Server {
	return &Server{
		search: cfg.SearchUseCase,
		tree:   cfg.TreeService,
		slug:   cfg.SlugService,
		tags:   cfg.TagsService,
		links:  cfg.LinkService,
		create: cfg.CreatePage,
		update: cfg.UpdatePage,
		move:   cfg.MovePage,
	}
}

// ─── JSON-RPC types ───────────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  interface{}      `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// ─── MCP protocol types ───────────────────────────────────────────────────────

type serverCapabilities struct {
	Tools struct {
		ListChanged bool `json:"listChanged"`
	} `json:"tools"`
}

type initializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    serverCapabilities `json:"capabilities"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

// toolInputSchema is a minimal JSON Schema descriptor for a tool's input.
type toolInputSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]toolSchemaProp `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

type toolSchemaProp struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Default     int    `json:"default,omitempty"`
}

type toolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema toolInputSchema `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolDefinition `json:"tools"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ContentBlock is a single piece of tool output content.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolCallResult is the MCP result returned for a tools/call request.
type ToolCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

// handlePost processes a synchronous MCP JSON-RPC request (Streamable HTTP
// transport).  It always responds with application/json.
func (s *Server) handlePost(c *gin.Context) {
	userVal, _ := c.Get("user")
	u, _ := userVal.(*coreauth.User)

	var req jsonRPCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			Error: &jsonRPCError{
				Code:    errCodeParseError,
				Message: "Parse error",
			},
		})
		return
	}

	if req.JSONRPC != "2.0" {
		c.JSON(http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    errCodeInvalidRequest,
				Message: "Invalid Request: jsonrpc must be \"2.0\"",
			},
		})
		return
	}

	// Notifications have no id — accept and return 202 without a body.
	if req.ID == nil {
		c.Status(http.StatusAccepted)
		return
	}

	resp := s.dispatch(c.Request.Context(), &req, u)
	c.JSON(http.StatusOK, resp)
}

// handleSSE implements the HTTP+SSE transport endpoint.  Clients that prefer
// the older SSE transport connect here; the server immediately sends an
// "endpoint" event pointing at the POST URL, then holds the connection open.
func (s *Server) handleSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// Build the POST URL for MCP messages.
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	host := c.Request.Host
	// Remove the "/mcp" suffix to get the base path, then re-append it.
	basePath := strings.TrimSuffix(c.FullPath(), "/mcp")
	endpointURL := fmt.Sprintf("%s://%s%s/mcp", scheme, host, basePath)

	c.SSEvent("endpoint", endpointURL)
	c.Writer.Flush()

	// Hold the connection until the client disconnects.
	<-c.Request.Context().Done()
}

// ─── Dispatch ─────────────────────────────────────────────────────────────────

func (s *Server) dispatch(ctx interface{ Done() <-chan struct{} }, req *jsonRPCRequest, user *coreauth.User) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "tools/list":
		return s.handleToolsList(req, user)
	case "tools/call":
		return s.handleToolsCall(ctx, req, user)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    errCodeMethodNotFound,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

// handleInitialize responds to the MCP initialize handshake.
func (s *Server) handleInitialize(req *jsonRPCRequest) jsonRPCResponse {
	var caps serverCapabilities
	caps.Tools.ListChanged = false

	result := initializeResult{
		ProtocolVersion: mcpProtocolVersion,
		Capabilities:    caps,
	}
	result.ServerInfo.Name = serverName
	result.ServerInfo.Version = serverVersion

	return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

// handleToolsList returns the list of tools available to the caller based on
// their role.  Viewer (or above) sees the read tools; editor (or above) also
// sees the write tools.  Write tools are never advertised to viewers.
func (s *Server) handleToolsList(req *jsonRPCRequest, user *coreauth.User) jsonRPCResponse {
	if user == nil {
		return errorResp(req, errCodeInvalidRequest, "Unauthenticated")
	}

	// Viewer, editor and admin can all use read tools.
	if !hasViewerRole(user) {
		return errorResp(req, http.StatusForbidden, "Insufficient role")
	}

	tools := readToolDefinitions()
	if hasEditorRole(user) {
		tools = append(tools, writeToolDefinitions()...)
	}
	return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: toolsListResult{Tools: tools}}
}

// handleToolsCall dispatches a tools/call request to the correct tool handler.
func (s *Server) handleToolsCall(ctx interface{ Done() <-chan struct{} }, req *jsonRPCRequest, user *coreauth.User) jsonRPCResponse {
	if user == nil {
		return errorResp(req, errCodeInvalidRequest, "Unauthenticated")
	}
	if !hasViewerRole(user) {
		return errorResp(req, http.StatusForbidden, "Insufficient role")
	}

	var params toolCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid params")
	}

	switch params.Name {
	// Read tools — viewer or above.
	case "search_pages":
		return s.callSearchPages(req, params.Arguments)
	case "get_page":
		return s.callGetPage(req, params.Arguments)
	case "list_tree":
		return s.callListTree(req, params.Arguments)
	case "list_tags":
		return s.callListTags(req, params.Arguments)
	case "get_page_links":
		return s.callGetPageLinks(req, params.Arguments)
	// Write tools — editor or above.
	case "create_page", "update_page", "move_page":
		if !hasEditorRole(user) {
			return errorResp(req, http.StatusForbidden, "Insufficient role: write tools require editor")
		}
		switch params.Name {
		case "create_page":
			return s.callCreatePage(req, params.Arguments, user)
		case "update_page":
			return s.callUpdatePage(req, params.Arguments, user)
		default:
			return s.callMovePage(req, params.Arguments, user)
		}
	default:
		return errorResp(req, errCodeMethodNotFound, fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func errorResp(req *jsonRPCRequest, code int, msg string) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &jsonRPCError{Code: code, Message: msg},
	}
}

// hasViewerRole returns true if the user holds viewer, editor, or admin role.
func hasViewerRole(user *coreauth.User) bool {
	return user.Role == coreauth.RoleViewer ||
		user.Role == coreauth.RoleEditor ||
		user.Role == coreauth.RoleAdmin
}

// hasEditorRole returns true if the user holds editor or admin role — the
// minimum required to invoke write tools.
func hasEditorRole(user *coreauth.User) bool {
	return user.Role == coreauth.RoleEditor ||
		user.Role == coreauth.RoleAdmin
}
