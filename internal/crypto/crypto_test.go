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
