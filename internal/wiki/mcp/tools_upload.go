package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	wikiassets "github.com/perber/wiki/internal/wiki/assets"
)

// uploadMultipartMemory is the in-memory budget for parsing an upload's
// multipart body; larger parts spill to temporary files.
const uploadMultipartMemory = 32 << 20 // 32 MiB

// uploadPathPlaceholder is the stand-in the agent replaces with a local path.
const uploadPathPlaceholder = "/path/to/file"

// imageExtensions mirrors IMAGE_EXTENSIONS in the frontend so uploaded images
// get the same embed Markdown the editor inserts.
var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".svg": true,
}

// baseURLKey carries the caller-facing origin + base path (e.g.
// "https://wiki.example.com/wiki") from the /mcp request into tool handlers.
type baseURLKey struct{}

// ─── Tool definition ──────────────────────────────────────────────────────────

// uploadToolDefinition describes create_upload_url. File bytes never travel
// through tool arguments: the tool hands out a single-use URL that a shell
// posts the file to directly, so the cost is the same few tokens regardless of
// file size.
func uploadToolDefinition() toolDefinition {
	return toolDefinition{
		Name: "create_upload_url",
		Description: "Get a single-use URL for attaching a local file (image, PDF, ...) to a page without sending its bytes through the model. " +
			"Use ONLY when the file exists at a local path you can read from a shell (e.g. a dragged-in file or a path the user gave); " +
			"if an image was pasted with no file path, ask the user for the file's path instead. Never base64-encode file contents into tool arguments. " +
			"Run the returned curl command with the real path; its JSON response holds a \"markdown\" snippet to insert with update_page. Expires in 10 minutes.",
		InputSchema: toolInputSchema{
			Type: "object",
			Properties: map[string]toolSchemaProp{
				"page_id": {Type: "string", Description: "ID of the page the file is attached to"},
			},
			Required: []string{"page_id"},
		},
	}
}

// ─── create_upload_url ────────────────────────────────────────────────────────

type createUploadURLArgs struct {
	PageID string `json:"page_id"`
}

// callCreateUploadURL issues a single-use upload URL bound to the caller and
// the target page.
func (s *Server) callCreateUploadURL(ctx context.Context, req *jsonRPCRequest, rawArgs json.RawMessage, user *coreauth.User) jsonRPCResponse {
	var args createUploadURLArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid create_upload_url arguments")
	}
	args.PageID = strings.TrimSpace(args.PageID)
	if args.PageID == "" {
		return errorResp(req, errCodeInvalidParams, "create_upload_url: page_id must not be empty")
	}
	if s.uploadAsset == nil || s.users == nil || s.tree == nil {
		return toolError(req, "Asset upload is currently unavailable")
	}
	if _, err := s.tree.FindPageByID(args.PageID); err != nil {
		return toolError(req, fmt.Sprintf("Page not found: %s", args.PageID))
	}

	ticket, _, err := s.uploads.issue(user.ID, args.PageID)
	if err != nil {
		if errors.Is(err, errTooManyUploadTickets) {
			return toolError(req, err.Error())
		}
		return toolError(req, "Could not create upload URL")
	}

	baseURL, _ := ctx.Value(baseURLKey{}).(string)
	uploadURL := baseURL + "/mcp/uploads/" + ticket
	text := fmt.Sprintf("Upload URL for page %s (single use, expires in 10 minutes, max %s).\n"+
		"- URL: %s\n"+
		"- Command: curl -sS -F \"file=@%s\" \"%s\"\n"+
		"Replace %s with the file's local path. Insert the response's \"markdown\" with update_page.",
		args.PageID, formatByteSize(s.maxUploadBytes), uploadURL, uploadPathPlaceholder, uploadURL, uploadPathPlaceholder)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: text}}},
	}
}

// ─── Upload endpoint ──────────────────────────────────────────────────────────

