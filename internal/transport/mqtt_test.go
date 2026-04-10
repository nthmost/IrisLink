package transport_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	ilcrypto "github.com/nthmost/IrisLink/internal/crypto"
	"github.com/nthmost/IrisLink/internal/transport"
)

// brokerAddr returns the test broker address.
// Override with IRISLINK_TEST_BROKER env var.
func brokerAddr() string {
	if v := os.Getenv("IRISLINK_TEST_BROKER"); v != "" {
		return v
	}
	return "homeassistant.local:1883"
}

func brokerCreds() (string, string) {
	return os.Getenv("IRISLINK_TEST_USER"), os.Getenv("IRISLINK_TEST_PASS")
}

func TestConnect(t *testing.T) {
	otp, _ := ilcrypto.GenerateOTP()
	roomID, _ := ilcrypto.DeriveRoomID(otp)
	key, _ := ilcrypto.DeriveEncKey(otp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bu, bp := brokerCreds()
	client, err := transport.Connect(ctx, brokerAddr(), roomID, "tester", key, func(transport.Envelope) {}, bu, bp)
	if err != nil {
		t.Skipf("broker not reachable (%s): %v", brokerAddr(), err)
	}
	defer client.Disconnect(ctx)
}

func TestPubSub_roundtrip(t *testing.T) {
	otp, _ := ilcrypto.GenerateOTP()
	roomID, _ := ilcrypto.DeriveRoomID(otp)
	key, _ := ilcrypto.DeriveEncKey(otp)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	received := make(chan transport.Envelope, 1)

	// Subscriber connects first
	u, p := brokerCreds()
	sub, err := transport.Connect(ctx, brokerAddr(), roomID, "bob", key, func(env transport.Envelope) {
		received <- env
	}, u, p)
	if err != nil {
		t.Skipf("broker not reachable (%s): %v", brokerAddr(), err)
	}
	defer sub.Disconnect(ctx)

	// Publisher connects separately (different handle, same room)
	pub, err := transport.Connect(ctx, brokerAddr(), roomID, "alice", key, func(transport.Envelope) {}, u, p)
	if err != nil {
		t.Fatalf("publisher connect failed: %v", err)
	}
	defer pub.Disconnect(ctx)

	// Give subscriptions time to establish
	time.Sleep(500 * time.Millisecond)

	if err := pub.Publish(ctx, transport.Envelope{Type: "message", Text: "hello from alice"}); err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	select {
	case env := <-received:
		if env.Text != "hello from alice" {
			t.Fatalf("got %q, want %q", env.Text, "hello from alice")
		}
		if env.Sender != "alice" {
			t.Fatalf("sender %q, want %q", env.Sender, "alice")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for message")
	}
}

func TestPubSub_ownMessagesIgnored(t *testing.T) {
	otp, _ := ilcrypto.GenerateOTP()
	roomID, _ := ilcrypto.DeriveRoomID(otp)
	key, _ := ilcrypto.DeriveEncKey(otp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	count := 0

	u, p := brokerCreds()
	client, err := transport.Connect(ctx, brokerAddr(), roomID, "solo", key, func(env transport.Envelope) {
		mu.Lock()
		count++
		mu.Unlock()
	}, u, p)
	if err != nil {
		t.Skipf("broker not reachable (%s): %v", brokerAddr(), err)
	}
	defer client.Disconnect(ctx)

	time.Sleep(300 * time.Millisecond)
	client.Publish(ctx, transport.Envelope{Type: "message", Text: "echo test"})
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count > 0 {
		t.Fatalf("received own message (should be ignored), count=%d", count)
	}
}

func TestPubSub_wrongKeyCannotDecrypt(t *testing.T) {
	otp, _ := ilcrypto.GenerateOTP()
	roomID, _ := ilcrypto.DeriveRoomID(otp)
	aliceKey, _ := ilcrypto.DeriveEncKey(otp)

	// Eve uses a different OTP → different key, same topic (knows room_id somehow)
	eveOTP, _ := ilcrypto.GenerateOTP()
	eveKey, _ := ilcrypto.DeriveEncKey(eveOTP)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	u, p := brokerCreds()
	eveReceived := make(chan transport.Envelope, 1)
	eve, err := transport.Connect(ctx, brokerAddr(), roomID, "eve", eveKey, func(env transport.Envelope) {
		eveReceived <- env
	}, u, p)
	if err != nil {
		t.Skipf("broker not reachable (%s): %v", brokerAddr(), err)
	}
	defer eve.Disconnect(ctx)

	alice, err := transport.Connect(ctx, brokerAddr(), roomID, "alice", aliceKey, func(transport.Envelope) {}, u, p)
	if err != nil {
		t.Fatalf("alice connect failed: %v", err)
	}
	defer alice.Disconnect(ctx)

	time.Sleep(300 * time.Millisecond)
	alice.Publish(ctx, transport.Envelope{Type: "message", Text: "secret"})
	time.Sleep(500 * time.Millisecond)

	select {
	case env := <-eveReceived:
		t.Fatalf("eve decrypted a message she shouldn't have: %+v", env)
	default:
		// good — message was silently dropped
	}
}
