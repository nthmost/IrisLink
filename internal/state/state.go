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
	ConnectorURL string `json:"connector_url"`
}

func ReadConfig() Config {
	data, err := os.ReadFile(filepath.Join(irisDir(), "config.json"))
	if err != nil {
		return Config{ConnectorURL: "http://localhost:8357"}
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{ConnectorURL: "http://localhost:8357"}
	}
	if c.ConnectorURL == "" {
		c.ConnectorURL = "http://localhost:8357"
	}
	return c
}
