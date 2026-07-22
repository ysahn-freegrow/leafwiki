package auth

import (
	"context"

	coreauth "github.com/perber/wiki/internal/core/auth"
)

// ─── GetMCPTokenUseCase ──────────────────────────────────────────────────────

type GetMCPTokenInput struct {
	UserID string
}

type GetMCPTokenOutput struct {
	// Token is nil when the user has no MCP token yet.
	Token *coreauth.PublicMCPToken
}

type GetMCPTokenUseCase struct {
	auth *coreauth.AuthService
}

func NewGetMCPTokenUseCase(a *coreauth.AuthService) *GetMCPTokenUseCase {
	return &GetMCPTokenUseCase{auth: a}
}

func (uc *GetMCPTokenUseCase) Execute(_ context.Context, in GetMCPTokenInput) (*GetMCPTokenOutput, error) {
	if uc.auth == nil {
		return nil, ErrAuthDisabled
	}
	meta, err := uc.auth.GetMCPTokenMeta(in.UserID)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		return &GetMCPTokenOutput{Token: nil}, nil
	}
	return &GetMCPTokenOutput{Token: meta.ToPublicMCPToken()}, nil
}

// ─── CreateMCPTokenUseCase ───────────────────────────────────────────────────

type CreateMCPTokenInput struct {
	UserID string
}

type CreateMCPTokenOutput struct {
	// Token is the plaintext value, returned exactly once at creation.
	Token string
	Meta  *coreauth.PublicMCPToken
}

type CreateMCPTokenUseCase struct {
	auth *coreauth.AuthService
}

func NewCreateMCPTokenUseCase(a *coreauth.AuthService) *CreateMCPTokenUseCase {
	return &CreateMCPTokenUseCase{auth: a}
}

func (uc *CreateMCPTokenUseCase) Execute(_ context.Context, in CreateMCPTokenInput) (*CreateMCPTokenOutput, error) {
	if uc.auth == nil {
		return nil, ErrAuthDisabled
	}
	value, meta, err := uc.auth.CreateMCPToken(in.UserID)
	if err != nil {
		return nil, err
	}
	return &CreateMCPTokenOutput{Token: value, Meta: meta.ToPublicMCPToken()}, nil
}

// ─── RevokeMCPTokenUseCase ───────────────────────────────────────────────────

type RevokeMCPTokenInput struct {
	UserID string
}

type RevokeMCPTokenUseCase struct {
	auth *coreauth.AuthService
}

func NewRevokeMCPTokenUseCase(a *coreauth.AuthService) *RevokeMCPTokenUseCase {
	return &RevokeMCPTokenUseCase{auth: a}
}

func (uc *RevokeMCPTokenUseCase) Execute(_ context.Context, in RevokeMCPTokenInput) error {
	if uc.auth == nil {
		return ErrAuthDisabled
	}
	return uc.auth.RevokeMCPToken(in.UserID)
}
