package state

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	ConnectorURL  string `json:"connector_url"`
	RendezvousURL string `json:"rendezvous_url"`
}

func ReadConfig() Config {
	data, err := os.ReadFile(filepath.Join(irisDir(), "config.json"))
	if err != nil {
		return Config{ConnectorURL: "http://localhost:8357", RendezvousURL: "http://localhost:4173"}
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{ConnectorURL: "http://localhost:8357", RendezvousURL: "http://localhost:4173"}
	}
	if c.ConnectorURL == "" {
		c.ConnectorURL = "http://localhost:8357"
	}
	if c.RendezvousURL == "" {
		c.RendezvousURL = "http://localhost:4173"
	}
	return c
}

// Meta represents ~/.irislink/rooms/<otp>.meta
type Meta struct {
	Handle string `json:"handle"`
	Mode   string `json:"mode"`
	Cursor int64  `json:"cursor"`
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
	m := Meta{Handle: "operator", Mode: "relay", Cursor: 0}
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
