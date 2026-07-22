package auth

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// setupMCPTokenAuthService builds an AuthService with the MCP-token store
// enabled and a single test user, returning the service and that user's ID.
func setupMCPTokenAuthService(t *testing.T) (*AuthService, string) {
	t.Helper()
	userStore, err := NewUserStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	userService := NewUserService(userStore)
	user, err := userService.CreateUser("mcpuser", "mcp@example.com", "securepass", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	sessionStore, err := NewSessionStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	authService := NewAuthService(userService, sessionStore, nil, "test-secret-key-for-unit-tests-1", time.Hour, 24*7*time.Hour)

	mcpStore, err := NewMCPTokenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	authService.EnableMCPTokens(mcpStore)
	t.Cleanup(func() {
		if err := authService.Close(); err != nil {
			t.Errorf("authService.Close: %v", err)
		}
	})
	return authService, user.ID
}

// TestMCPTokenLifecycle covers generate → get → regenerate (invalidating the
// previous value) → revoke, and that the plaintext value is returned only at
// creation.
func TestMCPTokenLifecycle(t *testing.T) {
	a, userID := setupMCPTokenAuthService(t)

	// Generate.
	v1, meta1, err := a.CreateMCPToken(userID)
	if err != nil {
		t.Fatalf("CreateMCPToken: %v", err)
	}
	if !strings.HasPrefix(v1, mcpTokenPrefix) {
		t.Errorf("token %q missing prefix %q", v1, mcpTokenPrefix)
	}
	if meta1.UserID != userID {
		t.Errorf("meta.UserID = %q, want %q", meta1.UserID, userID)
	}

	// Get metadata (never the value).
	got, err := a.GetMCPTokenMeta(userID)
	if err != nil {
		t.Fatalf("GetMCPTokenMeta: %v", err)
	}
	if got == nil {
		t.Fatal("expected token metadata, got nil")
	}

	// The generated value authenticates.
	if _, err := a.ValidateMCPToken(v1); err != nil {
		t.Fatalf("ValidateMCPToken(v1) after create: %v", err)
	}

	// Regenerate: a new value is issued and the old one is invalidated.
	v2, _, err := a.CreateMCPToken(userID)
	if err != nil {
		t.Fatalf("CreateMCPToken (regenerate): %v", err)
	}
	if v2 == v1 {
		t.Fatal("regenerated token must differ from the previous one")
	}
	if _, err := a.ValidateMCPToken(v1); !errors.Is(err, ErrInvalidMCPToken) {
		t.Errorf("old token after regenerate: want ErrInvalidMCPToken, got %v", err)
	}
	if _, err := a.ValidateMCPToken(v2); err != nil {
		t.Errorf("new token after regenerate should be valid, got %v", err)
	}

	// Revoke: metadata is gone and the token no longer authenticates.
	if err := a.RevokeMCPToken(userID); err != nil {
		t.Fatalf("RevokeMCPToken: %v", err)
	}
	meta, err := a.GetMCPTokenMeta(userID)
	if err != nil {
		t.Fatalf("GetMCPTokenMeta after revoke: %v", err)
	}
	if meta != nil {
		t.Error("expected nil metadata after revoke")
	}
	if _, err := a.ValidateMCPToken(v2); !errors.Is(err, ErrInvalidMCPToken) {
		t.Errorf("revoked token: want ErrInvalidMCPToken, got %v", err)
	}
}

// TestMCPTokenExpiry verifies the fixed 90-day lifetime, that an expired token
// is rejected, and that regenerating resets the expiry.
func TestMCPTokenExpiry(t *testing.T) {
	if mcpTokenLifetime != 90*24*time.Hour {
		t.Fatalf("mcpTokenLifetime = %v, want 90 days", mcpTokenLifetime)
	}

	a, userID := setupMCPTokenAuthService(t)

	// A freshly created token expires exactly one lifetime after creation.
	_, meta, err := a.CreateMCPToken(userID)
	if err != nil {
		t.Fatalf("CreateMCPToken: %v", err)
	}
	if got := meta.ExpiresAt.Sub(meta.CreatedAt); got != mcpTokenLifetime {
		t.Errorf("expiry - creation = %v, want %v", got, mcpTokenLifetime)
	}

	// Inject an already-expired token directly into the store; validation must
	// reject it even though the hash matches.
	expiredValue := mcpTokenPrefix + "expiredtokenvaluethatisdefinitelyoldnow"
	id, err := generateJTI()
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-100 * 24 * time.Hour)
	expired := time.Now().Add(-10 * 24 * time.Hour)
	if err := a.mcpTokens.Upsert(id, userID, hashMCPToken(expiredValue), created, expired); err != nil {
		t.Fatalf("Upsert expired: %v", err)
	}
	if _, err := a.ValidateMCPToken(expiredValue); !errors.Is(err, ErrInvalidMCPToken) {
		t.Errorf("expired token: want ErrInvalidMCPToken, got %v", err)
	}

	// Regenerating resets the expiry back to now + lifetime and yields a
	// working token.
	fresh, freshMeta, err := a.CreateMCPToken(userID)
	if err != nil {
		t.Fatalf("CreateMCPToken (reset): %v", err)
	}
	if !freshMeta.ExpiresAt.After(time.Now().Add(89 * 24 * time.Hour)) {
		t.Errorf("regenerated expiry %v not reset to ~now+90d", freshMeta.ExpiresAt)
	}
	if _, err := a.ValidateMCPToken(fresh); err != nil {
		t.Errorf("regenerated token should validate, got %v", err)
	}
}

// TestMCPTokenStorageAndLastUsed verifies that only a hash is persisted (never
// the plaintext) and that last-used is recorded on successful validation.
func TestMCPTokenStorageAndLastUsed(t *testing.T) {
	a, userID := setupMCPTokenAuthService(t)

	value, _, err := a.CreateMCPToken(userID)
	if err != nil {
		t.Fatalf("CreateMCPToken: %v", err)
	}

	// Storage is hash-keyed: the record is found by the hash of the value, and
	// NOT by the plaintext value used as a key — proving the plaintext is not
	// what's stored.
	if rec, err := a.mcpTokens.GetByHash(hashMCPToken(value)); err != nil || rec == nil {
		t.Fatalf("GetByHash(hash) = %v, %v; want a record", rec, err)
	}
	if rec, err := a.mcpTokens.GetByHash(value); err != nil || rec != nil {
		t.Errorf("GetByHash(plaintext) = %v, %v; want nil (plaintext must not be stored)", rec, err)
	}
	if hashMCPToken(value) == value {
		t.Error("stored hash must differ from the plaintext token")
	}

	// last_used is nil until the token is actually used.
	before, err := a.GetMCPTokenMeta(userID)
	if err != nil {
		t.Fatalf("GetMCPTokenMeta: %v", err)
	}
	if before.LastUsedAt != nil {
		t.Errorf("last_used should be nil before use, got %v", before.LastUsedAt)
	}

	// A successful validation records last_used.
	if _, err := a.ValidateMCPToken(value); err != nil {
		t.Fatalf("ValidateMCPToken: %v", err)
	}
	after, err := a.GetMCPTokenMeta(userID)
	if err != nil {
		t.Fatalf("GetMCPTokenMeta after use: %v", err)
	}
	if after.LastUsedAt == nil {
		t.Error("last_used should be set after a successful validation")
	}
}
