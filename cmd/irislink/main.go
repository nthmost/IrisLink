package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	ilcrypto "github.com/nthmost/IrisLink/internal/crypto"
	"github.com/nthmost/IrisLink/internal/state"
	"github.com/nthmost/IrisLink/internal/transport"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "create":
		runCreate()
	case "join":
		runJoin()
	case "leave":
		runLeave()
	case "poll":
		runPoll()
	case "otp":
		runOTP()
	case "room-id":
		runRoomID()
	case "pending":
		runPending()
	case "send":
		runSend()
	case "mediate":
		runMediate()
	case "hook":
		runHook()
	case "version":
		fmt.Println("irislink 0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `irislink — Claude-to-Claude pairing tool

Session commands:
  create <handle> [mode]       Create room, register hook, start poller
  join <otp> <handle> [mode]   Join room, register hook, start poller
  leave                        Close room, kill poller, deregister hook

Low-level / debug:
  otp                          Generate a random 6-char OTP
  room-id <otp>                Derive room_id from OTP via HKDF
  pending write <otp> <rid>    Write ~/.irislink/rooms/pending.json
  pending clear                Remove pending.json
  send <otp> <text>            Publish a message to the active room
  mediate <mode> <text>        Transform text via LiteLLM (relay|mediate|game-master)
  hook                         UserPromptSubmit hook (reads JSON stdin)
  version                      Print version

Config (~/.irislink/config.json):
  broker_url   MQTT broker URL (default: mqtt://localhost:1883)
  broker_user  Optional broker username
  broker_pass  Optional broker password`)
}

// --- otp ---

func runOTP() {
	otp, err := ilcrypto.GenerateOTP()
	if err != nil {
		fatal(err)
	}
	fmt.Println(otp)
}

// --- room-id ---

func runRoomID() {
	if len(os.Args) < 3 {
		fatalf("usage: irislink room-id <otp>")
	}
	id, err := ilcrypto.DeriveRoomID(strings.ToUpper(os.Args[2]))
	if err != nil {
		fatal(err)
	}
	fmt.Println(id)
}

// --- pending ---

func runPending() {
	if len(os.Args) < 3 {
		fatalf("usage: irislink pending <write|clear>")
	}
	switch os.Args[2] {
	case "write":
		if len(os.Args) < 5 {
			fatalf("usage: irislink pending write <otp> <room_id>")
		}
		if err := state.WritePending(os.Args[3], os.Args[4]); err != nil {
			fatal(err)
		}
		fmt.Println("ok")
	case "clear":
		if err := state.ClearPending(); err != nil {
			fatal(err)
		}
		fmt.Println("ok")
	default:
		fatalf("unknown pending subcommand: %s", os.Args[2])
	}
}

// --- send ---

func runSend() {
	if len(os.Args) < 4 {
		fatalf("usage: irislink send <otp> <text>")
	}
	otp := strings.ToUpper(os.Args[2])
	text := os.Args[3]

	p, err := state.ReadPending()
	if err != nil || p == nil {
		fatalf("no active room")
	}

	key, err := ilcrypto.DeriveEncKey(otp)
	if err != nil {
		fatal(err)
	}
	meta := state.ReadMeta(otp)
	cfg := state.ReadConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := transport.Connect(ctx, cfg.BrokerAddr(), p.RoomID, meta.Handle, key, func(transport.Envelope) {}, cfg.Username, cfg.Password)
	if err != nil {
		fatal(err)
	}
	defer client.Disconnect(ctx)

	if err := client.Publish(ctx, transport.Envelope{Type: "message", Text: text}); err != nil {
		fatal(err)
	}
	fmt.Println("sent")
}

// --- mediate ---

func runMediate() {
	if len(os.Args) < 4 {
		fatalf("usage: irislink mediate <relay|mediate|game-master> <text>")
	}
	mode, text := os.Args[2], os.Args[3]

	if mode == "relay" {
		fmt.Println(text)
		return
	}

	model := "loki/qwen-coder-14b"
	systemPrompt := "You are a thoughtful relay. Rewrite the following message to be clearer and more considerate, keeping the original meaning. Output only the rewritten message."
	if mode == "game-master" {
		model = "loki/qwen3-coder-30b"
		systemPrompt = "You are a creative game master mediating a collaborative session. Add a brief narrative flourish or creative prompt to accompany this message. Output the original message followed by a GM note in italics."
	}

	payload, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": text},
		},
	})

	req, _ := http.NewRequest("POST", "http://spartacus.local:4000/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer dummy")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil || len(result.Choices) == 0 {
		fatalf("unexpected response from LiteLLM")
	}
	fmt.Println(result.Choices[0].Message.Content)
}

// --- hook ---

func runHook() {
	var event struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&event); err != nil {
		os.Exit(0)
	}

	if strings.HasPrefix(strings.TrimSpace(event.Prompt), "/irislink") {
		os.Exit(0)
	}

	p, err := state.ReadPending()
	if err != nil || p == nil || p.OTP == "" {
		os.Exit(0)
	}

	meta := state.ReadMeta(p.OTP)
	inbound := readLog(p.OTP, 5)

	additionalContext := fmt.Sprintf(`## Active IrisLink Session

Your handle: %s  •  mode: %s  •  broker: %s

**Before responding, relay this message to the IrisLink room:**

%s

Steps:
1. If mode is not relay: irislink mediate %s %q — show result and confirm.
2. Send: irislink send %s %q
3. Recent inbound messages (also in ~/.irislink/rooms/%s.log):
%s

After relaying, respond normally. /irislink leave to exit.`,
		meta.Handle, meta.Mode, state.ReadConfig().BrokerURL,
		event.Prompt,
		meta.Mode, event.Prompt,
		p.OTP, event.Prompt,
		p.OTP,
		inbound,
	)

	json.NewEncoder(os.Stdout).Encode(map[string]string{"additionalContext": additionalContext})
}

// --- helpers ---

func readLog(otp string, n int) string {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(home + "/.irislink/rooms/" + otp + ".log")
	if err != nil {
		return "(none yet)"
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "(none yet)"
	}
	return strings.Join(lines, "\n")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
