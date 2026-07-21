package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	coreauth "github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
	wikipages "github.com/perber/wiki/internal/wiki/pages"
)

// ─── Tool definitions ─────────────────────────────────────────────────────────

// writeToolDefinitions returns the tool descriptors for editor-accessible
// (write) tools.  These are only advertised to editors and admins.
func writeToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "create_page",
			Description: "Create a new wiki page. Optionally place it under a parent and give it initial Markdown content. The authenticated user is recorded as the author.",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"title":     {Type: "string", Description: "Page title"},
					"slug":      {Type: "string", Description: "Optional URL slug (derived from the title when omitted)"},
					"parent_id": {Type: "string", Description: "Optional parent page ID; omit for a top-level page"},
					"kind":      {Type: "string", Description: "Node kind: \"page\" (default) or \"section\""},
					"content":   {Type: "string", Description: "Optional initial Markdown content"},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "update_page",
			Description: "Update an existing page's title, slug and/or Markdown content. Requires the current version token for optimistic locking; a stale version is rejected with a 409 conflict. The authenticated user is recorded as the last author.",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"id":      {Type: "string", Description: "The page ID"},
					"version": {Type: "string", Description: "The version token from get_page; must match the current version"},
					"title":   {Type: "string", Description: "New title (unchanged when omitted)"},
					"slug":    {Type: "string", Description: "New slug (unchanged when omitted)"},
					"content": {Type: "string", Description: "New full Markdown content (unchanged when omitted)"},
				},
				Required: []string{"id", "version"},
			},
		},
		{
			Name:        "move_page",
			Description: "Move a page under a new parent and/or position. Requires the current version token for optimistic locking; a stale version is rejected with a 409 conflict.",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"id":        {Type: "string", Description: "The page ID to move"},
					"version":   {Type: "string", Description: "The version token from get_page; must match the current version"},
					"parent_id": {Type: "string", Description: "Target parent page ID (empty string moves to root)"},
					"position":  {Type: "integer", Description: "Optional 0-based position among siblings; appended last when omitted"},
				},
				Required: []string{"id", "version"},
			},
		},
	}
}

// ─── create_page ──────────────────────────────────────────────────────────────

type createPageArgs struct {
	Title    string  `json:"title"`
	Slug     string  `json:"slug"`
	ParentID *string `json:"parent_id"`
	Kind     string  `json:"kind"`
	Content  string  `json:"content"`
}

// callCreatePage executes the create_page tool, attributing authorship to the
// calling user.
func (s *Server) callCreatePage(req *jsonRPCRequest, rawArgs json.RawMessage, user *coreauth.User) jsonRPCResponse {
	var args createPageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid create_page arguments")
	}
	args.Title = strings.TrimSpace(args.Title)
	if args.Title == "" {
		return errorResp(req, errCodeInvalidParams, "create_page: title must not be empty")
	}
	if s.create == nil || s.update == nil {
		return toolError(req, "Page authoring is currently unavailable")
	}

	kind := tree.NodeKindPage
	switch strings.ToLower(strings.TrimSpace(args.Kind)) {
	case "", "page":
		kind = tree.NodeKindPage
	case "section":
		kind = tree.NodeKindSection
	default:
		return errorResp(req, errCodeInvalidParams, "create_page: kind must be \"page\" or \"section\"")
	}

	// Derive a valid slug from the title when the caller omits one.
	slug := strings.TrimSpace(args.Slug)
	if slug == "" && s.slug != nil {
		slug = s.slug.GenerateValidSlug(args.Title)
	}

	ctx := context.Background()
	out, err := s.create.Execute(ctx, wikipages.CreatePageInput{
		UserID:   user.ID,
		ParentID: args.ParentID,
		Title:    args.Title,
		Slug:     slug,
		Kind:     &kind,
	})
	if err != nil {
		return s.writeError(req, err)
	}
	page := out.Page

	// CreatePageInput carries no content; apply initial content via an update
	// using the freshly created page's version.
	content := strings.TrimSpace(args.Content)
	if content != "" {
		body := args.Content
		upd, err := s.update.Execute(ctx, wikipages.UpdatePageInput{
			UserID:  user.ID,
			ID:      page.ID,
			Version: page.Version(),
			Title:   page.Title,
			Slug:    page.Slug,
			Content: &body,
		})
		if err != nil {
			return s.writeError(req, err)
		}
		page = upd.Page
	}

	text := fmt.Sprintf("Created page %q\n- ID: %s\n- Path: %s\n- Version: %s",
		page.Title, page.ID, page.CalculatePath(), page.Version())
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: text}}},
	}
}

