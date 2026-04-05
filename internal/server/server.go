package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	otpAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	ttlDuration = 15 * time.Minute
	sweepEvery  = 60 * time.Second
)

var otpPattern = regexp.MustCompile(`^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{6}$`)

type Participant struct {
	Handle string `json:"handle"`
	Status string `json:"status"`
}

type Message struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
}

type room struct {
	OTP          string        `json:"otp"`
	Mode         string        `json:"mode"`
	Participants []Participant `json:"participants"`
	Messages     []Message     `json:"messages"`
	CreatedAt    int64         `json:"createdAt"`
	ExpiresAt    int64         `json:"expiresAt"`
	ClosedAt     *int64        `json:"closedAt"`
}

type publicRoom struct {
	OTP          string        `json:"otp"`
	Mode         string        `json:"mode"`
	Phase        string        `json:"phase"`
	TTLSeconds   int           `json:"ttlSeconds"`
	Participants []Participant `json:"participants"`
	Messages     []Message     `json:"messages"`
	WaitingOn    *string       `json:"waitingOn"`
	CreatedAt    int64         `json:"createdAt"`
	ClosedAt     *int64        `json:"closedAt"`
}

type Server struct {
	mu    sync.RWMutex
	rooms map[string]*room
}

func New() *Server {
	return &Server{rooms: make(map[string]*room)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /rooms", s.createRoom)
	mux.HandleFunc("GET /rooms/{otp}", s.getRoom)
	mux.HandleFunc("DELETE /rooms/{otp}", s.deleteRoom)
	mux.HandleFunc("POST /rooms/{otp}/join", s.joinRoom)
	mux.HandleFunc("POST /rooms/{otp}/participants", s.updateParticipant)
	mux.HandleFunc("POST /rooms/{otp}/mode", s.setMode)
	mux.HandleFunc("POST /rooms/{otp}/messages", s.postMessage)
	mux.HandleFunc("POST /rooms/{otp}/messages/{msgID}/ack", s.ackMessage)
	return corsMiddleware(mux)
}

func (s *Server) StartSweep(ctx context.Context) {
	go func() {
		t := time.NewTicker(sweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep()
			}
		}
	}()
}

func (s *Server) sweep() {
	now := time.Now().UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rooms {
		if r.ClosedAt == nil && now > r.ExpiresAt {
			t := now
			r.ClosedAt = &t
		}
	}
}

// --- helpers ---

func nowMs() int64 { return time.Now().UnixMilli() }

func generateOTP() string {
	b := make([]byte, 6)
	for i := range b {
		b[i] = otpAlphabet[rand.Intn(len(otpAlphabet))]
	}
	return string(b)
}

func msgID() string {
	return fmt.Sprintf("%d-%06x", nowMs(), rand.Intn(1<<24))
}

func derivePhase(r *room) string {
	if r.ClosedAt != nil || nowMs() > r.ExpiresAt {
		return "closed"
	}
	if len(r.Participants) <= 1 {
		return "waiting"
	}
	for _, p := range r.Participants {
		if p.Status != "present" {
			return "joined"
		}
	}
	return "active"
}

func waitingOn(r *room) *string {
	if len(r.Messages) == 0 {
		return nil
	}
	last := r.Messages[len(r.Messages)-1]
	if last.Status == "acknowledged" {
		return nil
	}
	for _, p := range r.Participants {
		if p.Handle != last.Sender {
			return &p.Handle
		}
	}
	return nil
}

func toPublic(r *room) publicRoom {
	now := nowMs()
	ttl := int((r.ExpiresAt - now) / 1000)
	if ttl < 0 {
		ttl = 0
	}
	return publicRoom{
		OTP:          r.OTP,
		Mode:         r.Mode,
		Phase:        derivePhase(r),
		TTLSeconds:   ttl,
		Participants: r.Participants,
		Messages:     r.Messages,
		WaitingOn:    waitingOn(r),
		CreatedAt:    r.CreatedAt,
		ClosedAt:     r.ClosedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func validateOTP(w http.ResponseWriter, otp string) bool {
	if !otpPattern.MatchString(strings.ToUpper(otp)) {
		errJSON(w, http.StatusBadRequest, "invalid OTP format")
		return false
	}
	return true
}

func (s *Server) getAndCheck(w http.ResponseWriter, otp string) *room {
	r := s.rooms[otp]
	if r == nil {
		errJSON(w, http.StatusNotFound, "room not found")
		return nil
	}
	if r.ClosedAt != nil || nowMs() > r.ExpiresAt {
		t := nowMs()
		if r.ClosedAt == nil {
			r.ClosedAt = &t
		}
		errJSON(w, http.StatusGone, "room closed")
		return nil
	}
	return r
}

// --- handlers ---

func (s *Server) createRoom(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Handle string `json:"handle"`
	}
	json.NewDecoder(req.Body).Decode(&body)
	handle := body.Handle
	if handle == "" {
		handle = "operator"
	}
	now := nowMs()
	otp := generateOTP()
	r := &room{
		OTP:          otp,
		Mode:         "relay",
		Participants: []Participant{{Handle: handle, Status: "present"}},
		Messages:     []Message{},
		CreatedAt:    now,
		ExpiresAt:    now + int64(ttlDuration/time.Millisecond),
	}
	s.mu.Lock()
	s.rooms[otp] = r
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"room": toPublic(r)})
}

