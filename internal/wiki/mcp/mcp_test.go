package mcp_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	"github.com/perber/wiki/internal/http/middleware/security"
	"github.com/perber/wiki/internal/wiki"
	"github.com/perber/wiki/internal/wiki/mcp"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ─── Test environment ─────────────────────────────────────────────────────────

type testEnv struct {
	w      *wiki.Wiki
	router *gin.Engine
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	w, err := wiki.NewWiki(&wiki.WikiOptions{
		StorageDir:          t.TempDir(),
		AdminPassword:       "admin",
		JWTSecret:           "test-secret-key-at-least-32-chars!!",
		AccessTokenTimeout:  time.Hour,
		RefreshTokenTimeout: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("NewWiki: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("wiki.Close: %v", err)
		}
	})

	engine := gin.New()
	base := engine.Group("")
	authCookies := authmw.NewAuthCookies(true, time.Hour, 24*time.Hour)
	csrfCookie := security.NewCSRFCookie(true, 72*time.Hour)
	rctx := httpinternal.RouterContext{
		Engine:      engine,
		Base:        base,
		AuthCookies: authCookies,
		CSRFCookie:  csrfCookie,
		Opts:        httpinternal.RouterOptions{},
	}
	mcp.NewRoutes(w.MCPRoutesConfig()).RegisterRoutes(rctx)

	return &testEnv{w: w, router: engine}
}

// createViewerToken creates a viewer user in the wiki and returns its access token.
func createViewerToken(t *testing.T, env *testEnv) string {
	t.Helper()
	_, err := env.w.UserService().CreateUser(
		"viewer1", "viewer1@example.com", "viewer1pass", coreauth.RoleViewer,
	)
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}
	tok, err := env.w.AuthService().Login("viewer1", "viewer1pass")
	if err != nil {
		t.Fatalf("Login viewer: %v", err)
	}
	return tok.Token
}

// adminToken returns a JWT access token for the built-in admin user.
func adminToken(t *testing.T, env *testEnv) string {
	t.Helper()
	tok, err := env.w.AuthService().Login("admin", "admin")
	if err != nil {
		t.Fatalf("Login admin: %v", err)
	}
	return tok.Token
}

// postMCP sends a POST /mcp request with the given bearer token.
func postMCP(t *testing.T, env *testEnv, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	return rec
}

// rpc builds a minimal JSON-RPC 2.0 request map.
func rpc(id int, method string, params interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
}

// mustResult unmarshals the "result" field of a JSON-RPC response, failing the
// test if a JSON-RPC error is present.
func mustResult(t *testing.T, body []byte, dst interface{}) {
	t.Helper()
	var resp struct {
		Result json.RawMessage `json:"result"`
		Err    *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nbody: %s", err, body)
	}
	if resp.Err != nil {
		t.Fatalf("JSON-RPC error %d: %s", resp.Err.Code, resp.Err.Message)
	}
	if dst == nil {
		return
	}
	if err := json.Unmarshal(resp.Result, dst); err != nil {
		t.Fatalf("unmarshal result: %v\nresult: %s", err, resp.Result)
	}
}

