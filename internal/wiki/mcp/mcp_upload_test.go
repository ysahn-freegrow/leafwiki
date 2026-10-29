package mcp_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreauth "github.com/perber/wiki/internal/core/auth"
	httpinternal "github.com/perber/wiki/internal/http"
)

// ─── Upload helpers ───────────────────────────────────────────────────────────

// createTestPage creates a page through the create_page tool and returns its ID.
func createTestPage(t *testing.T, env *testEnv, token, title string) string {
	t.Helper()
	text := callToolText(t, env, token, 900, "create_page", map[string]interface{}{"title": title})
	id := extractField(text, "ID")
	if id == "" {
		t.Fatalf("create_page returned no ID:\n%s", text)
	}
	return id
}

// issueUploadURL calls create_upload_url for pageID and returns the URL.
func issueUploadURL(t *testing.T, env *testEnv, token, pageID string) string {
	t.Helper()
	text := callToolText(t, env, token, 901, "create_upload_url", map[string]interface{}{"page_id": pageID})
	u := extractField(text, "URL")
	if u == "" {
		t.Fatalf("create_upload_url returned no URL:\n%s", text)
	}
	return u
}

// postUpload sends a multipart upload with a single file part to uploadURL.
func postUpload(t *testing.T, env *testEnv, uploadURL, field, filename string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	parsed, err := url.Parse(uploadURL)
	if err != nil {
		t.Fatalf("parse upload URL %q: %v", uploadURL, err)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, parsed.Path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	return rec
}

func toolNames(t *testing.T, env *testEnv, token string) map[string]bool {
	t.Helper()
	rec := postMCP(t, env, token, rpc(2, "tools/list", map[string]interface{}{}))
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
	return names
}

type uploadResponse struct {
	File     string `json:"file"`
	Markdown string `json:"markdown"`
}

func assetDiskPath(env *testEnv, pageID, filename string) string {
	return filepath.Join(env.storageDir, "assets", pageID, filename)
}

// ─── Tool exposure ────────────────────────────────────────────────────────────

func TestMCPUpload_ToolAdvertisedToEditorsOnly(t *testing.T) {
	env := newTestEnv(t)
	if !toolNames(t, env, editorToken(t, env))["create_upload_url"] {
		t.Error("create_upload_url should be advertised to editors")
	}
	if toolNames(t, env, createViewerToken(t, env))["create_upload_url"] {
		t.Error("create_upload_url must not be advertised to viewers")
	}
}

func TestMCPUpload_ViewerCannotCreateUploadURL(t *testing.T) {
	env := newTestEnv(t)
	pageID := createTestPage(t, env, editorToken(t, env), "Photos")

	rec := postMCP(t, env, createViewerToken(t, env), rpc(3, "tools/call", map[string]interface{}{
		"name":      "create_upload_url",
		"arguments": map[string]interface{}{"page_id": pageID},
	}))
	rpcErr := mustRPCError(t, rec.Body.Bytes())
	if rpcErr == nil || rpcErr.Code != http.StatusForbidden {
		t.Fatalf("want 403 JSON-RPC error, got %+v\n%s", rpcErr, rec.Body)
	}
}

func TestMCPUpload_UnknownPageRejected(t *testing.T) {
	env := newTestEnv(t)
	rec := postMCP(t, env, editorToken(t, env), rpc(4, "tools/call", map[string]interface{}{
		"name":      "create_upload_url",
		"arguments": map[string]interface{}{"page_id": "does-not-exist"},
	}))
	var result struct {
		IsError bool `json:"isError"`
	}
	mustResult(t, rec.Body.Bytes(), &result)
	if !result.IsError {
		t.Fatalf("want isError=true for an unknown page; got %s", rec.Body)
	}
}

func TestMCPUpload_ToolOutputIncludesCurlCommandWithPathPlaceholder(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")

	text := callToolText(t, env, editor, 5, "create_upload_url", map[string]interface{}{"page_id": pageID})
	uploadURL := extractField(text, "URL")
	cmd := extractField(text, "Command")
	if !strings.HasPrefix(cmd, "curl ") || !strings.Contains(cmd, `-F "file=@`) || !strings.Contains(cmd, uploadURL) {
		t.Fatalf("expected a curl multipart command targeting %q; got:\n%s", uploadURL, text)
	}
}

// ─── Upload endpoint ──────────────────────────────────────────────────────────

func TestMCPUpload_UploadsLocalFileThroughIssuedURL(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")
	uploadURL := issueUploadURL(t, env, editor, pageID)

	content := []byte("\x89PNG\r\n\x1a\nfake image bytes")
	rec := postUpload(t, env, uploadURL, "file", "photo.png", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d\n%s", rec.Code, rec.Body)
	}

	var body uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal upload response: %v\n%s", err, rec.Body)
	}
	wantFile := "/assets/" + pageID + "/photo.png"
	if body.File != wantFile {
		t.Errorf("file: want %q, got %q", wantFile, body.File)
	}
	if want := "![photo.png](" + wantFile + ")"; body.Markdown != want {
		t.Errorf("markdown: want %q, got %q", want, body.Markdown)
	}

	got, err := os.ReadFile(assetDiskPath(env, pageID, "photo.png"))
	if err != nil {
		t.Fatalf("uploaded asset not on disk: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("stored bytes differ from uploaded bytes")
	}
}

func TestMCPUpload_NonImageGetsPlainLinkMarkdown(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Docs")

	rec := postUpload(t, env, issueUploadURL(t, env, editor, pageID), "file", "spec.pdf", []byte("%PDF-1.7"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d\n%s", rec.Code, rec.Body)
	}
	var body uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := "[spec.pdf](/assets/" + pageID + "/spec.pdf)"; body.Markdown != want {
		t.Errorf("markdown: want %q, got %q", want, body.Markdown)
	}
}

func TestMCPUpload_URLIsSingleUse(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")
	uploadURL := issueUploadURL(t, env, editor, pageID)

	if rec := postUpload(t, env, uploadURL, "file", "a.png", []byte("a")); rec.Code != http.StatusCreated {
		t.Fatalf("first upload: want 201, got %d\n%s", rec.Code, rec.Body)
	}
	rec := postUpload(t, env, uploadURL, "file", "b.png", []byte("b"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reused URL: want 404, got %d\n%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(assetDiskPath(env, pageID, "b.png")); !os.IsNotExist(err) {
		t.Errorf("second upload through a spent URL must not be stored")
	}
}

func TestMCPUpload_UnknownURLRejected(t *testing.T) {
	env := newTestEnv(t)
	rec := postUpload(t, env, "http://example.com/mcp/uploads/not-a-real-ticket", "file", "a.png", []byte("a"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d\n%s", rec.Code, rec.Body)
	}
}

func TestMCPUpload_MissingFileFieldRejected(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")

	rec := postUpload(t, env, issueUploadURL(t, env, editor, pageID), "attachment", "a.png", []byte("a"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d\n%s", rec.Code, rec.Body)
	}
}

func TestMCPUpload_OversizedFileRejected(t *testing.T) {
	env := newTestEnvWithOpts(t, httpinternal.RouterOptions{MaxAssetUploadSizeBytes: 1024})
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")

	rec := postUpload(t, env, issueUploadURL(t, env, editor, pageID), "file", "big.png", bytes.Repeat([]byte("x"), 4096))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d\n%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(assetDiskPath(env, pageID, "big.png")); !os.IsNotExist(err) {
		t.Errorf("oversized upload must not be stored")
	}
}

func TestMCPUpload_DemotedUserCannotRedeem(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")
	uploadURL := issueUploadURL(t, env, editor, pageID)

	user, err := env.w.UserService().GetUserByUsername("editor1")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if _, err := env.w.UserService().UpdateUser(user.ID, user.Username, user.Email, "", coreauth.RoleViewer); err != nil {
		t.Fatalf("demote editor: %v", err)
	}

	rec := postUpload(t, env, uploadURL, "file", "a.png", []byte("a"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 after demotion, got %d\n%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(assetDiskPath(env, pageID, "a.png")); !os.IsNotExist(err) {
		t.Errorf("upload by a demoted user must not be stored")
	}
}

// ─── Public URL derivation ────────────────────────────────────────────────────

func TestMCPUpload_URLHonoursForwardedProtoAndHost(t *testing.T) {
	env := newTestEnv(t)
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")

	text := toolCallText(t, env, editor, map[string]string{
		"X-Forwarded-Proto": "https",
		"X-Forwarded-Host":  "wiki.example.org",
	}, "create_upload_url", map[string]interface{}{"page_id": pageID})
	uploadURL := extractField(text, "URL")
	if !strings.HasPrefix(uploadURL, "https://wiki.example.org/mcp/uploads/") {
		t.Fatalf("want URL behind the proxy's public origin; got %q", uploadURL)
	}
}

func TestMCPUpload_URLIncludesBasePath(t *testing.T) {
	env := newTestEnvWithOpts(t, httpinternal.RouterOptions{BasePath: "/wiki"})
	editor := editorToken(t, env)
	pageID := createTestPage(t, env, editor, "Photos")

	uploadURL := issueUploadURL(t, env, editor, pageID)
	if !strings.HasPrefix(uploadURL, "http://example.com/wiki/mcp/uploads/") {
		t.Fatalf("want URL under the base path; got %q", uploadURL)
	}
	if rec := postUpload(t, env, uploadURL, "file", "a.png", []byte("a")); rec.Code != http.StatusCreated {
		t.Fatalf("upload under base path: want 201, got %d\n%s", rec.Code, rec.Body)
	}
}
