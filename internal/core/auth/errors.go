package auth

import "errors"

var ErrUserNotFound = errors.New("user not found")
var ErrUserAlreadyExists = errors.New("user already exists")
var ErrUserInvalidCredentials = errors.New("invalid credentials")
var ErrUserInvalidRole = errors.New("invalid role")
var ErrUserAdminCannotBeDeleted = errors.New("admin user cannot be deleted; change role before deletion")
var ErrLastAdminCannotBeDemoted = errors.New("cannot remove admin role from the last admin user")
var ErrInvalidToken = errors.New("invalid token")
var ErrUserAccountLocked = errors.New("account temporarily locked due to too many failed login attempts")

// ErrInvalidMCPToken is returned for every MCP-token validation failure —
// unknown, expired, or revoked — so the caller cannot distinguish the exact
// reason (which would leak token state). ErrMCPTokensDisabled is distinct: it
// signals the feature is not wired (e.g. auth disabled), not a bad token.
var ErrInvalidMCPToken = errors.New("invalid or expired MCP token")
var ErrMCPTokensDisabled = errors.New("MCP tokens are not available")
