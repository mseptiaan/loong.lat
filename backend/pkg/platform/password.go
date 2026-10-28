package platform

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2Params are the argon2id cost settings; Memory is in KiB.
type Argon2Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultArgon2 follows the OWASP guidance for argon2id with headroom (64 MiB, 3 passes, 2 lanes). Raise it as
// hardware allows: every hash records its own parameters, so old hashes keep verifying.
var DefaultArgon2 = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}

// ErrBadHash means the stored hash is not a usable argon2id encoding (a data problem, not a wrong secret).
var ErrBadHash = errors.New("platform: malformed argon2id hash")

// Bounds on parameters read from a stored hash, so a corrupt or hostile value cannot exhaust the server.
const (
	maxArgon2Memory  = 256 * 1024
	maxArgon2Time    = 16
	maxArgon2Threads = 32
)

// HashSecret hashes a password or PIN with DefaultArgon2 into the standard PHC string
// "$argon2id$v=19$m=...,t=...,p=...$salt$hash".
func HashSecret(secret string) (string, error) { return DefaultArgon2.Hash(secret) }

// Hash hashes secret with these parameters and a fresh random salt.
func (p Argon2Params) Hash(secret string) (string, error) {
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("platform: salt: %w", err)
	}
	key := argon2.IDKey([]byte(secret), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifySecret reports whether secret matches an encoded hash, in constant time. The error is non-nil only for
// a malformed hash.
func VerifySecret(secret, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrBadHash
	}
	var p Argon2Params
	var threads uint
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &threads); err != nil {
		return false, ErrBadHash
	}
	if p.Memory == 0 || p.Memory > maxArgon2Memory || p.Time == 0 || p.Time > maxArgon2Time || threads == 0 || threads > maxArgon2Threads {
		return false, ErrBadHash
	}
	p.Threads = uint8(threads)

	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, ErrBadHash
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrBadHash
	}
	got := argon2.IDKey([]byte(secret), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
