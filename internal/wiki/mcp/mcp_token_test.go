package mcp_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestMCPTokenAuthCutover verifies the hard cutover: /mcp accepts a dedicated
// MCP token but rejects a normal web access-token JWT.
func TestMCPTokenAuthCutover(t *testing.T) {
	env := newTestEnv(t)

	// A valid MCP token authenticates successfully.
	mcpToken := adminToken(t, env)
	rec := postMCP(t, env, mcpToken, rpc(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "test", "version": "1.0"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("MCP token: want 200, got %d\n%s", rec.Code, rec.Body)
	}

	// A web access token (issued by Login) must be rejected on /mcp.
	webToken := webAccessToken(t, env)
	rec = postMCP(t, env, webToken, rpc(2, "initialize", map[string]interface{}{}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("web access token on /mcp: want 401, got %d\n%s", rec.Code, rec.Body)
	}
	if code := mcpErrorCode(t, rec.Body.Bytes()); code != "mcp_token_invalid" {
		t.Errorf("web token rejection: want code mcp_token_invalid, got %q", code)
	}
}

// TestMCPTokenInvalidResponse verifies that any invalid MCP token yields a 401
// with the stable, machine-readable error code — without disclosing the reason.
func TestMCPTokenInvalidResponse(t *testing.T) {
	env := newTestEnv(t)

	cases := []struct {
		name  string
		token string
	}{
		{"garbage", "lw_mcp_deadbeef-not-a-real-token"},
		{"empty-bearer", ""},
		{"web-jwt", webAccessToken(t, env)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postMCP(t, env, tc.token, rpc(1, "initialize", map[string]interface{}{}))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d\n%s", rec.Code, rec.Body)
			}
			if code := mcpErrorCode(t, rec.Body.Bytes()); code != "mcp_token_invalid" {
				t.Errorf("want error code mcp_token_invalid, got %q\nbody: %s", code, rec.Body)
			}
		})
	}
}

// TestMCPTokenRevokedRejected verifies that a revoked MCP token stops working.
func TestMCPTokenRevokedRejected(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	// Works before revoke.
	rec := postMCP(t, env, token, rpc(1, "initialize", map[string]interface{}{}))
	if rec.Code != http.StatusOK {
		t.Fatalf("before revoke: want 200, got %d", rec.Code)
	}

	// Revoke the viewer's token.
	viewer, err := env.w.UserService().GetUserByIdentifier("viewer1")
	if err != nil {
		t.Fatalf("GetUserByIdentifier: %v", err)
	}
	if err := env.w.AuthService().RevokeMCPToken(viewer.ID); err != nil {
		t.Fatalf("RevokeMCPToken: %v", err)
	}

	rec = postMCP(t, env, token, rpc(2, "initialize", map[string]interface{}{}))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after revoke: want 401, got %d", rec.Code)
	}
}

// mcpErrorCode extracts error.code from a /mcp 401 JSON body.
func mcpErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal error body: %v\nbody: %s", err, body)
	}
	return resp.Error.Code
}
