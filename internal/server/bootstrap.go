package server

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/rhisijmuiden/fleetwarden/internal/auth"
	"github.com/rhisijmuiden/fleetwarden/internal/store"
)

// BreakGlassUsername is the installer-created local administrator.
const BreakGlassUsername = "admin"

// EnsureAdmin creates the local emergency administrator when it does not exist
// yet, with a random one-time password that is written to passwordFile (mode
// 0600) and must be changed at the first login. It returns the password when
// an account was created.
func EnsureAdmin(ctx context.Context, st *store.Store, passwordFile string) (string, error) {
	if _, err := st.BreakGlassUser(ctx); err == nil {
		return "", nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	pw := auth.GeneratePassword()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return "", err
	}
	if _, err := st.CreateUser(ctx, store.User{
		Username: BreakGlassUsername, DisplayName: "Administrator", Source: "local", PasswordHash: hash,
		MustChangePassword: true, Role: string(auth.RoleAdministrator), BreakGlass: true,
	}); err != nil {
		return "", fmt.Errorf("create admin: %w", err)
	}
	return pw, writePasswordFile(passwordFile, pw)
}

// ResetAdmin gives the emergency administrator a new one-time password and
// removes its TOTP, for when the password or authenticator is lost.
func ResetAdmin(ctx context.Context, st *store.Store, passwordFile string) (string, error) {
	u, err := st.BreakGlassUser(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return EnsureAdmin(ctx, st, passwordFile)
	}
	if err != nil {
		return "", err
	}
	pw := auth.GeneratePassword()
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return "", err
	}
	if err := st.SetPassword(ctx, u.ID, hash, true); err != nil {
		return "", err
	}
	if err := st.SetTOTP(ctx, u.ID, "", false); err != nil {
		return "", err
	}
	if err := st.UpdateProfile(ctx, u.ID, u.DisplayName, u.Email, string(auth.RoleAdministrator), false); err != nil {
		return "", err
	}
	if err := st.DeleteUserSessions(ctx, u.ID, ""); err != nil {
		return "", err
	}
	_ = st.Audit(ctx, auditSystem("user.password_reset", u.Username, true, map[string]any{"via": "console"}))
	return pw, writePasswordFile(passwordFile, pw)
}

func writePasswordFile(path, pw string) error {
	if path == "" {
		return nil
	}
	_ = os.Remove(path)
	return os.WriteFile(path, []byte(BreakGlassUsername+"\n"+pw+"\n"), 0o600)
}
