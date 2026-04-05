package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nthmost/IrisLink/internal/server"
)

// newStack starts a real rendezvous server and a proxy pointing at it.
// Returns the proxy test server; caller must Close both.
func newStack(t *testing.T) (proxyTS *httptest.Server, rdvTS *httptest.Server) {
	t.Helper()
	rdvTS = httptest.NewServer(server.New().Handler())
	proxyTS = httptest.NewServer(Handler(rdvTS.URL))
	return
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decodeMap(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var v map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

// createRoomOnRdv creates a room directly on the rendezvous and returns the otp.
func createRoomOnRdv(t *testing.T, rdvURL, handle string) string {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"handle": handle})
	resp, err := http.Post(rdvURL+"/rooms", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	body := decodeMap(t, resp)
	room := body["room"].(map[string]any)
	return room["otp"].(string)
}

// --- status ---

func TestStatus_noRoom(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	resp, err := http.Get(proxyTS.URL + "/status")
	if err != nil {
		t.Fatal(err)
	}
	body := decodeMap(t, resp)
	if body["room_attached"] != false {
		t.Fatalf("expected room_attached=false, got %v", body["room_attached"])
	}
	if body["version"] == nil {
		t.Fatal("expected version field")
	}
}

// --- message forwarding ---

func TestMessage_forwardsToRendezvous(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	otp := createRoomOnRdv(t, rdvTS.URL, "alice")
	// join so room has two participants
	data, _ := json.Marshal(map[string]string{"handle": "bob"})
	http.Post(rdvTS.URL+"/rooms/"+otp+"/join", "application/json", bytes.NewReader(data))

	resp := postJSON(t, proxyTS.URL+"/message", map[string]string{
		"room_otp": otp,
		"sender":   "alice",
		"text":     "hello proxy",
	})
	body := decodeMap(t, resp)
	msg := body["message"].(map[string]any)
	if msg["text"] != "hello proxy" {
		t.Fatalf("unexpected text %q", msg["text"])
	}
	if msg["status"] != "pending" {
		t.Fatalf("expected pending, got %q", msg["status"])
	}
}

func TestMessage_missingBody(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	resp := postJSON(t, proxyTS.URL+"/message", map[string]string{
		"sender": "alice",
		"text":   "no otp",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- events ---

func TestEvents_empty(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	otp := createRoomOnRdv(t, rdvTS.URL, "alice")
	resp, err := http.Get(fmt.Sprintf("%s/events?room_otp=%s&since=0", proxyTS.URL, otp))
	if err != nil {
		t.Fatal(err)
	}
	body := decodeMap(t, resp)
	events := body["events"].([]any)
	if len(events) != 0 {
		t.Fatalf("expected 0 events, got %d", len(events))
	}
	if body["phase"] != "waiting" {
		t.Fatalf("expected phase=waiting, got %v", body["phase"])
	}
}

func TestEvents_filtersOldMessages(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	otp := createRoomOnRdv(t, rdvTS.URL, "alice")
	data, _ := json.Marshal(map[string]string{"handle": "bob"})
	http.Post(rdvTS.URL+"/rooms/"+otp+"/join", "application/json", bytes.NewReader(data))

	// post a message
	data, _ = json.Marshal(map[string]string{"sender": "alice", "text": "first"})
	http.Post(rdvTS.URL+"/rooms/"+otp+"/messages", "application/json", bytes.NewReader(data))

	// fetch with since=0 → should see the message
	resp, _ := http.Get(fmt.Sprintf("%s/events?room_otp=%s&since=0", proxyTS.URL, otp))
	body := decodeMap(t, resp)
	events := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 event with since=0, got %d", len(events))
	}

	// extract cursor
	next := int64(body["next"].(float64))
	if next == 0 {
		t.Fatal("next cursor should be > 0")
	}

	// fetch again with next as since → should see 0 events
	resp2, _ := http.Get(fmt.Sprintf("%s/events?room_otp=%s&since=%d", proxyTS.URL, otp, next))
	body2 := decodeMap(t, resp2)
	events2 := body2["events"].([]any)
	if len(events2) != 0 {
		t.Fatalf("expected 0 events after cursor, got %d", len(events2))
	}
}

func TestEvents_missingOTP(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	resp, _ := http.Get(proxyTS.URL + "/events")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestEvents_unknownOTP(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	resp, _ := http.Get(fmt.Sprintf("%s/events?room_otp=ZZZZZZ", proxyTS.URL))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- ack forwarding ---

func TestAck_forwardsToRendezvous(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	otp := createRoomOnRdv(t, rdvTS.URL, "alice")
	data, _ := json.Marshal(map[string]string{"handle": "bob"})
	http.Post(rdvTS.URL+"/rooms/"+otp+"/join", "application/json", bytes.NewReader(data))

	// post a message directly on rendezvous to get an ID
	data, _ = json.Marshal(map[string]string{"sender": "alice", "text": "ack me"})
	resp, _ := http.Post(rdvTS.URL+"/rooms/"+otp+"/messages", "application/json", bytes.NewReader(data))
	msgBody := decodeMap(t, resp)
	msgID := msgBody["message"].(map[string]any)["id"].(string)

	// ack via proxy
	ackResp := postJSON(t, proxyTS.URL+"/ack", map[string]string{
		"room_otp":   otp,
		"message_id": msgID,
	})
	body := decodeMap(t, ackResp)
	room := body["room"].(map[string]any)
	messages := room["messages"].([]any)
	m := messages[0].(map[string]any)
	if m["status"] != "acknowledged" {
		t.Fatalf("expected acknowledged, got %q", m["status"])
	}
}

func TestAck_missingFields(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	resp := postJSON(t, proxyTS.URL+"/ack", map[string]string{"room_otp": "ABC123"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// --- CORS ---

func TestProxy_CORSHeaders(t *testing.T) {
	proxyTS, rdvTS := newStack(t)
	defer proxyTS.Close()
	defer rdvTS.Close()

	req, _ := http.NewRequest(http.MethodOptions, proxyTS.URL+"/status", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS header on proxy")
	}
}
