package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	ilcrypto "github.com/nthmost/IrisLink/internal/crypto"
	"github.com/nthmost/IrisLink/internal/state"
)

// runCreate: irislink create <handle> [mode]
func runCreate() {
	handle := "operator"
	mode := "relay"
	if len(os.Args) >= 3 {
		handle = os.Args[2]
	}
	if len(os.Args) >= 4 {
		mode = os.Args[3]
	}

	cfg := state.ReadConfig()

	payload, _ := json.Marshal(map[string]string{"handle": handle})
	resp, err := http.Post(cfg.RendezvousURL+"/rooms", "application/json", bytes.NewReader(payload))
	if err != nil {
		fatalf("cannot reach rendezvous server at %s: %v\nStart it with: irislink server &", cfg.RendezvousURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Room struct {
			OTP string `json:"otp"`
		} `json:"room"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Room.OTP == "" {
		fatalf("unexpected response from server: %s", string(body))
	}
	otp := result.Room.OTP

	roomID, err := ilcrypto.DeriveRoomID(otp)
	if err != nil {
		fatal(err)
	}

	if err := state.WritePending(otp, roomID); err != nil {
		fatal(err)
	}
	if err := state.WriteMeta(otp, state.Meta{Handle: handle, Mode: mode}); err != nil {
		fatal(err)
	}

	if err := registerHook(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register hook: %v\n", err)
	}
	if err := startPoller(otp, handle, cfg.ConnectorURL); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not start poller: %v\n", err)
	}

	fmt.Printf("\n╔══════════════════════╗\n║  IrisLink: %s   ║\n║  mode: %-13s║\n╚══════════════════════╝\n\nShare this code with your partner.\nWaiting for them to join — once connected, just type.\n\n", otp, mode)
}

// runJoin: irislink join <otp> <handle> [mode]
func runJoin() {
	if len(os.Args) < 4 {
		fatalf("usage: irislink join <otp> <handle> [mode]")
	}
	otp := strings.ToUpper(os.Args[2])
	handle := os.Args[3]
	mode := "relay"
	if len(os.Args) >= 5 {
		mode = os.Args[4]
	}

	cfg := state.ReadConfig()

	payload, _ := json.Marshal(map[string]string{"handle": handle})
	resp, err := http.Post(cfg.RendezvousURL+"/rooms/"+otp+"/join", "application/json", bytes.NewReader(payload))
	if err != nil {
		fatalf("cannot reach rendezvous server: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case 200:
		// ok
	case 404:
		fatalf("room not found — code may be expired or incorrect")
	case 409:
		fatalf("room already has two participants")
	case 410:
		fatalf("room has expired")
	default:
		fatalf("server error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Room struct {
			Participants []struct {
				Handle string `json:"handle"`
			} `json:"participants"`
		} `json:"room"`
	}
	json.Unmarshal(body, &result)

	partner := ""
	for _, p := range result.Room.Participants {
		if p.Handle != handle {
			partner = p.Handle
		}
	}

	roomID, err := ilcrypto.DeriveRoomID(otp)
	if err != nil {
		fatal(err)
	}

	if err := state.WritePending(otp, roomID); err != nil {
		fatal(err)
	}
	if err := state.WriteMeta(otp, state.Meta{Handle: handle, Mode: mode}); err != nil {
		fatal(err)
	}

	if err := registerHook(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register hook: %v\n", err)
	}
	if err := startPoller(otp, handle, cfg.ConnectorURL); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not start poller: %v\n", err)
	}

	fmt.Printf("\n ___      _     _     _       _\n|_ _|_ __(_)___| |   (_)_ __ | | __\n | || '__| / __| |   | | '_ \\| |/ /\n | || |  | \\__ \\ |___| | | | |   <\n|___|_|  |_|___/_____|_|_| |_|_|\\_\\\n\nconnected  •  room: %s  •  mode: %s\npartner: %s\n\nJust type your messages. /irislink leave when done.\n\n", otp, mode, partner)
}

// runLeave: irislink leave
func runLeave() {
	p, err := state.ReadPending()
	if err != nil || p == nil || p.OTP == "" {
		fmt.Println("no active room")
		return
	}
	otp := p.OTP
	cfg := state.ReadConfig()

	req, _ := http.NewRequest("DELETE", cfg.RendezvousURL+"/rooms/"+otp, nil)
	http.DefaultClient.Do(req)

	killPoller(otp)
	state.ClearPending()

	home, _ := os.UserHomeDir()
	os.Remove(filepath.Join(home, ".irislink", "rooms", otp+".meta"))
	os.Remove(filepath.Join(home, ".irislink", "rooms", otp+".pid"))

	if err := deregisterHook(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not deregister hook: %v\n", err)
	}

	fmt.Printf("Left room %s. Log at ~/.irislink/rooms/%s.log\n", otp, otp)
}

// runPoll: irislink poll <otp> <handle> <connector_url>
// Spawned as a detached background process by create/join.
func runPoll() {
	if len(os.Args) < 5 {
		fatalf("usage: irislink poll <otp> <handle> <connector_url>")
	}
	otp := os.Args[2]
	handle := os.Args[3]
	connURL := os.Args[4]

	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".irislink", "rooms", otp+".log")

	prevPhase := ""
	cursor := int64(0)

	for {
		url := fmt.Sprintf("%s/events?room_otp=%s&since=%d", connURL, otp, cursor)
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			Phase  string `json:"phase"`
			Next   int64  `json:"next"`
			Events []struct {
				Sender    string `json:"sender"`
				Text      string `json:"text"`
				Timestamp int64  `json:"timestamp"`
			} `json:"events"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		if result.Phase == "active" && prevPhase != "active" {
			fmt.Fprintln(os.Stderr, "\n ___      _     _     _       _    ")
			fmt.Fprintln(os.Stderr, "|_ _|_ __(_)___| |   (_)_ __ | | __")
			fmt.Fprintln(os.Stderr, " | || '__| / __| |   | | '_ \\| |/ /")
			fmt.Fprintln(os.Stderr, " | || |  | \\__ \\ |___| | | | |   < ")
			fmt.Fprintln(os.Stderr, "|___|_|  |_|___/_____|_|_| |_|_|\\_\\")
			fmt.Fprintln(os.Stderr, "\nconnected  •  room: "+otp)
			fmt.Fprintln(os.Stderr, "partner has joined — just type!\n")
		}
		prevPhase = result.Phase

		if len(result.Events) > 0 {
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err == nil {
				for _, e := range result.Events {
					if e.Sender != handle {
						ts := time.UnixMilli(e.Timestamp).Format("15:04:05")
						fmt.Fprintf(f, "[%s] %s: %s\n", ts, e.Sender, e.Text)
					}
				}
				f.Close()
			}
		}

		if result.Next > 0 {
			cursor = result.Next
			m := state.ReadMeta(otp)
			m.Cursor = cursor
			state.WriteMeta(otp, m)
		}

		if result.Phase == "closed" {
			f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if f != nil {
				fmt.Fprintln(f, "[room closed]")
				f.Close()
			}
			return
		}

		time.Sleep(2 * time.Second)
	}
}

// startPoller spawns `irislink poll` as a detached background process.
func startPoller(otp, handle, connURL string) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "irislink"
	}
	cmd := exec.Command(exe, "poll", otp, handle, connURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	pidPath := filepath.Join(home, ".irislink", "rooms", otp+".pid")
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d\n", cmd.Process.Pid)), 0o644)
	cmd.Process.Release()
	return nil
}