// handleUpload accepts a multipart "file" field at a URL issued by
// create_upload_url. The ticket in the path is the credential: it is consumed
// on first use, and the issuing user must still hold an editor role.
func (s *Server) handleUpload(c *gin.Context) {
	ticket, ok := s.uploads.redeem(c.Param("ticket"))
	if !ok {
		respondUploadError(c, http.StatusNotFound, "upload_url_invalid",
			"Upload URL is invalid, already used, or expired; request a new one with create_upload_url")
		return
	}
	if s.uploadAsset == nil || s.users == nil {
		respondUploadError(c, http.StatusServiceUnavailable, "upload_unavailable", "Asset upload is currently unavailable")
		return
	}
	user, err := s.users.GetUserByID(ticket.userID)
	if err != nil || !hasEditorRole(user) {
		respondUploadError(c, http.StatusForbidden, "upload_forbidden", "The user who requested this upload URL may no longer upload files")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.maxUploadBytes)
	if err := c.Request.ParseMultipartForm(uploadMultipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			respondUploadError(c, http.StatusRequestEntityTooLarge, wikiassets.ErrCodeAssetFileTooLarge,
				fmt.Sprintf("File too large (max %s)", formatByteSize(s.maxUploadBytes)))
			return
		}
		respondUploadError(c, http.StatusBadRequest, wikiassets.ErrCodeAssetInvalidPayload,
			"Expected a multipart/form-data body with a \"file\" field")
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		respondUploadError(c, http.StatusBadRequest, wikiassets.ErrCodeAssetMissingFile, "Missing \"file\" field")
		return
	}
	defer func() { _ = file.Close() }()

	out, err := s.uploadAsset.Execute(c.Request.Context(), wikiassets.UploadAssetInput{
		UserID:   user.ID,
		PageID:   ticket.pageID,
		File:     file,
		Filename: header.Filename,
		MaxBytes: s.maxUploadBytes,
	})
	if err != nil {
		status, code, msg := uploadErrorStatus(err)
		respondUploadError(c, status, code, msg)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"file": out.URL, "markdown": assetMarkdown(out.URL)})
}

func respondUploadError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// uploadErrorStatus maps an upload use-case error to an HTTP status, using the
// same status rules as the REST asset endpoints.
func uploadErrorStatus(err error) (int, string, string) {
	loc, ok := sharederrors.AsLocalizedError(err)
	if !ok {
		return http.StatusInternalServerError, wikiassets.ErrCodeAssetInternalError, "Upload failed"
	}
	switch loc.Code {
	case wikiassets.ErrCodeAssetFileTooLarge:
		return http.StatusRequestEntityTooLarge, loc.Code, loc.Message
	case wikiassets.ErrCodeAssetPageNotFound:
		return http.StatusNotFound, loc.Code, loc.Message
	case wikiassets.ErrCodeAssetInvalidExtension, wikiassets.ErrCodeAssetInvalidName:
		return http.StatusBadRequest, loc.Code, loc.Message
	default:
		return http.StatusInternalServerError, loc.Code, loc.Message
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// assetMarkdown returns the Markdown the editor would insert for an asset:
// an image embed for image files, a plain link otherwise.
func assetMarkdown(assetURL string) string {
	name := path.Base(assetURL)
	if imageExtensions[strings.ToLower(path.Ext(name))] {
		return "![" + name + "](" + assetURL + ")"
	}
	return "[" + name + "](" + assetURL + ")"
}

// requestBaseURL reconstructs the origin and base path the caller used to
// reach /mcp, honouring X-Forwarded-Proto/Host from a TLS-terminating reverse
// proxy. The headers only shape the URL handed back to the same authenticated
// caller, so trusting them grants nothing.
func requestBaseURL(c *gin.Context) string {
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if p := strings.ToLower(firstHeaderValue(c.GetHeader("X-Forwarded-Proto"))); p == "http" || p == "https" {
		scheme = p
	}
	host := c.Request.Host
	if h := firstHeaderValue(c.GetHeader("X-Forwarded-Host")); h != "" {
		host = h
	}
	return scheme + "://" + host + strings.TrimSuffix(c.FullPath(), "/mcp")
}

// firstHeaderValue returns the first entry of a comma-separated header value.
func firstHeaderValue(v string) string {
	first, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(first)
}

func formatByteSize(n int64) string {
	const mib = 1 << 20
	if n >= mib && n%mib == 0 {
		return fmt.Sprintf("%d MB", n/mib)
	}
	return fmt.Sprintf("%d bytes", n)
}
