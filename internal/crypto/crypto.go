package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/nacl/secretbox"
)

const otpAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GenerateOTP returns a random 6-character Crockford Base32 OTP.
func GenerateOTP() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 6)
	for i, v := range b {
		out[i] = otpAlphabet[int(v)%len(otpAlphabet)]
	}
	return string(out), nil
}

// DeriveRoomID returns a 32-char lowercase hex string from HKDF-SHA256(otp).
func DeriveRoomID(otp string) (string, error) {
	r := hkdf.New(sha256.New, []byte(strings.ToUpper(otp)), []byte("irislink:v0"), []byte("irislink-room"))
	buf := make([]byte, 16)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// DeriveEncKey returns a 32-byte NaCl secretbox key derived from the OTP.
func DeriveEncKey(otp string) ([32]byte, error) {
	r := hkdf.New(sha256.New, []byte(strings.ToUpper(otp)), []byte("irislink:v0"), []byte("irislink-e2e-key"))
	var key [32]byte
	if _, err := io.ReadFull(r, key[:]); err != nil {
		return key, err
	}
	return key, nil
}

// Seal encrypts plaintext with NaCl secretbox using the given key.
// Returns nonce(24 bytes) || ciphertext.
func Seal(plaintext []byte, key [32]byte) ([]byte, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	encrypted := secretbox.Seal(nonce[:], plaintext, &nonce, &key)
	return encrypted, nil
}

// Open decrypts a message produced by Seal.
func Open(msg []byte, key [32]byte) ([]byte, error) {
	if len(msg) < 24 {
		return nil, fmt.Errorf("message too short")
	}
	var nonce [24]byte
	copy(nonce[:], msg[:24])
	plaintext, ok := secretbox.Open(nil, msg[24:], &nonce, &key)
	if !ok {
		return nil, fmt.Errorf("decryption failed")
	}
	return plaintext, nil
}
