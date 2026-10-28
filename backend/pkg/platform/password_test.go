package platform

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

// fast keeps tests quick; production hashes use DefaultArgon2.
var fast = Argon2Params{Memory: 8, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}

func TestHashAndVerify(t *testing.T) {
	encoded, err := fast.Hash("s3cret-PIN")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=8,t=1,p=1$") || strings.Count(encoded, "$") != 5 {
		t.Errorf("encoding %q is not the PHC argon2id format", encoded)
	}
	if strings.Contains(encoded, "s3cret-PIN") {
		t.Error("the hash must not contain the secret")
	}

	ok, err := VerifySecret("s3cret-PIN", encoded)
	if err != nil || !ok {
		t.Errorf("right secret: %v, %v", ok, err)
	}
	for _, wrong := range []string{"s3cret-PIn", "", "s3cret-PIN ", "S3CRET-PIN"} {
		if ok, err := VerifySecret(wrong, encoded); err != nil || ok {
			t.Errorf("wrong secret %q: %v, %v", wrong, ok, err)
		}
	}

	again, _ := fast.Hash("s3cret-PIN")
	if again == encoded {
		t.Error("two hashes of one secret must differ (random salt)")
	}
}

func TestDefaultParametersMeetTheBaseline(t *testing.T) {
	if DefaultArgon2.Memory < 19*1024 || DefaultArgon2.Time < 2 || DefaultArgon2.Threads < 1 || DefaultArgon2.KeyLen < 32 || DefaultArgon2.SaltLen < 16 {
		t.Errorf("DefaultArgon2 %+v is weaker than the OWASP minimum", DefaultArgon2)
	}
	encoded, err := HashSecret("pw")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, "m=65536,t=3,p=2") {
		t.Errorf("HashSecret should use DefaultArgon2: %s", encoded)
	}
	if ok, err := VerifySecret("pw", encoded); !ok || err != nil {
		t.Errorf("verify: %v, %v", ok, err)
	}
}

// A hash produced by argon2.IDKey directly (not by Hash) must verify: the encoding is the standard one, so
// hashes from other implementations, or from the driver.provisioned event, are accepted.
func TestVerifyAcceptsAStandardEncoding(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte("pin-123456"), salt, 2, 16, 1, 24)
	enc := base64.RawStdEncoding
	encoded := fmt.Sprintf("$argon2id$v=19$m=16,t=2,p=1$%s$%s", enc.EncodeToString(salt), enc.EncodeToString(key))
	if ok, err := VerifySecret("pin-123456", encoded); !ok || err != nil {
		t.Errorf("standard encoding rejected: %v, %v", ok, err)
	}
	if ok, _ := VerifySecret("pin-654321", encoded); ok {
		t.Error("wrong PIN accepted")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	valid, _ := fast.Hash("x")
	parts := strings.Split(valid, "$")
	with := func(i int, v string) string {
		p := append([]string(nil), parts...)
		p[i] = v
		return strings.Join(p, "$")
	}
	tests := map[string]string{
		"empty":              "",
		"plain text":         "hunter2",
		"other algorithm":    with(1, "argon2i"),
		"old version":        with(2, "v=16"),
		"garbled version":    with(2, "v=x"),
		"garbled parameters": with(3, "m=big,t=1,p=1"),
		"zero memory":        with(3, "m=0,t=1,p=1"),
		"absurd memory":      with(3, "m=4294967295,t=1,p=1"),
		"absurd time":        with(3, "m=8,t=1000000,p=1"),
		"zero threads":       with(3, "m=8,t=1,p=0"),
		"absurd threads":     with(3, "m=8,t=1,p=200"),
		"bad salt":           with(4, "***"),
		"empty salt":         with(4, ""),
		"bad hash":           with(5, "***"),
		"empty hash":         with(5, ""),
		"too few parts":      strings.Join(parts[:5], "$"),
		"too many parts":     valid + "$extra",
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := VerifySecret("x", encoded)
			if ok || !errors.Is(err, ErrBadHash) {
				t.Errorf("VerifySecret = %v, %v; want false, ErrBadHash", ok, err)
			}
		})
	}
}
