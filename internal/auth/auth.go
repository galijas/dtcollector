// Package auth holds password hashing and token/key generation.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	UploadKeyPrefix  = "dtk_"
	uploadKeyRandLen = 43
	bcryptCost       = 12
	MinPasswordLen   = 12
)

const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// RandomString returns n characters from [A-Za-z0-9], uniformly distributed.
func RandomString(n int) string {
	var b strings.Builder
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b.WriteByte(alphabet[v.Int64()])
	}
	return b.String()
}

// HashToken hashes high-entropy secrets (upload keys, session tokens). A
// fast hash is enough here: these are random, not user-chosen.
func HashToken(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// NewUploadKey returns the full key (shown once), a display prefix and the hash to store.
func NewUploadKey() (key, prefix, hash string) {
	key = UploadKeyPrefix + RandomString(uploadKeyRandLen)
	return key, key[:len(UploadKeyPrefix)+6], HashToken(key)
}

// LooksLikeUploadKey rejects obviously malformed keys before a DB lookup.
func LooksLikeUploadKey(k string) bool {
	if !strings.HasPrefix(k, UploadKeyPrefix) || len(k) != len(UploadKeyPrefix)+uploadKeyRandLen {
		return false
	}
	for _, c := range k[len(UploadKeyPrefix):] {
		if !strings.ContainsRune(alphabet, c) {
			return false
		}
	}
	return true
}

func NewSessionToken() string { return RandomString(43) }

func GeneratePassword() string { return RandomString(20) }

func HashPassword(pw string) (string, error) {
	if len(pw) < MinPasswordLen {
		return "", errors.New("password must be at least 12 characters")
	}
	if len(pw) > 72 {
		return "", errors.New("password must be at most 72 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

// dummyHash is compared against when the username doesn't exist, so a
// login attempt takes the same time either way.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dtcollector-dummy-password"), bcryptCost)

func CheckPassword(hash, pw string) bool {
	if hash == "" {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