// mustRPCError unmarshals only the error part of a JSON-RPC response.
func mustRPCError(t *testing.T, body []byte) *struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
} {
	t.Helper()
	var resp struct {
		Err *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, body)
	}
	return resp.Err
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestMCP_RequiresBearer ensures requests without a token are rejected with 401.
func TestMCP_RequiresBearer(t *testing.T) {
	env := newTestEnv(t)
	rec := postMCP(t, env, "", rpc(1, "initialize", map[string]interface{}{}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// TestMCP_InvalidToken ensures a bad JWT is rejected with 401.
func TestMCP_InvalidToken(t *testing.T) {
	env := newTestEnv(t)
	rec := postMCP(t, env, "not-a-valid-jwt", rpc(1, "initialize", map[string]interface{}{}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// TestMCP_Initialize verifies the MCP initialize handshake returns correct fields.
func TestMCP_Initialize(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", rec.Code, rec.Body)
	}

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	mustResult(t, rec.Body.Bytes(), &result)

	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, "2024-11-05")
	}
	if result.ServerInfo.Name != "leafwiki-mcp" {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, "leafwiki-mcp")
	}
}

// TestMCP_ToolsList verifies viewer sees search_pages and get_page.
func TestMCP_ToolsList(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(2, "tools/list", map[string]interface{}{}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", rec.Code, rec.Body)
	}

	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	mustResult(t, rec.Body.Bytes(), &result)

	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	if !names["search_pages"] {
		t.Errorf("search_pages missing from tools/list; got %v", result.Tools)
	}
	if !names["get_page"] {
		t.Errorf("get_page missing from tools/list; got %v", result.Tools)
	}
}

// TestMCP_AdminTokenSeesSameReadTools verifies that admin also sees the read tools.
func TestMCP_AdminTokenSeesSameReadTools(t *testing.T) {
	env := newTestEnv(t)
	token := adminToken(t, env)

	rec := postMCP(t, env, token, rpc(3, "tools/list", map[string]interface{}{}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", rec.Code, rec.Body)
	}

	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	mustResult(t, rec.Body.Bytes(), &result)
	if len(result.Tools) == 0 {
		t.Error("admin should see at least the read tools")
	}
}

// TestMCP_SearchPages verifies the search_pages tool returns an excerpt.
// The wiki is pre-seeded with a "Welcome to LeafWiki" page.
func TestMCP_SearchPages(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(4, "tools/call", map[string]interface{}{
		"name": "search_pages",
		"arguments": map[string]interface{}{
			"query": "LeafWiki",
			"limit": 5,
		},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d\n%s", rec.Code, rec.Body)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	mustResult(t, rec.Body.Bytes(), &result)

	if result.IsError {
		t.Fatalf("tool returned isError=true: %v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content block")
	}
	text := result.Content[0].Text

	// The search text must contain the welcome page title and excerpt metadata.
	if !strings.Contains(text, "Welcome") {
		t.Errorf("expected result to mention Welcome page; got:\n%s", text)
	}
	if !strings.Contains(text, "Path:") {
		t.Errorf("expected result to contain Path: field; got:\n%s", text)
	}
	if !strings.Contains(text, "ID:") {
		t.Errorf("expected result to contain ID: field; got:\n%s", text)
	}
}

// TestMCP_GetPage verifies the get_page tool returns full markdown and a version token.
func TestMCP_GetPage(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	// Step 1: find a page id via search.
	searchRec := postMCP(t, env, token, rpc(5, "tools/call", map[string]interface{}{
		"name":      "search_pages",
		"arguments": map[string]interface{}{"query": "Welcome", "limit": 1},
	}))
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search step failed: %d\n%s", searchRec.Code, searchRec.Body)
	}
	var searchResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	mustResult(t, searchRec.Body.Bytes(), &searchResult)
	if len(searchResult.Content) == 0 {
		t.Fatal("search returned no content blocks")
	}

	// Extract page ID from the search text ("   ID: <id>").
	pageID := ""
	for _, line := range strings.Split(searchResult.Content[0].Text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ID:") {
			pageID = strings.TrimSpace(strings.TrimPrefix(trimmed, "ID:"))
			break
		}
	}
	if pageID == "" {
		t.Fatalf("could not extract page ID from search output:\n%s", searchResult.Content[0].Text)
	}

	// Step 2: fetch the full page.
	getRec := postMCP(t, env, token, rpc(6, "tools/call", map[string]interface{}{
		"name":      "get_page",
		"arguments": map[string]interface{}{"id": pageID},
	}))
	if getRec.Code != http.StatusOK {
		t.Fatalf("get_page failed: %d\n%s", getRec.Code, getRec.Body)
	}

	var getResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	mustResult(t, getRec.Body.Bytes(), &getResult)

	if getResult.IsError {
		t.Fatalf("get_page returned isError=true: %v", getResult.Content)
	}
	if len(getResult.Content) == 0 {
		t.Fatal("get_page returned no content blocks")
	}
	text := getResult.Content[0].Text

	if !strings.Contains(text, "Version:") {
		t.Errorf("expected Version: in output; got:\n%s", text)
	}
	if !strings.Contains(text, "LeafWiki") {
		t.Errorf("expected markdown content; got:\n%s", text)
	}
}

// TestMCP_GetPage_NotFound verifies that an unknown id returns isError:true.
func TestMCP_GetPage_NotFound(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(7, "tools/call", map[string]interface{}{
		"name":      "get_page",
		"arguments": map[string]interface{}{"id": "page-that-does-not-exist"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}

	var result struct {
		IsError bool `json:"isError"`
	}
	mustResult(t, rec.Body.Bytes(), &result)
	if !result.IsError {
		t.Error("expected isError=true for unknown page id")
	}
}

// TestMCP_Notification verifies that MCP notifications (no id) return HTTP 202.
func TestMCP_Notification(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	// Notifications do not have an "id" field.
	data, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("notification: want 202, got %d", rec.Code)
	}
}

// TestMCP_UnknownMethod verifies method-not-found (-32601) for unknown methods.
func TestMCP_UnknownMethod(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(8, "completely/unknown", map[string]interface{}{}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	rpcErr := mustRPCError(t, rec.Body.Bytes())
	if rpcErr == nil {
		t.Fatal("expected error in response")
	}
	if rpcErr.Code != -32601 {
		t.Errorf("want error code -32601, got %d", rpcErr.Code)
	}
}

// TestMCP_SearchPages_EmptyQueryRejected verifies that an empty query returns an error.
func TestMCP_SearchPages_EmptyQueryRejected(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(9, "tools/call", map[string]interface{}{
		"name":      "search_pages",
		"arguments": map[string]interface{}{"query": ""},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	// The tool should return a JSON-RPC error for invalid params.
	rpcErr := mustRPCError(t, rec.Body.Bytes())
	if rpcErr == nil {
		t.Fatal("expected error response for empty query")
	}
}
