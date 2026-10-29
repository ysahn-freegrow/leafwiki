package mcp_test

import (
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	httpinternal "github.com/perber/wiki/internal/http"
)

// maxInstructionsRunes keeps the server instructions short enough that MCP
// clients which cap instruction length still receive them whole.
const maxInstructionsRunes = 2048

func toolCallText(t *testing.T, env *testEnv, token string, headers map[string]string, name string, args map[string]interface{}) string {
	t.Helper()
	rec := postMCPWithHeaders(t, env, token, rpc(30, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": args,
	}), headers)
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
	if result.IsError || len(result.Content) == 0 {
		t.Fatalf("%s failed: %s", name, rec.Body)
	}
	return result.Content[0].Text
}

// ─── Server instructions ──────────────────────────────────────────────────────

func TestMCP_InitializeProvidesAuthoringInstructions(t *testing.T) {
	env := newTestEnv(t)
	rec := postMCP(t, env, createViewerToken(t, env), rpc(1, "initialize", map[string]interface{}{}))

	var result struct {
		Instructions string `json:"instructions"`
	}
	mustResult(t, rec.Body.Bytes(), &result)

	for _, marker := range []string{
		"## 기준", "## 결과", // data pages: criteria + results tables
		"## 준비물", "## 절차", "## 문제 해결", // how-to pages for zero-background readers
		"항목 | 필요한 내용", // ask the user when information is missing
		"참조",          // link the pages used when showing wiki content
	} {
		if !strings.Contains(result.Instructions, marker) {
			t.Errorf("instructions missing %q:\n%s", marker, result.Instructions)
		}
	}
	if n := utf8.RuneCountInString(result.Instructions); n > maxInstructionsRunes {
		t.Errorf("instructions are %d characters; keep them within %d", n, maxInstructionsRunes)
	}
}

// ─── Public page URLs in tool output ──────────────────────────────────────────

func TestMCP_GetPageIncludesPublicURL(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	created := callToolText(t, env, editor, 31, "create_page", map[string]interface{}{"title": "Battery Test"})
	id, path := extractField(created, "ID"), extractField(created, "Path")

	text := callToolText(t, env, editor, 32, "get_page", map[string]interface{}{"id": id})
	if want := "**URL:** http://example.com" + path; !strings.Contains(text, want) {
		t.Fatalf("want %q in get_page output; got:\n%s", want, text)
	}
}

func TestMCP_GetPageURLHonoursProxyAndBasePath(t *testing.T) {
	env := newTestEnvWithOpts(t, httpinternal.RouterOptions{BasePath: "/wiki"})
	editor := editorToken(t, env)
	created := callToolText(t, env, editor, 33, "create_page", map[string]interface{}{"title": "Battery Test"})
	id, path := extractField(created, "ID"), extractField(created, "Path")

	text := toolCallText(t, env, editor, map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "wiki.example.org",
	}, "get_page", map[string]interface{}{"id": id})
	if want := "**URL:** https://wiki.example.org/wiki" + path; !strings.Contains(text, want) {
		t.Fatalf("want %q in get_page output; got:\n%s", want, text)
	}
}

func TestMCP_SearchResultsIncludePublicURL(t *testing.T) {
	env := newTestEnv(t)
	text := callToolText(t, env, createViewerToken(t, env), 34, "search_pages", map[string]interface{}{"query": "LeafWiki", "limit": 5})

	var paths, urls []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Path: "):
			paths = append(paths, strings.TrimPrefix(line, "Path: "))
		case strings.HasPrefix(line, "URL: "):
			urls = append(urls, strings.TrimPrefix(line, "URL: "))
		}
	}
	if len(paths) == 0 {
		t.Fatalf("expected search hits; got:\n%s", text)
	}
	if len(urls) != len(paths) {
		t.Fatalf("want one URL per result (%d), got %d:\n%s", len(paths), len(urls), text)
	}
	for i, p := range paths {
		if want := "http://example.com/" + strings.TrimPrefix(p, "/"); urls[i] != want {
			t.Errorf("result %d: want URL %q, got %q", i+1, want, urls[i])
		}
	}
}

func TestMCP_WriteToolsReportPublicURL(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)

	created := callToolText(t, env, editor, 35, "create_page", map[string]interface{}{"title": "Battery Test"})
	path := extractField(created, "Path")
	if got, want := extractField(created, "URL"), "http://example.com"+path; got != want {
		t.Errorf("create_page URL: want %q, got %q\n%s", want, got, created)
	}

	updated := callToolText(t, env, editor, 36, "update_page", map[string]interface{}{
		"id":      extractField(created, "ID"),
		"version": extractField(created, "Version"),
		"content": "updated",
	})
	if got, want := extractField(updated, "URL"), "http://example.com"+path; got != want {
		t.Errorf("update_page URL: want %q, got %q\n%s", want, got, updated)
	}
}
