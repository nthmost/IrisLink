package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

func irisDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".irislink")
}

func roomsDir() string {
	return filepath.Join(irisDir(), "rooms")
}

// Pending represents ~/.irislink/rooms/pending.json
type Pending struct {
	OTP    string `json:"otp"`
	RoomID string `json:"room_id"`
}

func WritePending(otp, roomID string) error {
	if err := os.MkdirAll(roomsDir(), 0o755); err != nil {
		return err
	}
	p := Pending{OTP: otp, RoomID: roomID}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(roomsDir(), "pending.json"), data, 0o644)
}

func ReadPending() (*Pending, error) {
	data, err := os.ReadFile(filepath.Join(roomsDir(), "pending.json"))
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func ClearPending() error {
	path := filepath.Join(roomsDir(), "pending.json")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Config represents ~/.irislink/config.json
type Config struct {
	BrokerURL    string `json:"broker_url"`
	Username     string `json:"broker_user"`
	Password     string `json:"broker_pass"`
	ClaudeAPIKey string `json:"claude_api_key,omitempty"`
	ClaudeModel  string `json:"claude_model,omitempty"` // model for mediation; default: claude-sonnet-4-6
}

func defaultConfig() Config {
	return Config{BrokerURL: "mqtt://localhost:1883"}
}

// WriteConfig persists a Config back to ~/.irislink/config.json.
func WriteConfig(c Config) error {
	if err := os.MkdirAll(irisDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(irisDir(), "config.json"), data, 0o644)
}

func ReadConfig() Config {
	data, err := os.ReadFile(filepath.Join(irisDir(), "config.json"))
	if err != nil {
		return defaultConfig()
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return defaultConfig()
	}
	if c.BrokerURL == "" {
		c.BrokerURL = "mqtt://localhost:1883"
	}
	return c
}

// BrokerAddr returns host:port suitable for net.Dial from the broker_url.
func (c Config) BrokerAddr() string {
	url := c.BrokerURL
	// strip scheme
	for _, prefix := range []string{"mqtts://", "mqtt://", "tcp://"} {
		if len(url) > len(prefix) && url[:len(prefix)] == prefix {
			url = url[len(prefix):]
			break
		}
	}
	// add default port if missing
	if !containsColon(url) {
		url += ":1883"
	}
	return url
}

// AppendLog appends a message to the session's JSONL history log.
func AppendLog(otp, sender, text string) {
	type entry struct {
		TS     time.Time `json:"ts"`
		Sender string    `json:"sender"`
		Text   string    `json:"text"`
	}
	data, err := json.Marshal(entry{TS: time.Now(), Sender: sender, Text: text})
	if err != nil {
		return
	}
	path := filepath.Join(roomsDir(), otp+".log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(data, '\n')) //nolint:errcheck
}

func containsColon(s string) bool {
	for _, c := range s {
		if c == ':' {
			return true
		}
	}
	return false
}

// Meta represents ~/.irislink/rooms/<otp>.meta
type Meta struct {
	Handle          string `json:"handle"`
	Mode            string `json:"mode"`
	Cursor          int64  `json:"cursor"`
	MaxParticipants int    `json:"max_participants"` // 0 = unlimited, default 2
}

func WriteMeta(otp string, m Meta) error {
	if err := os.MkdirAll(roomsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(roomsDir(), otp+".meta"), data, 0o644)
}

func ReadMeta(otp string) Meta {
	m := Meta{Handle: "operator", Mode: "relay", Cursor: 0, MaxParticipants: 2}
	data, err := os.ReadFile(filepath.Join(roomsDir(), otp+".meta"))
	if err != nil {
		return m
	}
	var parsed Meta
	if json.Unmarshal(data, &parsed) == nil {
		if parsed.Handle != "" {
			m.Handle = parsed.Handle
		}
		if parsed.Mode != "" {
			m.Mode = parsed.Mode
		}
		m.Cursor = parsed.Cursor
	}
	return m
}