// killPoller sends SIGTERM to the poller process recorded in <otp>.pid.
func killPoller(otp string) {
	home, _ := os.UserHomeDir()
	pidPath := filepath.Join(home, ".irislink", "rooms", otp+".pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			proc.Signal(syscall.SIGTERM)
		}
	}
}

// registerHook adds the irislink hook entry to ~/.claude/settings.json.
func registerHook() error {
	exe, err := os.Executable()
	if err != nil {
		exe = "irislink"
	}
	hookCmd := exe + " hook"

	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	var settings map[string]any
	if data, err := os.ReadFile(settingsPath); err == nil {
		json.Unmarshal(data, &settings)
	}
	if settings == nil {
		settings = map[string]any{}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	ups, _ := hooks["UserPromptSubmit"].([]any)
	for _, entry := range ups {
		if b, _ := json.Marshal(entry); strings.Contains(string(b), "irislink hook") {
			return nil // already registered
		}
	}
	ups = append(ups, map[string]any{
		"matcher": "",
		"hooks":   []any{map[string]any{"type": "command", "command": hookCmd}},
	})
	hooks["UserPromptSubmit"] = ups

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, out, 0o644)
}

// deregisterHook removes the irislink hook entry from ~/.claude/settings.json.
func deregisterHook() error {
	home, _ := os.UserHomeDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return nil
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}
	ups, _ := hooks["UserPromptSubmit"].([]any)
	filtered := ups[:0]
	for _, entry := range ups {
		if b, _ := json.Marshal(entry); !strings.Contains(string(b), "irislink hook") {
			filtered = append(filtered, entry)
		}
	}
	hooks["UserPromptSubmit"] = filtered

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, out, 0o644)
}
