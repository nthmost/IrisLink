package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

	cfg := state.ReadConfig()
	key, err := ilcrypto.DeriveEncKey(otp)
	if err != nil {
		fatal(err)
	}

	incoming := make(chan transport.Envelope, 32)
	ctx := context.Background()
	client, err := transport.Connect(ctx, cfg.BrokerAddr(), roomID, handle, key, func(env transport.Envelope) {
		incoming <- env
	}, cfg.Username, cfg.Password)
	if err != nil {
		fatalf("cannot connect to broker: %v\nCheck broker_url in ~/.irislink/config.json", err)
	}

	// Show the OTP in plain terminal for 5 seconds before the TUI takes over.
	fmt.Printf("\n  room created\n\n  code:  %s\n\n  share this with your partner.\n\n", otp)
	for i := 5; i > 0; i-- {
		fmt.Printf("\r  launching in %d...  ", i)
		time.Sleep(time.Second)
	}
	fmt.Println()

	runTUIWithClient(otp, handle, mode, client, incoming, cfg, true)

	client.Disconnect(context.Background())
	state.ClearPending()
	home, _ := os.UserHomeDir()
	os.Remove(filepath.Join(home, ".irislink", "rooms", otp+".meta"))
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

	cfg := state.ReadConfig()
	key, err := ilcrypto.DeriveEncKey(otp)
	if err != nil {
		fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	incoming := make(chan transport.Envelope, 32)
	client, err := transport.Connect(ctx, cfg.BrokerAddr(), roomID, handle, key, func(env transport.Envelope) {
		incoming <- env
	}, cfg.Username, cfg.Password)
	if err != nil {
		fatalf("cannot connect to broker: %v\nCheck broker_url in ~/.irislink/config.json", err)
	}

	// Publish presence.
	client.Publish(context.Background(), transport.Envelope{Type: "presence", Text: "joined"}) //nolint:errcheck

	if err := state.WritePending(otp, roomID); err != nil {
		fatal(err)
	}
	if err := state.WriteMeta(otp, state.Meta{Handle: handle, Mode: mode}); err != nil {
		fatal(err)
	}

	runTUIWithClient(otp, handle, mode, client, incoming, cfg, false)

	client.Disconnect(context.Background())
	state.ClearPending()
	home, _ := os.UserHomeDir()
	os.Remove(filepath.Join(home, ".irislink", "rooms", otp+".meta"))
}

// runLeave: irislink leave (non-TUI cleanup, kept for scripting)
func runLeave() {
	p, err := state.ReadPending()
	if err != nil || p == nil || p.OTP == "" {
		fmt.Println("no active room")
		return
	}
	otp := p.OTP

	cfg := state.ReadConfig()
	meta := state.ReadMeta(otp)
	key, err := ilcrypto.DeriveEncKey(otp)
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if client, err := transport.Connect(ctx, cfg.BrokerAddr(), p.RoomID, meta.Handle, key, func(transport.Envelope) {}, cfg.Username, cfg.Password); err == nil {
			client.Publish(ctx, transport.Envelope{Type: "presence", Text: "left"}) //nolint:errcheck
			client.Disconnect(ctx)
		}
	}

	state.ClearPending()
	home, _ := os.UserHomeDir()
	os.Remove(filepath.Join(home, ".irislink", "rooms", otp+".meta"))
	os.Remove(filepath.Join(home, ".irislink", "rooms", otp+".pid"))

	fmt.Printf("Left room %s.\n", otp)
}

// runTUIWithClient launches the bubbletea TUI with an already-connected client.
// showWaiting opens a popover asking the user to share their OTP.
func runTUIWithClient(otp, handle, mode string, client *transport.Client, incoming chan transport.Envelope, cfg state.Config, showWaiting bool) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	m := initialModel(otp, handle, mode, client, incoming, cfg, cwd, showWaiting)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
	}
}
