package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"
)

// mcpTokenLifetime is the fixed lifetime of every MCP token. Per product
// decision it is NOT user-configurable (configurable expiry / "never expires"
// are deliberately out of scope): every token expires exactly 90 days after
// creation, and regenerating resets the clock.
const mcpTokenLifetime = 90 * 24 * time.Hour

// mcpTokenPrefix marks a string as a LeafWiki MCP token. It is purely a
// human/UX aid; validation always relies on the stored hash, not the prefix.
const mcpTokenPrefix = "lw_mcp_"

// hashMCPToken returns the deterministic SHA-256 hex digest used as the stored
// lookup key. See MCPTokenStore for why a deterministic hash (not bcrypt) is
// required here.
func hashMCPToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// generateMCPTokenValue mints a new opaque token: a fixed prefix plus 32 bytes
// (256 bits) of cryptographic randomness, hex-encoded.
func generateMCPTokenValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return mcpTokenPrefix + hex.EncodeToString(b), nil
}

// CreateMCPToken issues (or regenerates) the MCP token for userID and returns
// the plaintext value. The plaintext is shown to the caller EXACTLY ONCE here;
// only its hash is persisted, so it can never be retrieved again — the user
// must regenerate to obtain a new value. Any previously issued token for the
// user is invalidated. The second return value is the stored metadata.
func (a *AuthService) CreateMCPToken(userID string) (string, *MCPToken, error) {
	if a.mcpTokens == nil {
		return "", nil, ErrMCPTokensDisabled
	}
	// Confirm the user exists before minting a token bound to them.
	if _, err := a.userService.GetUserByID(userID); err != nil {
		return "", nil, err
	}

	value, err := generateMCPTokenValue()
	if err != nil {
		return "", nil, err
	}
	id, err := generateJTI()
	if err != nil {
		return "", nil, err
	}

	now := time.Now()
	expiresAt := now.Add(mcpTokenLifetime)
	if err := a.mcpTokens.Upsert(id, userID, hashMCPToken(value), now, expiresAt); err != nil {
		return "", nil, err
	}

	return value, &MCPToken{
		ID:        id,
		UserID:    userID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}, nil
}

// ValidateMCPToken authenticates a bearer token presented on the /mcp
// endpoints and returns the owning user. It accepts ONLY MCP tokens: web
// access/refresh JWTs are not stored in the MCP-token table and therefore fail
// here, which is exactly the intended hard cutover. Every failure mode —
// unknown, expired, or revoked — returns ErrInvalidMCPToken so the caller
// cannot distinguish them (no token-state leak). A successful validation
// records the last-used timestamp on a best-effort basis.
func (a *AuthService) ValidateMCPToken(token string) (*User, error) {
	if a.mcpTokens == nil {
		return nil, ErrInvalidMCPToken
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInvalidMCPToken
	}

	rec, err := a.mcpTokens.GetByHash(hashMCPToken(token))
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, ErrInvalidMCPToken
	}
	if time.Now().After(rec.ExpiresAt) {
		return nil, ErrInvalidMCPToken
	}

	user, err := a.userService.GetUserByID(rec.UserID)
	if err != nil {
		// The token references a user that no longer exists; treat as invalid
		// rather than surfacing a user-not-found detail to an MCP client.
		return nil, ErrInvalidMCPToken
	}

	if err := a.mcpTokens.TouchLastUsed(rec.ID, time.Now()); err != nil {
		slog.Warn("failed to update mcp token last_used_at", "tokenID", rec.ID, "error", err)
	}

	user.Password = ""
	return user, nil
}

// GetMCPTokenMeta returns the metadata for userID's MCP token, or (nil, nil)
// when the user has no token. Never returns the token value.
func (a *AuthService) GetMCPTokenMeta(userID string) (*MCPToken, error) {
	if a.mcpTokens == nil {
		return nil, ErrMCPTokensDisabled
	}
	return a.mcpTokens.GetByUser(userID)
}

// RevokeMCPToken immediately invalidates userID's MCP token. It is a no-op if
// the user has no token.
func (a *AuthService) RevokeMCPToken(userID string) error {
	if a.mcpTokens == nil {
		return ErrMCPTokensDisabled
	}
	return a.mcpTokens.DeleteForUser(userID)
}

// DeleteMCPTokensForUser removes a user's MCP token as part of account
// deletion. Unlike RevokeMCPToken it is safe to call when the feature is not
// wired (returns nil), so cascade-delete paths need no special-casing.
func (a *AuthService) DeleteMCPTokensForUser(userID string) error {
	if a.mcpTokens == nil {
		return nil
	}
	return a.mcpTokens.DeleteForUser(userID)
}
