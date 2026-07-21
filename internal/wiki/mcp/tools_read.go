package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/perber/wiki/internal/core/tree"
	wikisearch "github.com/perber/wiki/internal/wiki/search"
)

// ─── Tool definitions ─────────────────────────────────────────────────────────

// readToolDefinitions returns the tool descriptors for viewer-accessible tools.
func readToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "search_pages",
			Description: "Search the wiki using full-text BM25 search and return matching pages with excerpts. Use this to discover relevant pages before fetching full content.",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"query": {
						Type:        "string",
						Description: "Full-text search query",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of results to return (default 10, max 50)",
						Default:     10,
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "get_page",
			Description: "Retrieve a single wiki page by its ID, returning the full Markdown content and the current version token (used for optimistic locking).",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"id": {
						Type:        "string",
						Description: "The page ID",
					},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "list_tree",
			Description: "List the wiki page hierarchy as an indented tree of titles, IDs, paths and kinds. Use this to browse structure before fetching a specific page.",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"depth": {
						Type:        "integer",
						Description: "Maximum tree depth to render (0 or omitted = full tree)",
					},
				},
			},
		},
		{
			Name:        "list_tags",
			Description: "List all tags used across the wiki, with the number of pages carrying each tag. Use this to discover topical entry points.",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"filter": {
						Type:        "string",
						Description: "Optional case-insensitive substring filter on tag names",
					},
					"limit": {
						Type:        "integer",
						Description: "Maximum number of tags to return (default 50, max 200)",
						Default:     50,
					},
				},
			},
		},
		{
			Name:        "get_page_links",
			Description: "Return the backlinks (pages linking to this page) and outgoing links (pages this page links to) for a given page ID, for graph/backlink browsing.",
			InputSchema: toolInputSchema{
				Type: "object",
				Properties: map[string]toolSchemaProp{
					"id": {
						Type:        "string",
						Description: "The page ID",
					},
				},
				Required: []string{"id"},
			},
		},
	}
}

// ─── search_pages ─────────────────────────────────────────────────────────────

type searchPagesArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// callSearchPages executes the search_pages tool.
func (s *Server) callSearchPages(req *jsonRPCRequest, rawArgs json.RawMessage) jsonRPCResponse {
	var args searchPagesArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid search_pages arguments")
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return errorResp(req, errCodeInvalidParams, "search_pages: query must not be empty")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 50 {
		args.Limit = 50
	}

	if s.search == nil {
		return toolError(req, "Search is currently unavailable")
	}

	out, err := s.search.Execute(context.Background(), wikisearch.SearchInput{
		Query:  args.Query,
		Offset: 0,
		Limit:  args.Limit,
	})
	if err != nil {
		return toolError(req, fmt.Sprintf("Search failed: %v", err))
	}

	// Render results as plain text for token-efficient RAG retrieval.
	// Each result: title, path, excerpt, bm25_score.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d result(s) for %q:\n\n", out.Result.Count, args.Query))
	for i, item := range out.Result.Items {
		sb.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, item.Title))
		sb.WriteString(fmt.Sprintf("   Path: %s\n", item.Path))
		sb.WriteString(fmt.Sprintf("   ID: %s\n", item.PageID))
		if item.Excerpt != "" {
			sb.WriteString(fmt.Sprintf("   Excerpt: %s\n", item.Excerpt))
		}
		sb.WriteString(fmt.Sprintf("   Score: %.4f\n", item.Rank))
		sb.WriteByte('\n')
	}
	if len(out.Result.Items) == 0 {
		sb.WriteString("No pages matched the query.\n")
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}},
	}
}

// ─── get_page ─────────────────────────────────────────────────────────────────

type getPageArgs struct {
	ID string `json:"id"`
}

// callGetPage executes the get_page tool.
func (s *Server) callGetPage(req *jsonRPCRequest, rawArgs json.RawMessage) jsonRPCResponse {
	var args getPageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid get_page arguments")
	}
	args.ID = strings.TrimSpace(args.ID)
	if args.ID == "" {
		return errorResp(req, errCodeInvalidParams, "get_page: id must not be empty")
	}

	if s.tree == nil {
		return toolError(req, "Page service unavailable")
	}

	page, err := s.tree.GetPage(args.ID)
	if err != nil {
		return toolError(req, fmt.Sprintf("Page not found: %v", err))
	}

	// Return structured text with metadata header followed by full Markdown content.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", page.Title))
	sb.WriteString(fmt.Sprintf("**ID:** %s  \n", page.ID))
	sb.WriteString(fmt.Sprintf("**Path:** %s  \n", page.CalculatePath()))
	sb.WriteString(fmt.Sprintf("**Version:** %s  \n", page.Version()))
	sb.WriteString(fmt.Sprintf("**Kind:** %s  \n\n", page.Kind))
	sb.WriteString("---\n\n")
	sb.WriteString(page.Content)

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}},
	}
}

// ─── list_tree ──────────────────────────────────────────────────────────────

type listTreeArgs struct {
	Depth int `json:"depth"`
}

