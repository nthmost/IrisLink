package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	ilcrypto "github.com/nthmost/IrisLink/internal/crypto"
	"github.com/nthmost/IrisLink/internal/proxy"
	"github.com/nthmost/IrisLink/internal/server"
	"github.com/nthmost/IrisLink/internal/state"
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
	case "server":
		runServer()
	case "proxy":
		runProxy()
	case "otp":
		runOTP()
	case "room-id":
		runRoomID()
	case "pending":
		runPending()
	case "send":
		runSend()
	case "events":
		runEvents()
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
  create <handle> [mode]      Create room, register hook, start poller
  join <otp> <handle> [mode]  Join room, register hook, start poller
  leave                       Close room, kill poller, deregister hook

Infrastructure:
  server                      Start the rendezvous server (default port 4173)
  proxy                       Start the connector proxy (default port 8357)

Low-level / debug:
  otp                         Generate a random 6-char OTP
  room-id <otp>               Derive room_id from OTP via HKDF
  pending write <otp> <rid>   Write ~/.irislink/rooms/pending.json
  pending clear               Remove pending.json
  pending connector           Print connector URL from config
  send <url> <otp> <from> <text>  POST a message via connector
  events <url> <otp> [since]  GET events from connector
  mediate <mode> <text>       Transform text via LiteLLM (relay|mediate|game-master)
  hook                        UserPromptSubmit hook (reads JSON stdin)
  version                     Print version`)
}

// --- server ---

func runServer() {
	port := envOr("PORT", "4173")
	srv := server.New()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	srv.StartSweep(ctx)
	addr := ":" + port
	fmt.Fprintf(os.Stderr, "IrisLink rendezvous server on %s\n", addr)
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}
	go func() {
		<-ctx.Done()
		httpSrv.Shutdown(context.Background())
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

// --- proxy ---

func runProxy() {
	port := envOr("CONNECTOR_PORT", "8357")
	if len(os.Args) >= 4 && os.Args[2] == "--listen" {
		port = os.Args[3]
	}
	rendezvous := envOr("IRISLINK_BASE_URL", "http://localhost:4173")
	addr := ":" + port
	fmt.Fprintf(os.Stderr, "IrisLink connector proxy on %s → %s\n", addr, rendezvous)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	httpSrv := &http.Server{Addr: addr, Handler: proxy.Handler(rendezvous)}
	go func() {
		<-ctx.Done()
		httpSrv.Shutdown(context.Background())
	}()
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
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
		fatalf("usage: irislink pending <write|clear|connector>")
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
	case "connector":
		fmt.Println(state.ReadConfig().ConnectorURL)
	default:
		fatalf("unknown pending subcommand: %s", os.Args[2])
	}
}

// --- send ---

func runSend() {
	if len(os.Args) < 6 {
		fatalf("usage: irislink send <connector_url> <otp> <sender> <text>")
	}
	connURL, otp, sender, text := os.Args[2], os.Args[3], os.Args[4], os.Args[5]
	payload, _ := json.Marshal(map[string]string{"room_otp": otp, "sender": sender, "text": text})
	resp, err := http.Post(connURL+"/message", "application/json", bytes.NewReader(payload))
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
}

// --- events ---

func runEvents() {
	if len(os.Args) < 4 {
		fatalf("usage: irislink events <connector_url> <otp> [since]")
	}
	connURL, otp := os.Args[2], os.Args[3]
	since := "0"
	if len(os.Args) >= 5 {
		since = os.Args[4]
	}
	url := fmt.Sprintf("%s/events?room_otp=%s&since=%s", connURL, otp, since)
	resp, err := http.Get(url)
	if err != nil {
		fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(os.Stdout, resp.Body)
	fmt.Println()
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

	// Let explicit /irislink commands pass through
	if strings.HasPrefix(strings.TrimSpace(event.Prompt), "/irislink") {
		os.Exit(0)
	}

	p, err := state.ReadPending()
	if err != nil || p == nil || p.OTP == "" {
		os.Exit(0)
	}

	cfg := state.ReadConfig()
	connURL := cfg.ConnectorURL

	// Read meta for handle/mode/cursor
	meta := state.ReadMeta(p.OTP)
	handle, mode, cursor := meta.Handle, meta.Mode, meta.Cursor

	// Read recent log lines
	inbound := readLog(p.OTP, 5)

	context := fmt.Sprintf(`## Active IrisLink Session

OTP: %s
Your handle: %s
Mode: %s
Connector: %s

**Relay this message to the IrisLink room before responding.**

Steps:
1. If mode is not relay, run: irislink mediate %s %q
   Show mediated version and confirm before sending.
2. Send: irislink send %s %s %s %q
3. Check inbound: irislink events %s %s %s

Recent inbound messages:
%s

After relaying, respond normally. To exit relay mode: /irislink leave`,
		p.OTP, handle, mode, connURL,
		mode, event.Prompt,
		connURL, p.OTP, handle, event.Prompt,
		connURL, p.OTP, strconv.FormatInt(cursor, 10),
		inbound,
	)

	json.NewEncoder(os.Stdout).Encode(map[string]string{"additionalContext": context})
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
