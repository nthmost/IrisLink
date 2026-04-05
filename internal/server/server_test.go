package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := New()
	return httptest.NewServer(srv.Handler())
}

func post(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func roomFrom(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	decode(t, resp, &body)
	room, ok := body["room"].(map[string]any)
	if !ok {
		t.Fatalf("response has no 'room' key: %v", body)
	}
	return room
}

// --- create ---

func TestCreateRoom_returnsOTP(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp, _ := room["otp"].(string)
	if len(otp) != 6 {
		t.Fatalf("expected 6-char OTP, got %q", otp)
	}
}

func TestCreateRoom_defaultHandle(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{}))
	participants, _ := room["participants"].([]any)
	if len(participants) == 0 {
		t.Fatal("no participants")
	}
	p := participants[0].(map[string]any)
	if p["handle"] != "operator" {
		t.Fatalf("expected 'operator', got %q", p["handle"])
	}
}

func TestCreateRoom_phase_waiting(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	if room["phase"] != "waiting" {
		t.Fatalf("expected waiting, got %q", room["phase"])
	}
}

func TestCreateRoom_mode_relay(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	if room["mode"] != "relay" {
		t.Fatalf("expected relay, got %q", room["mode"])
	}
}

// --- join ---

func TestJoinRoom_phase_joined(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)

	joined := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "bob"}))
	if joined["phase"] != "joined" {
		t.Fatalf("expected joined, got %q", joined["phase"])
	}
}

func TestJoinRoom_phase_active_when_both_present(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)

	post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "bob"})
	// bob marks present
	updated := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/participants",
		map[string]string{"handle": "bob", "status": "present"}))
	if updated["phase"] != "active" {
		t.Fatalf("expected active, got %q", updated["phase"])
	}
}

func TestJoinRoom_rejectsFull(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)
	post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "bob"})

	resp := post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "carol"})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestJoinRoom_rejoinSameHandle(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)
	post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "bob"})

	// bob rejoins — should update status, not add a third participant
	rejoin := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "bob"}))
	participants := rejoin["participants"].([]any)
	if len(participants) != 2 {
		t.Fatalf("expected 2 participants after rejoin, got %d", len(participants))
	}
}

func TestJoinRoom_unknownOTP(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := post(t, ts.URL+"/rooms/ZZZZZZ/join", map[string]string{"handle": "alice"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- OTP validation ---

func TestInvalidOTP_badChars(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := post(t, ts.URL+"/rooms/000000/join", map[string]string{"handle": "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid OTP, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestInvalidOTP_wrongLength(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := post(t, ts.URL+"/rooms/ABC/join", map[string]string{"handle": "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for short OTP, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- messages ---

func TestPostMessage_and_ack(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)
	post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "bob"})

	// post a message
	var msgBody map[string]any
	resp := post(t, ts.URL+"/rooms/"+otp+"/messages",
		map[string]string{"sender": "alice", "text": "hello"})
	decode(t, resp, &msgBody)
	msg := msgBody["message"].(map[string]any)
	if msg["text"] != "hello" {
		t.Fatalf("expected 'hello', got %q", msg["text"])
	}
	if msg["status"] != "pending" {
		t.Fatalf("expected pending, got %q", msg["status"])
	}

	msgID := msg["id"].(string)

	// ack it
	acked := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/messages/"+msgID+"/ack", nil))
	messages := acked["messages"].([]any)
	m := messages[0].(map[string]any)
	if m["status"] != "acknowledged" {
		t.Fatalf("expected acknowledged, got %q", m["status"])
	}
}

func TestWaitingOn(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)
	post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "bob"})

	// alice sends
	post(t, ts.URL+"/rooms/"+otp+"/messages",
		map[string]string{"sender": "alice", "text": "yo"})

	// fetch room — bob should owe
	resp, _ := http.Get(ts.URL + "/rooms/" + otp)
	room2 := roomFrom(t, resp)
	if room2["waitingOn"] != "bob" {
		t.Fatalf("expected waitingOn=bob, got %v", room2["waitingOn"])
	}
}

// --- mode ---

func TestSetMode(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)

	updated := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/mode",
		map[string]string{"mode": "mediate"}))
	if updated["mode"] != "mediate" {
		t.Fatalf("expected mediate, got %q", updated["mode"])
	}
}

// --- delete ---

func TestDeleteRoom(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/rooms/"+otp, nil)
	resp, _ := http.DefaultClient.Do(req)
	deleted := roomFrom(t, resp)
	if deleted["phase"] != "closed" {
		t.Fatalf("expected closed, got %q", deleted["phase"])
	}
}

func TestDeleteRoom_notFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/rooms/AAAAAA", nil)
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

// --- get ---

func TestGetRoom(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)

	resp, _ := http.Get(ts.URL + "/rooms/" + otp)
	fetched := roomFrom(t, resp)
	if fetched["otp"] != otp {
		t.Fatalf("expected otp %q, got %q", otp, fetched["otp"])
	}
}

func TestGetRoom_notFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, _ := http.Get(ts.URL + "/rooms/BBBBBB")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

// --- end-to-end flow ---

func TestFullFlow(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// Create
	room := roomFrom(t, post(t, ts.URL+"/rooms", map[string]string{"handle": "alice"}))
	otp := room["otp"].(string)
	if room["phase"] != "waiting" {
		t.Fatalf("want waiting, got %q", room["phase"])
	}

	// Join
	joined := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/join",
		map[string]string{"handle": "bob"}))
	if joined["phase"] != "joined" {
		t.Fatalf("want joined, got %q", joined["phase"])
	}

	// Both present → active
	active := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/participants",
		map[string]string{"handle": "bob", "status": "present"}))
	if active["phase"] != "active" {
		t.Fatalf("want active, got %q", active["phase"])
	}

	// Send message
	var msgBody map[string]any
	decode(t, post(t, ts.URL+"/rooms/"+otp+"/messages",
		map[string]string{"sender": "alice", "text": "ping"}), &msgBody)
	msg := msgBody["message"].(map[string]any)
	msgID := msg["id"].(string)

	// Verify waitingOn
	resp, _ := http.Get(ts.URL + "/rooms/" + otp)
	r := roomFrom(t, resp)
	if r["waitingOn"] != "bob" {
		t.Fatalf("want waitingOn=bob, got %v", r["waitingOn"])
	}

	// Ack
	acked := roomFrom(t, post(t, ts.URL+"/rooms/"+otp+"/messages/"+msgID+"/ack", nil))
	if acked["waitingOn"] != nil {
		t.Fatalf("want waitingOn=nil after ack, got %v", acked["waitingOn"])
	}

	// Close
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/rooms/"+otp, nil)
	closed := roomFrom(t, mustDo(t, req))
	if closed["phase"] != "closed" {
		t.Fatalf("want closed, got %q", closed["phase"])
	}

	// Closed room rejects joins
	rejoin := post(t, ts.URL+"/rooms/"+otp+"/join", map[string]string{"handle": "carol"})
	defer rejoin.Body.Close()
	if rejoin.StatusCode != http.StatusGone {
		t.Fatalf("want 410 on closed room join, got %d", rejoin.StatusCode)
	}
}

func mustDo(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// --- CORS ---

func TestCORSHeaders(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/rooms", strings.NewReader(""))
	resp, _ := http.DefaultClient.Do(req)
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS header")
	}
}