func (s *Server) getRoom(w http.ResponseWriter, req *http.Request) {
	otp := strings.ToUpper(req.PathValue("otp"))
	if !validateOTP(w, otp) {
		return
	}
	s.mu.RLock()
	r := s.rooms[otp]
	s.mu.RUnlock()
	if r == nil {
		errJSON(w, http.StatusNotFound, "room not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"room": toPublic(r)})
}

func (s *Server) deleteRoom(w http.ResponseWriter, req *http.Request) {
	otp := strings.ToUpper(req.PathValue("otp"))
	if !validateOTP(w, otp) {
		return
	}
	s.mu.Lock()
	r := s.rooms[otp]
	if r == nil {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.ClosedAt == nil {
		t := nowMs()
		r.ClosedAt = &t
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"room": toPublic(r)})
}

func (s *Server) joinRoom(w http.ResponseWriter, req *http.Request) {
	otp := strings.ToUpper(req.PathValue("otp"))
	if !validateOTP(w, otp) {
		return
	}
	var body struct {
		Handle string `json:"handle"`
	}
	json.NewDecoder(req.Body).Decode(&body)
	if body.Handle == "" {
		errJSON(w, http.StatusBadRequest, "handle required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.getAndCheck(w, otp)
	if r == nil {
		return
	}
	found := false
	for i := range r.Participants {
		if r.Participants[i].Handle == body.Handle {
			r.Participants[i].Status = "present"
			found = true
			break
		}
	}
	if !found {
		if len(r.Participants) >= 2 {
			errJSON(w, http.StatusConflict, "room already has two handles")
			return
		}
		r.Participants = append(r.Participants, Participant{Handle: body.Handle, Status: "joined"})
	}
	r.ExpiresAt = nowMs() + int64(ttlDuration/time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]any{"room": toPublic(r)})
}

func (s *Server) updateParticipant(w http.ResponseWriter, req *http.Request) {
	otp := strings.ToUpper(req.PathValue("otp"))
	if !validateOTP(w, otp) {
		return
	}
	var body struct {
		Handle string `json:"handle"`
		Status string `json:"status"`
	}
	json.NewDecoder(req.Body).Decode(&body)
	if body.Handle == "" || body.Status == "" {
		errJSON(w, http.StatusBadRequest, "handle and status required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.getAndCheck(w, otp)
	if r == nil {
		return
	}
	for i := range r.Participants {
		if r.Participants[i].Handle == body.Handle {
			r.Participants[i].Status = body.Status
			writeJSON(w, http.StatusOK, map[string]any{"room": toPublic(r)})
			return
		}
	}
	errJSON(w, http.StatusNotFound, "handle not in room")
}

func (s *Server) setMode(w http.ResponseWriter, req *http.Request) {
	otp := strings.ToUpper(req.PathValue("otp"))
	if !validateOTP(w, otp) {
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	json.NewDecoder(req.Body).Decode(&body)
	if body.Mode == "" {
		errJSON(w, http.StatusBadRequest, "mode required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.getAndCheck(w, otp)
	if r == nil {
		return
	}
	r.Mode = body.Mode
	writeJSON(w, http.StatusOK, map[string]any{"room": toPublic(r)})
}

func (s *Server) postMessage(w http.ResponseWriter, req *http.Request) {
	otp := strings.ToUpper(req.PathValue("otp"))
	if !validateOTP(w, otp) {
		return
	}
	var body struct {
		Sender string `json:"sender"`
		Text   string `json:"text"`
	}
	json.NewDecoder(req.Body).Decode(&body)
	if body.Sender == "" || body.Text == "" {
		errJSON(w, http.StatusBadRequest, "sender and text required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.getAndCheck(w, otp)
	if r == nil {
		return
	}
	msg := Message{
		ID:        msgID(),
		Sender:    body.Sender,
		Text:      body.Text,
		Status:    "pending",
		Timestamp: nowMs(),
	}
	r.Messages = append(r.Messages, msg)
	r.ExpiresAt = nowMs() + int64(ttlDuration/time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}

func (s *Server) ackMessage(w http.ResponseWriter, req *http.Request) {
	otp := strings.ToUpper(req.PathValue("otp"))
	msgID := req.PathValue("msgID")
	if !validateOTP(w, otp) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.getAndCheck(w, otp)
	if r == nil {
		return
	}
	for i := range r.Messages {
		if r.Messages[i].ID == msgID {
			r.Messages[i].Status = "acknowledged"
			writeJSON(w, http.StatusOK, map[string]any{"room": toPublic(r)})
			return
		}
	}
	errJSON(w, http.StatusNotFound, "message not found")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