// ─── update_page ──────────────────────────────────────────────────────────────

type updatePageArgs struct {
	ID      string  `json:"id"`
	Version string  `json:"version"`
	Title   *string `json:"title"`
	Slug    *string `json:"slug"`
	Content *string `json:"content"`
}

// callUpdatePage executes the update_page tool with optimistic locking.
func (s *Server) callUpdatePage(req *jsonRPCRequest, rawArgs json.RawMessage, user *coreauth.User) jsonRPCResponse {
	var args updatePageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid update_page arguments")
	}
	args.ID = strings.TrimSpace(args.ID)
	if args.ID == "" {
		return errorResp(req, errCodeInvalidParams, "update_page: id must not be empty")
	}
	if strings.TrimSpace(args.Version) == "" {
		return errorResp(req, errCodeInvalidParams, "update_page: version is required for optimistic locking")
	}
	if s.update == nil || s.tree == nil {
		return toolError(req, "Page authoring is currently unavailable")
	}

	// Fetch the current page to fill in fields the caller left unspecified
	// (UpdatePageInput requires a non-empty title). The caller-supplied version
	// — not the freshly read one — is passed to the use case so stale writes
	// are still rejected.
	current, err := s.tree.GetPage(args.ID)
	if err != nil {
		return toolError(req, fmt.Sprintf("Page not found: %v", err))
	}

	title := current.Title
	if args.Title != nil && strings.TrimSpace(*args.Title) != "" {
		title = *args.Title
	}
	slug := current.Slug
	if args.Slug != nil && strings.TrimSpace(*args.Slug) != "" {
		slug = *args.Slug
	}

	out, err := s.update.Execute(context.Background(), wikipages.UpdatePageInput{
		UserID:  user.ID,
		ID:      args.ID,
		Version: args.Version,
		Title:   title,
		Slug:    slug,
		Content: args.Content,
	})
	if err != nil {
		return s.writeError(req, err)
	}

	text := fmt.Sprintf("Updated page %q\n- ID: %s\n- Path: %s\n- New version: %s",
		out.Page.Title, out.Page.ID, out.Page.CalculatePath(), out.Page.Version())
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: text}}},
	}
}

// ─── move_page ──────────────────────────────────────────────────────────────

type movePageArgs struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	ParentID string `json:"parent_id"`
	Position *int   `json:"position"`
}

// callMovePage executes the move_page tool with optimistic locking.
func (s *Server) callMovePage(req *jsonRPCRequest, rawArgs json.RawMessage, user *coreauth.User) jsonRPCResponse {
	var args movePageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid move_page arguments")
	}
	args.ID = strings.TrimSpace(args.ID)
	if args.ID == "" {
		return errorResp(req, errCodeInvalidParams, "move_page: id must not be empty")
	}
	if strings.TrimSpace(args.Version) == "" {
		return errorResp(req, errCodeInvalidParams, "move_page: version is required for optimistic locking")
	}
	if s.move == nil || s.tree == nil {
		return toolError(req, "Page authoring is currently unavailable")
	}

	err := s.move.Execute(context.Background(), wikipages.MovePageInput{
		UserID:   user.ID,
		ID:       args.ID,
		Version:  args.Version,
		ParentID: args.ParentID,
		Position: args.Position,
	})
	if err != nil {
		return s.writeError(req, err)
	}

	// Report the post-move location and refreshed version.
	var text string
	if page, gerr := s.tree.GetPage(args.ID); gerr == nil {
		text = fmt.Sprintf("Moved page %q\n- ID: %s\n- New path: %s\n- New version: %s",
			page.Title, page.ID, page.CalculatePath(), page.Version())
	} else {
		text = fmt.Sprintf("Moved page (id: %s)", args.ID)
	}
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: text}}},
	}
}

// ─── write error mapping ──────────────────────────────────────────────────────

// writeError maps domain write errors to JSON-RPC responses.  A stale/mismatched
// version becomes a 409-coded error (mirroring the REST layer's HTTP 409); a
// missing version becomes an invalid-params error.
func (s *Server) writeError(req *jsonRPCRequest, err error) jsonRPCResponse {
	switch {
	case errors.Is(err, tree.ErrVersionConflict):
		return errorResp(req, http.StatusConflict,
			fmt.Sprintf("version conflict: the page was modified since you read it (%v)", err))
	case errors.Is(err, tree.ErrVersionRequired):
		return errorResp(req, errCodeInvalidParams, "version required for optimistic locking")
	default:
		return toolError(req, fmt.Sprintf("Write failed: %v", err))
	}
}
