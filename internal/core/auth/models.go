package auth

import "time"

// PublicUser represents a user object that is safe to expose to the public.
type PublicUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	TOTPEnabled bool   `json:"totpEnabled"`
}

// User represents a user object with sensitive information.
type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Role     string `json:"role"`

	// TOTP fields are only ever populated for this user's own record; never
	// exposed via ToPublicUser() except the enabled flag. TOTPSecretEncrypted
	// is only non-empty while a secret is pending confirmation or enabled.
	TOTPEnabled            bool
	TOTPSecretEncrypted    string
	TOTPRecoveryCodeHashes []string
	TOTPEnabledAt          *time.Time
	TOTPLastResetAt        *time.Time
}

func (u *User) HasRole(role string) bool {
	return u.Role == role
}

func (u *User) ToPublicUser() *PublicUser {
	return &PublicUser{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		Role:        u.Role,
		TOTPEnabled: u.TOTPEnabled,
	}
}

// MCPToken is the non-secret record of a user's MCP access token. The token's
// plaintext value is never stored (only a hash) and is returned exactly once at
// creation, so this struct deliberately carries no secret material.
type MCPToken struct {
	ID         string
	UserID     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time // nil until the token has been used at least once
}

// PublicMCPToken is the metadata shape exposed to the owning user's settings
// UI. It never includes the token value; unix-second timestamps keep it
// trivially serialisable for the frontend.
type PublicMCPToken struct {
	CreatedAt  int64  `json:"createdAt"`
	ExpiresAt  int64  `json:"expiresAt"`
	LastUsedAt *int64 `json:"lastUsedAt"`
}

// ToPublicMCPToken converts the internal record into its safe metadata shape.
func (m *MCPToken) ToPublicMCPToken() *PublicMCPToken {
	pub := &PublicMCPToken{
		CreatedAt: m.CreatedAt.Unix(),
		ExpiresAt: m.ExpiresAt.Unix(),
	}
	if m.LastUsedAt != nil {
		lu := m.LastUsedAt.Unix()
		pub.LastUsedAt = &lu
	}
	return pub
}

// Roles
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

var validRoles = map[string]bool{
	RoleAdmin:  true,
	RoleEditor: true,
	RoleViewer: true,
}

func IsValidRole(role string) bool {
	return validRoles[role]
}
