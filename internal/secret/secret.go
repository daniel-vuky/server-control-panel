// Package secret seals and opens the Drive refresh token (AES-256-GCM).
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
)

const prefix = "v1:"

// ErrBadSeal is returned by Open for anything that is not a value sealed with
// this key: wrong format, wrong key, tampering. It carries no detail.
var ErrBadSeal = errors.New("secret: cannot open sealed value")

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("secret: key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plain with AES-256-GCM and a random 12-byte nonce and returns
// "v1:" + base64(nonce|ciphertext).
func Seal(key []byte, plain string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return prefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal. Any failure returns ErrBadSeal.
func Open(key []byte, sealed string) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	rest, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return "", ErrBadSeal
	}
	raw, err := base64.StdEncoding.DecodeString(rest)
	if err != nil || len(raw) < gcm.NonceSize()+gcm.Overhead() {
		return "", ErrBadSeal
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", ErrBadSeal
	}
	return string(plain), nil
}
