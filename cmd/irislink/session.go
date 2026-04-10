package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	ilcrypto "github.com/nthmost/IrisLink/internal/crypto"
	"github.com/nthmost/IrisLink/internal/state"
	"github.com/nthmost/IrisLink/internal/transport"
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

	otp, err := ilcrypto.GenerateOTP()
	if err != nil {
		fatal(err)
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
	if err := startPoller(otp, handle, mode); err != nil {
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

	roomID, err := ilcrypto.DeriveRoomID(otp)
	if err != nil {
		fatal(err)
	}

	// Verify broker is reachable before writing state
	cfg := state.ReadConfig()
	key, err := ilcrypto.DeriveEncKey(otp)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := transport.Connect(ctx, cfg.BrokerAddr(), roomID, handle, key, func(transport.Envelope) {})
	if err != nil {
		fatalf("cannot connect to broker: %v\nCheck broker_url in ~/.irislink/config.json", err)
	}
	// Publish presence
	client.Publish(ctx, transport.Envelope{Type: "presence", Text: "joined"})
	client.Disconnect(ctx)

	if err := state.WritePending(otp, roomID); err != nil {
		fatal(err)
	}
	if err := state.WriteMeta(otp, state.Meta{Handle: handle, Mode: mode}); err != nil {
		fatal(err)
	}
	if err := registerHook(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not register hook: %v\n", err)
	}
	if err := startPoller(otp, handle, mode); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not start poller: %v\n", err)
	}

	fmt.Printf("\n ___      _     _     _       _\n|_ _|_ __(_)___| |   (_)_ __ | | __\n | || '__| / __| |   | | '_ \\| |/ /\n | || |  | \\__ \\ |___| | | | |   <\n|___|_|  |_|___/_____|_|_| |_|_|\\_\\\n\nconnected  •  room: %s  •  mode: %s\n\nJust type your messages. /irislink leave when done.\n\n", otp, mode)
}

// runLeave: irislink leave
func runLeave() {
	p, err := state.ReadPending()
	if err != nil || p == nil || p.OTP == "" {
		fmt.Println("no active room")
		return
	}
	otp := p.OTP

	// Publish leave presence
	cfg := state.ReadConfig()
	meta := state.ReadMeta(otp)
	key, err := ilcrypto.DeriveEncKey(otp)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if client, err := transport.Connect(ctx, cfg.BrokerAddr(), p.RoomID, meta.Handle, key, func(transport.Envelope) {}); err == nil {
			client.Publish(ctx, transport.Envelope{Type: "presence", Text: "left"})
			client.Disconnect(ctx)
		}
	}

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

// runPoll: irislink poll <otp> <handle> <mode>
// Spawned as a detached background process by create/join.
func runPoll() {
	if len(os.Args) < 5 {
		fatalf("usage: irislink poll <otp> <handle> <mode>")
	}
	otp := os.Args[2]
	handle := os.Args[3]
	// mode := os.Args[4]  // reserved for future mediation

	p, err := state.ReadPending()
	if err != nil || p == nil {
		os.Exit(1)
	}

	key, err := ilcrypto.DeriveEncKey(otp)
	if err != nil {
		os.Exit(1)
	}

	cfg := state.ReadConfig()
	home, _ := os.UserHomeDir()
	logPath := filepath.Join(home, ".irislink", "rooms", otp+".log")

	for {
		ctx := context.Background()
		client, err := transport.Connect(ctx, cfg.BrokerAddr(), p.RoomID, handle, key, func(env transport.Envelope) {
			if env.Type == "presence" {
				if env.Text == "joined" {
					fmt.Fprintln(os.Stderr, "\n ___      _     _     _       _    ")
					fmt.Fprintln(os.Stderr, "|_ _|_ __(_)___| |   (_)_ __ | | __")
					fmt.Fprintln(os.Stderr, " | || '__| / __| |   | | '_ \\| |/ /")
					fmt.Fprintln(os.Stderr, " | || |  | \\__ \\ |___| | | | |   < ")
					fmt.Fprintln(os.Stderr, "|___|_|  |_|___/_____|_|_| |_|_|\\_\\")
					fmt.Fprintf(os.Stderr, "\n%s joined — just type!\n\n", env.Sender)
				} else if env.Text == "left" {
					fmt.Fprintf(os.Stderr, "\n[%s left the room]\n", env.Sender)
					f, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
					if f != nil {
						fmt.Fprintf(f, "[%s left]\n", env.Sender)
						f.Close()
					}
				}
				return
			}
			// message
			ts := time.UnixMilli(env.Timestamp).Format("15:04:05")
			line := fmt.Sprintf("[%s] %s: %s\n", ts, env.Sender, env.Text)
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
			if err == nil {
				f.WriteString(line)
				f.Close()
			}
		})
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		// Check if pending is still active; if not, disconnect and exit
		for {
			time.Sleep(10 * time.Second)
			if _, err := state.ReadPending(); err != nil {
				client.Disconnect(context.Background())
				return
			}
			_ = client
		}
	}
}

func startPoller(otp, handle, mode string) error {
	exe, err := os.Executable()
	if err != nil {
		exe = "irislink"
	}
	cmd := exec.Command(exe, "poll", otp, handle, mode)
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
			return nil
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
