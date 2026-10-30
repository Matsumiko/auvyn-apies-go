// Package security provides identifiers, tokens and constant-time comparison.
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// NoAmbiguousMixed excludes 0/O/o/1/I/l so secrets survive copy-paste.
const NoAmbiguousMixed = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// GenerateToken returns a token of n characters drawn uniformly from alphabet.
func GenerateToken(n int, alphabet string) string {
	if n <= 0 || len(alphabet) < 2 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic("security: crypto/rand failed: " + err.Error())
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}

// GenerateID mirrors the legacy format: prefix_<14 hex>_<6 hex>.
func GenerateID(prefix string) string {
	a := make([]byte, 7)
	b := make([]byte, 3)
	if _, err := rand.Read(a); err != nil {
		panic("security: crypto/rand failed: " + err.Error())
	}
	if _, err := rand.Read(b); err != nil {
		panic("security: crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("%s_%s_%s", prefix, hex.EncodeToString(a), hex.EncodeToString(b))
}

// RandomRefID returns AVN + 64 bits encoded as unpadded base32 (13 chars).
// Total length stays 16, matching the legacy provider-safe format.
func RandomRefID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic("security: crypto/rand failed: " + err.Error())
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	return "AVN" + enc.EncodeToString(buf)
}

func SHA256Base64(value string) string {
	sum := sha256.Sum256([]byte(value))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// ConstantTimeEqual compares two strings without leaking length-independent
// timing information.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