// callListTree renders the page hierarchy as an indented tree.
func (s *Server) callListTree(req *jsonRPCRequest, rawArgs json.RawMessage) jsonRPCResponse {
	var args listTreeArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return errorResp(req, errCodeInvalidParams, "Invalid list_tree arguments")
		}
	}

	if s.tree == nil {
		return toolError(req, "Page tree unavailable")
	}
	root := s.tree.GetTree()
	if root == nil {
		return toolError(req, "Page tree is not loaded")
	}

	var sb strings.Builder
	sb.WriteString("Wiki page tree:\n\n")
	for _, child := range root.Children {
		writeTreeNode(&sb, child, 0, args.Depth)
	}
	if len(root.Children) == 0 {
		sb.WriteString("(empty)\n")
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}},
	}
}

// writeTreeNode renders a node and its descendants with indentation.  maxDepth
// of 0 means unlimited; depth is the current node's depth (0-based).
func writeTreeNode(sb *strings.Builder, node *tree.PageNode, depth, maxDepth int) {
	if node == nil {
		return
	}
	indent := strings.Repeat("  ", depth)
	fmt.Fprintf(sb, "%s- %s [%s] (id: %s, path: %s)\n",
		indent, node.Title, node.Kind, node.ID, node.CalculatePath())
	if maxDepth > 0 && depth+1 >= maxDepth {
		return
	}
	for _, child := range node.Children {
		writeTreeNode(sb, child, depth+1, maxDepth)
	}
}

// ─── list_tags ──────────────────────────────────────────────────────────────

type listTagsArgs struct {
	Filter string `json:"filter"`
	Limit  int    `json:"limit"`
}

// callListTags lists all tags with their page counts.
func (s *Server) callListTags(req *jsonRPCRequest, rawArgs json.RawMessage) jsonRPCResponse {
	var args listTagsArgs
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &args); err != nil {
			return errorResp(req, errCodeInvalidParams, "Invalid list_tags arguments")
		}
	}
	if args.Limit <= 0 {
		args.Limit = 50
	}
	if args.Limit > 200 {
		args.Limit = 200
	}

	if s.tags == nil {
		return toolError(req, "Tag service unavailable")
	}

	tagCounts, err := s.tags.GetAllTags(args.Filter, args.Limit)
	if err != nil {
		return toolError(req, fmt.Sprintf("Failed to list tags: %v", err))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d tag(s):\n\n", len(tagCounts)))
	for _, tc := range tagCounts {
		sb.WriteString(fmt.Sprintf("- %s (%d)\n", tc.Tag, tc.Count))
	}
	if len(tagCounts) == 0 {
		sb.WriteString("No tags found.\n")
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}},
	}
}

// ─── get_page_links ───────────────────────────────────────────────────────────

type getPageLinksArgs struct {
	ID string `json:"id"`
}

// callGetPageLinks returns backlinks and outgoing links for a page.
func (s *Server) callGetPageLinks(req *jsonRPCRequest, rawArgs json.RawMessage) jsonRPCResponse {
	var args getPageLinksArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResp(req, errCodeInvalidParams, "Invalid get_page_links arguments")
	}
	args.ID = strings.TrimSpace(args.ID)
	if args.ID == "" {
		return errorResp(req, errCodeInvalidParams, "get_page_links: id must not be empty")
	}

	if s.links == nil {
		return toolError(req, "Link service unavailable")
	}

	backlinks, err := s.links.GetBacklinksForPage(args.ID)
	if err != nil {
		return toolError(req, fmt.Sprintf("Failed to load backlinks: %v", err))
	}
	outgoing, err := s.links.GetOutgoingLinksForPage(args.ID)
	if err != nil {
		return toolError(req, fmt.Sprintf("Failed to load outgoing links: %v", err))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Links for page %s\n\n", args.ID))

	sb.WriteString(fmt.Sprintf("## Backlinks (%d)\n", backlinks.Count))
	for _, b := range backlinks.Backlinks {
		broken := ""
		if b.Broken {
			broken = " (broken)"
		}
		sb.WriteString(fmt.Sprintf("- %s (id: %s, path: %s)%s\n", b.FromTitle, b.FromPageID, b.FromPath, broken))
	}
	if len(backlinks.Backlinks) == 0 {
		sb.WriteString("- none\n")
	}

	sb.WriteString(fmt.Sprintf("\n## Outgoing links (%d)\n", outgoing.Count))
	for _, o := range outgoing.Outgoings {
		broken := ""
		if o.Broken {
			broken = " (broken)"
		}
		sb.WriteString(fmt.Sprintf("- %s (id: %s, path: %s)%s\n", o.ToPageTitle, o.ToPageID, o.ToPath, broken))
	}
	if len(outgoing.Outgoings) == 0 {
		sb.WriteString("- none\n")
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  ToolCallResult{Content: []ContentBlock{{Type: "text", Text: sb.String()}}},
	}
}

// ─── toolError helper ─────────────────────────────────────────────────────────

// toolError returns a successful JSON-RPC response that carries an MCP-level
// error inside the tool result (isError: true).
func toolError(req *jsonRPCRequest, msg string) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: ToolCallResult{
			Content: []ContentBlock{{Type: "text", Text: msg}},
			IsError: true,
		},
	}
}
