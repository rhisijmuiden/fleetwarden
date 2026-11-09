// Package auth holds the authentication primitives: password hashing, TOTP,
// roles and permissions and login rate limiting.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are the argon2id cost parameters. Tests lower them.
var Argon2Params = struct {
	Memory  uint32
	Time    uint32
	Threads uint8
}{Memory: 64 * 1024, Time: 3, Threads: 2}

// argonSlots bounds how many argon2id computations run at once. Each one
// takes Argon2Params.Memory (64 MiB), so a burst of login attempts cannot
// exhaust the server's memory; extra attempts wait their turn.
var argonSlots = make(chan struct{}, 4)

func idKey(pw, salt []byte, t, m uint32, p uint8, n uint32) []byte {
	argonSlots <- struct{}{}
	defer func() { <-argonSlots }()
	return argon2.IDKey(pw, salt, t, m, p, n)
}

// MinPasswordLength is the minimum length of a local account password.
const MinPasswordLength = 12

// Password policy errors.
var (
	ErrPasswordTooShort = errors.New("password_too_short")
	ErrPasswordTooLong  = errors.New("password_too_long")
	ErrPasswordUsername = errors.New("password_contains_username")
)

// HashPassword returns an encoded argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := Argon2Params
	key := idKey([]byte(pw), salt, p.Time, p.Memory, p.Threads, 32)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches the encoded hash.
func VerifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := idKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when a user does not exist, so that response
// times do not reveal which usernames exist.
var dummyHash, _ = HashPassword("fleetwarden-dummy-password")

// VerifyDummy spends the same time as a real verification.
func VerifyDummy(pw string) { VerifyPassword(pw, dummyHash) }

// ValidatePassword enforces the password policy for local accounts.
func ValidatePassword(pw, username string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < MinPasswordLength:
		return ErrPasswordTooShort
	case n > 256:
		return ErrPasswordTooLong
	case username != "" && strings.Contains(strings.ToLower(pw), strings.ToLower(username)):
		return ErrPasswordUsername
	}
	return nil
}

// GeneratePassword returns a random password without look-alike characters,
// grouped for easy reading from a console (xxxxx-xxxxx-xxxxx-xxxxx).
func GeneratePassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	var b strings.Builder
	for i := 0; i < 20; i++ {
		if i > 0 && i%5 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String()
}
