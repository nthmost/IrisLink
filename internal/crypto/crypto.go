package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
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
// Matches the Python implementation: salt="irislink:v0", info="irislink-room", 16 bytes.
func DeriveRoomID(otp string) (string, error) {
	r := hkdf.New(sha256.New, []byte(strings.ToUpper(otp)), []byte("irislink:v0"), []byte("irislink-room"))
	buf := make([]byte, 16)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
