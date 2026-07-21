package mcp_test

import (
	"net/http"
	"strings"
	"testing"

	coreauth "github.com/perber/wiki/internal/core/auth"
)

// editorToken creates an editor user in the wiki and returns its access token.
func editorToken(t *testing.T, env *testEnv) string {
	t.Helper()
	_, err := env.w.UserService().CreateUser(
		"editor1", "editor1@example.com", "editor1pass", coreauth.RoleEditor,
	)
	if err != nil {
		t.Fatalf("CreateUser editor: %v", err)
	}
	tok, err := env.w.AuthService().Login("editor1", "editor1pass")
	if err != nil {
		t.Fatalf("Login editor: %v", err)
	}
	return tok.Token
}

// callTool is a helper that invokes a tools/call and returns the text of the
// first content block (or fails on JSON-RPC error).
func callToolText(t *testing.T, env *testEnv, token string, id int, name string, args map[string]interface{}) string {
	t.Helper()
	rec := postMCP(t, env, token, rpc(id, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": args,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: want 200, got %d\n%s", name, rec.Code, rec.Body)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	mustResult(t, rec.Body.Bytes(), &result)
	if result.IsError {
		t.Fatalf("%s returned isError=true: %v", name, result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatalf("%s returned no content", name)
	}
	return result.Content[0].Text
}

// extractField pulls the value after "<label>:" from a labeled text block.
func extractField(text, label string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		if strings.HasPrefix(trimmed, label+":") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, label+":"))
		}
	}
	return ""
}

// ─── AC2: read tools ──────────────────────────────────────────────────────────

func TestMCP_ListTree(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)
	text := callToolText(t, env, token, 20, "list_tree", map[string]interface{}{})
	if !strings.Contains(text, "Welcome") {
		t.Errorf("expected seeded Welcome page in tree; got:\n%s", text)
	}
	if !strings.Contains(text, "id:") {
		t.Errorf("expected node ids in tree; got:\n%s", text)
	}
}

func TestMCP_ListTags(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)
	// Should succeed even with no tags present.
	text := callToolText(t, env, token, 21, "list_tags", map[string]interface{}{})
	if !strings.Contains(text, "tag(s)") {
		t.Errorf("expected tag summary line; got:\n%s", text)
	}
}

func TestMCP_GetPageLinks(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	// Find the seeded page id via search.
	search := callToolText(t, env, token, 22, "search_pages", map[string]interface{}{"query": "Welcome", "limit": 1})
	pageID := extractField(search, "ID")
	if pageID == "" {
		t.Fatalf("could not extract page id from:\n%s", search)
	}

	text := callToolText(t, env, token, 23, "get_page_links", map[string]interface{}{"id": pageID})
	if !strings.Contains(text, "Backlinks") || !strings.Contains(text, "Outgoing links") {
		t.Errorf("expected backlinks and outgoing sections; got:\n%s", text)
	}
}

// ─── AC3: role gating ───────────────────────────────────────────────────────

func TestMCP_ViewerDoesNotSeeWriteTools(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(24, "tools/list", map[string]interface{}{}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	mustResult(t, rec.Body.Bytes(), &result)
	for _, tool := range result.Tools {
		if tool.Name == "create_page" || tool.Name == "update_page" || tool.Name == "move_page" {
			t.Errorf("viewer must not see write tool %q", tool.Name)
		}
	}
}

func TestMCP_EditorSeesWriteTools(t *testing.T) {
	env := newTestEnv(t)
	token := editorToken(t, env)

	rec := postMCP(t, env, token, rpc(25, "tools/list", map[string]interface{}{}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
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
	for _, want := range []string{"create_page", "update_page", "move_page"} {
		if !names[want] {
			t.Errorf("editor should see write tool %q; got %v", want, names)
		}
	}
}

func TestMCP_ViewerCannotCreatePage(t *testing.T) {
	env := newTestEnv(t)
	token := createViewerToken(t, env)

	rec := postMCP(t, env, token, rpc(26, "tools/call", map[string]interface{}{
		"name":      "create_page",
		"arguments": map[string]interface{}{"title": "Sneaky"},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	rpcErr := mustRPCError(t, rec.Body.Bytes())
	if rpcErr == nil {
		t.Fatal("expected forbidden error for viewer create_page")
	}
	if rpcErr.Code != http.StatusForbidden {
		t.Errorf("want 403 code, got %d (%s)", rpcErr.Code, rpcErr.Message)
	}
}

// ─── AC3/AC4: write + optimistic lock ─────────────────────────────────────────

func TestMCP_EditorCreateUpdateMove(t *testing.T) {
	env := newTestEnv(t)
	token := editorToken(t, env)

	// Create.
	created := callToolText(t, env, token, 30, "create_page", map[string]interface{}{
		"title": "Runbook",
	})
	id := extractField(created, "ID")
	version := extractField(created, "Version")
	if id == "" || version == "" {
		t.Fatalf("could not parse id/version from create output:\n%s", created)
	}

	// Update content with the current version → succeeds and returns a new version.
	updated := callToolText(t, env, token, 31, "update_page", map[string]interface{}{
		"id":      id,
		"version": version,
		"content": "# Runbook\n\nStep one.",
	})
	newVersion := extractField(updated, "New version")
	if newVersion == "" {
		t.Fatalf("could not parse new version from update output:\n%s", updated)
	}
	if newVersion == version {
		t.Errorf("expected version to change after update; still %q", version)
	}

	// Create a section to move the page under.
	section := callToolText(t, env, token, 32, "create_page", map[string]interface{}{
		"title": "Docs",
		"kind":  "section",
	})
	sectionID := extractField(section, "ID")
	if sectionID == "" {
		t.Fatalf("could not parse section id from:\n%s", section)
	}

	// Move the page under the section with its current version → succeeds.
	moved := callToolText(t, env, token, 33, "move_page", map[string]interface{}{
		"id":        id,
		"version":   newVersion,
		"parent_id": sectionID,
	})
	if !strings.Contains(moved, "New path:") {
		t.Errorf("expected move output to report new path; got:\n%s", moved)
	}
}

func TestMCP_UpdateStaleVersionConflict(t *testing.T) {
	env := newTestEnv(t)
	token := editorToken(t, env)

	created := callToolText(t, env, token, 40, "create_page", map[string]interface{}{
		"title": "Concurrent",
	})
	id := extractField(created, "ID")
	staleVersion := extractField(created, "Version")
	if id == "" || staleVersion == "" {
		t.Fatalf("could not parse id/version:\n%s", created)
	}

	// First update advances the version.
	_ = callToolText(t, env, token, 41, "update_page", map[string]interface{}{
		"id":      id,
		"version": staleVersion,
		"content": "first edit",
	})

	// Second update reuses the now-stale version → must be rejected with 409.
	rec := postMCP(t, env, token, rpc(42, "tools/call", map[string]interface{}{
		"name": "update_page",
		"arguments": map[string]interface{}{
			"id":      id,
			"version": staleVersion,
			"content": "conflicting edit",
		},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 transport, got %d", rec.Code)
	}
	rpcErr := mustRPCError(t, rec.Body.Bytes())
	if rpcErr == nil {
		t.Fatal("expected version-conflict error, got success")
	}
	if rpcErr.Code != http.StatusConflict {
		t.Errorf("want 409 conflict code, got %d (%s)", rpcErr.Code, rpcErr.Message)
	}
}
