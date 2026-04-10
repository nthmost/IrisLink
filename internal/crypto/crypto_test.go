package crypto

import (
	"regexp"
	"testing"
)

var otpRe = regexp.MustCompile(`^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{6}$`)

func TestGenerateOTP_length(t *testing.T) {
	otp, err := GenerateOTP()
	if err != nil {
		t.Fatal(err)
	}
	if len(otp) != 6 {
		t.Fatalf("expected 6 chars, got %d: %q", len(otp), otp)
	}
}

func TestGenerateOTP_alphabet(t *testing.T) {
	for range 200 {
		otp, err := GenerateOTP()
		if err != nil {
			t.Fatal(err)
		}
		if !otpRe.MatchString(otp) {
			t.Fatalf("OTP %q contains invalid characters", otp)
		}
	}
}

func TestGenerateOTP_excludedChars(t *testing.T) {
	excluded := regexp.MustCompile(`[01IO]`)
	for range 500 {
		otp, err := GenerateOTP()
		if err != nil {
			t.Fatal(err)
		}
		if excluded.MatchString(otp) {
			t.Fatalf("OTP %q contains excluded character", otp)
		}
	}
}

func TestDeriveRoomID_knownValue(t *testing.T) {
	// Verified against the Python implementation:
	// from cryptography.hazmat.primitives.hashes import SHA256
	// from cryptography.hazmat.primitives.kdf.hkdf import HKDF
	// hkdf = HKDF(SHA256(), 16, b"irislink:v0", b"irislink-room")
	// hkdf.derive(b"ABC123").hex() == "2b7f78b348ec93bd73a3627e0acfe919"
	got, err := DeriveRoomID("ABC123")
	if err != nil {
		t.Fatal(err)
	}
	want := "2b7f78b348ec93bd73a3627e0acfe919"
	if got != want {
		t.Fatalf("DeriveRoomID(%q) = %q, want %q", "ABC123", got, want)
	}
}

func TestDeriveRoomID_caseInsensitive(t *testing.T) {
	lower, err := DeriveRoomID("abc123")
	if err != nil {
		t.Fatal(err)
	}
	upper, err := DeriveRoomID("ABC123")
	if err != nil {
		t.Fatal(err)
	}
	if lower != upper {
		t.Fatalf("case should not matter: %q != %q", lower, upper)
	}
}

func TestDeriveRoomID_length(t *testing.T) {
	id, err := DeriveRoomID("TEST99")
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Fatalf("expected 32 hex chars, got %d: %q", len(id), id)
	}
}

func TestDeriveRoomID_deterministic(t *testing.T) {
	a, _ := DeriveRoomID("XK7P2N")
	b, _ := DeriveRoomID("XK7P2N")
	if a != b {
		t.Fatalf("HKDF is not deterministic: %q != %q", a, b)
	}
}

func TestDeriveRoomID_distinct(t *testing.T) {
	a, _ := DeriveRoomID("AAAAA2")
	b, _ := DeriveRoomID("AAAAA3")
	if a == b {
		t.Fatal("different OTPs produced the same room_id")
	}
}

func TestDeriveEncKey_deterministic(t *testing.T) {
	a, err := DeriveEncKey("ABC123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := DeriveEncKey("ABC123")
	if a != b {
		t.Fatal("DeriveEncKey is not deterministic")
	}
}

func TestDeriveEncKey_distinctFromRoomID(t *testing.T) {
	key, _ := DeriveEncKey("ABC123")
	roomID, _ := DeriveRoomID("ABC123")
	// enc key and room_id must not share the same bytes
	if string(key[:16]) == roomID[:16] {
		t.Fatal("enc key and room_id must be distinct")
	}
}

func TestSealOpen_roundtrip(t *testing.T) {
	key, err := DeriveEncKey("TEST99")
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"sender":"alice","text":"hello","type":"message"}`)
	sealed, err := Seal(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) < 24 {
		t.Fatalf("sealed too short: %d bytes", len(sealed))
	}
	opened, err := Open(sealed, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != string(plain) {
		t.Fatalf("roundtrip mismatch: got %q want %q", opened, plain)
	}
}

func TestOpen_wrongKey(t *testing.T) {
	key, _ := DeriveEncKey("AAAAA2")
	wrongKey, _ := DeriveEncKey("AAAAA3")
	sealed, _ := Seal([]byte("secret"), key)
	if _, err := Open(sealed, wrongKey); err == nil {
		t.Fatal("expected decryption failure with wrong key")
	}
}

func TestSeal_uniqueNonces(t *testing.T) {
	key, _ := DeriveEncKey("NONCE9")
	plain := []byte("same message")
	a, _ := Seal(plain, key)
	b, _ := Seal(plain, key)
	// First 24 bytes are the nonce — must differ
	if string(a[:24]) == string(b[:24]) {
		t.Fatal("Seal produced identical nonces")
	}
}
