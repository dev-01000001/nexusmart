package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/nexusmart/core/services/user/internal/db"
	"github.com/nexusmart/core/services/user/internal/password"
)

// BootstrapAdmin idempotently ensures the configured bootstrap super_admin account exists and is active.
func BootstrapAdmin(ctx context.Context, store db.Store, hasher *password.Hasher, log *slog.Logger, email, plainPassword string) error {
	email = normalizeEmail(email)
	if email == "" || strings.TrimSpace(plainPassword) == "" {
		return nil
	}

	existing, err := store.GetUserByEmail(ctx, email)
	if err == nil {
		if existing.Role != RoleSuperAdmin {
			if _, err := store.UpdateUserRole(ctx, db.UpdateUserRoleParams{
				ID:   existing.ID,
				Role: RoleSuperAdmin,
			}); err != nil {
				return fmt.Errorf("promote bootstrap admin role: %w", err)
			}
		}
		if existing.Status != StatusActive {
			if _, err := store.UpdateUserStatus(ctx, db.UpdateUserStatusParams{
				ID:     existing.ID,
				Status: StatusActive,
			}); err != nil {
				return fmt.Errorf("activate bootstrap admin: %w", err)
			}
		}
		if log != nil {
			log.InfoContext(ctx, "bootstrap admin verified", slog.String("user_id", existing.ID), slog.String("email", existing.Email))
		}
		return nil
	}
	if !isNotFoundErr(err) {
		return fmt.Errorf("lookup bootstrap admin: %w", err)
	}

	hash, err := hasher.Hash(plainPassword)
	if err != nil {
		return fmt.Errorf("hash bootstrap admin password: %w", err)
	}

	userID := uuid.NewString()
	created, err := store.CreateUser(ctx, db.CreateUserParams{
		ID:            userID,
		FullName:      "NexusMart Super Admin",
		Email:         email,
		Phone:         "",
		PasswordHash:  hash,
		Role:          RoleSuperAdmin,
		Status:        StatusActive,
		PhoneVerified: true,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return fmt.Errorf("create bootstrap admin: %w", err)
	}

	if log != nil {
		log.InfoContext(ctx, "bootstrap admin created", slog.String("user_id", created.ID), slog.String("email", created.Email))
	}
	return nil
}
