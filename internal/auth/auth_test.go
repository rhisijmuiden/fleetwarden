package auth

import (
	"strings"
	"testing"
	"time"
)

func init() { Argon2Params.Memory, Argon2Params.Time = 1024, 1 }

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery", h) {
		t.Error("correct password rejected")
	}
	if VerifyPassword("wrong", h) || VerifyPassword("x", "garbage") {
		t.Error("wrong password accepted")
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short", "") != ErrPasswordTooShort {
		t.Error("short password accepted")
	}
	if ValidatePassword("my-admin-password", "Admin") != ErrPasswordUsername {
		t.Error("password with username accepted")
	}
	if err := ValidatePassword("a-long-enough-pass", "admin"); err != nil {
		t.Error(err)
	}
	if p := GeneratePassword(); len(p) != 23 || ValidatePassword(p, "admin") != nil {
		t.Errorf("generated password %q", p)
	}
}

func TestTOTP(t *testing.T) {
	// RFC 6238 test vector (SHA1, secret "12345678901234567890", T=59 -> 94287082, 6 digits 287082).
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	code, err := TOTPCode(secret, 59/30)
	if err != nil || code != "287082" {
		t.Fatalf("code = %s, %v", code, err)
	}
	now := time.Unix(59, 0)
	step, ok := VerifyTOTP(secret, "287 082", now, 0)
	if !ok {
		t.Fatal("valid code rejected")
	}
	if _, ok := VerifyTOTP(secret, "287082", now, step); ok {
		t.Error("replayed code accepted")
	}
	if _, ok := VerifyTOTP(secret, "000000", now, 0); ok {
		t.Error("wrong code accepted")
	}
	if !strings.HasPrefix(TOTPURI("FleetWarden", "admin", secret), "otpauth://totp/FleetWarden:admin?") {
		t.Error("bad URI")
	}
}

func TestRoles(t *testing.T) {
	if !RoleAdministrator.Has(PermSystemUpdate) || RoleTechnician.Has(PermUsersManage) || RoleReadOnly.Has(PermDevicesManage) {
		t.Error("unexpected permissions")
	}
	if RoleAdministrator.Rank() >= RoleReadOnly.Rank() || Role("x").Rank() != len(Roles) {
		t.Error("bad rank")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		l.Fail("ip")
	}
	if !l.Blocked("ip") || l.Blocked("other") {
		t.Fatal("limiter did not block")
	}
	now = now.Add(2 * time.Minute)
	if l.Blocked("ip") {
		t.Error("limiter did not expire")
	}
	l.Fail("ip")
	l.Reset("ip")
	if l.Blocked("ip") {
		t.Error("reset did not clear")
	}
}
