package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/nthmost/IrisLink/internal/state"
)

const version = "0.1.0"

func Handler(rendezvousURL string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", statusHandler(rendezvousURL))
	mux.HandleFunc("GET /rooms/pending.json", pendingHandler)
	mux.HandleFunc("POST /message", messageHandler(rendezvousURL))
	mux.HandleFunc("GET /events", eventsHandler(rendezvousURL))
	mux.HandleFunc("POST /ack", ackHandler(rendezvousURL))
	return corsMiddleware(mux)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func errJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"detail": msg})
}

func statusHandler(rendezvousURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		p, err := state.ReadPending()
		attached := err == nil && p != nil
		otp := ""
		roomID := ""
		if attached {
			otp = p.OTP
			roomID = p.RoomID
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"version":       version,
			"room_attached": attached,
			"room_otp":      otp,
			"room_id":       roomID,
			"rendezvous_url": rendezvousURL,
		})
	}
}

func pendingHandler(w http.ResponseWriter, _ *http.Request) {
	p, err := state.ReadPending()
	if err != nil {
		errJSON(w, http.StatusNotFound, "no pending room")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func messageHandler(rendezvousURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			RoomOTP string `json:"room_otp"`
			Sender  string `json:"sender"`
			Text    string `json:"text"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.RoomOTP == "" {
			errJSON(w, http.StatusBadRequest, "room_otp, sender and text required")
			return
		}
		payload, _ := json.Marshal(map[string]string{"sender": body.Sender, "text": body.Text})
		resp, err := http.Post(
			fmt.Sprintf("%s/rooms/%s/messages", rendezvousURL, body.RoomOTP),
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			errJSON(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(data)
	}
}

func eventsHandler(rendezvousURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		otp := req.URL.Query().Get("room_otp")
		sinceStr := req.URL.Query().Get("since")
		if otp == "" {
			errJSON(w, http.StatusBadRequest, "room_otp required")
			return
		}
		since, _ := strconv.ParseInt(sinceStr, 10, 64)

		resp, err := http.Get(fmt.Sprintf("%s/rooms/%s", rendezvousURL, otp))
		if err != nil {
			errJSON(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			errJSON(w, http.StatusNotFound, "room not found")
			return
		}
		if resp.StatusCode == http.StatusGone {
			errJSON(w, http.StatusGone, "room closed")
			return
		}

		var payload struct {
			Room struct {
				Messages   []map[string]any `json:"messages"`
				Phase      string           `json:"phase"`
				TTLSeconds int              `json:"ttlSeconds"`
				WaitingOn  *string          `json:"waitingOn"`
			} `json:"room"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			errJSON(w, http.StatusBadGateway, "invalid response from rendezvous")
			return
		}

		var newEvents []map[string]any
		var next int64 = since
		for _, m := range payload.Room.Messages {
			ts, _ := m["timestamp"].(float64)
			if int64(ts) > since {
				newEvents = append(newEvents, m)
				if int64(ts) > next {
					next = int64(ts)
				}
			}
		}
		if newEvents == nil {
			newEvents = []map[string]any{}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"events":     newEvents,
			"next":       next,
			"phase":      payload.Room.Phase,
			"ttlSeconds": payload.Room.TTLSeconds,
			"waitingOn":  payload.Room.WaitingOn,
		})
	}
}

func ackHandler(rendezvousURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			RoomOTP   string `json:"room_otp"`
			MessageID string `json:"message_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body.RoomOTP == "" || body.MessageID == "" {
			errJSON(w, http.StatusBadRequest, "room_otp and message_id required")
			return
		}
		url := fmt.Sprintf("%s/rooms/%s/messages/%s/ack", rendezvousURL, body.RoomOTP, body.MessageID)
		resp, err := http.Post(url, "application/json", strings.NewReader("{}"))
		if err != nil {
			errJSON(w, http.StatusBadGateway, err.Error())
			return
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(data)
	}
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
