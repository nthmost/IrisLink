package main

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

const authHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>IrisLink — Claude Login</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    background: #0a0a14;
    color: #00d4ff;
    font-family: 'Courier New', monospace;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
  }
  .card {
    border: 1px solid #1a3a5c;
    padding: 2.5rem 3rem;
    max-width: 480px;
    width: 100%;
  }
  h1 {
    font-size: 1.1rem;
    letter-spacing: 0.2em;
    color: #00d4ff;
    margin-bottom: 0.4rem;
  }
  .sub {
    color: #4a7a9b;
    font-size: 0.8rem;
    margin-bottom: 2rem;
  }
  .step {
    color: #4a7a9b;
    font-size: 0.8rem;
    margin-bottom: 0.5rem;
  }
  .step a {
    color: #c678dd;
    text-decoration: none;
  }
  .step a:hover { text-decoration: underline; }
  input[type="password"] {
    width: 100%;
    background: #0d0d1a;
    border: 1px solid #1a3a5c;
    color: #ffffff;
    font-family: monospace;
    font-size: 0.9rem;
    padding: 0.6rem 0.8rem;
    margin: 1rem 0;
    outline: none;
  }
  input[type="password"]:focus { border-color: #00d4ff; }
  button {
    background: #1a3a5c;
    color: #00d4ff;
    border: 1px solid #00d4ff;
    font-family: monospace;
    font-size: 0.9rem;
    padding: 0.5rem 1.5rem;
    cursor: pointer;
    letter-spacing: 0.1em;
  }
  button:hover { background: #00d4ff; color: #0a0a14; }
  .done {
    color: #00d4ff;
    font-size: 1rem;
    text-align: center;
    padding: 1rem 0;
  }
  .done .check { font-size: 2rem; display: block; margin-bottom: 0.5rem; }
</style>
</head>
<body>
<div class="card">
  <h1>IRISLINK</h1>
  <div class="sub">claude authentication</div>
  <div class="step">1. <a href="https://platform.claude.com/settings/keys" target="_blank">Get API Key ↗</a></div>
  <div class="step">2. Navigate to API Keys and create or copy a key.</div>
  <div class="step">3. Paste it below.</div>
  <form method="POST" action="/">
    <input type="password" name="key" placeholder="sk-ant-..." autofocus autocomplete="off">
    <br>
    <button type="submit">CONNECT</button>
  </form>
</div>
</body>
</html>`

const doneHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>IrisLink — Connected</title>
<style>
  body {
    background: #0a0a14;
    color: #00d4ff;
    font-family: 'Courier New', monospace;
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100vh;
  }
  .card { border: 1px solid #1a3a5c; padding: 2.5rem 3rem; text-align: center; }
  .check { font-size: 2.5rem; margin-bottom: 1rem; }
  h1 { font-size: 1rem; letter-spacing: 0.2em; }
  p { color: #4a7a9b; font-size: 0.8rem; margin-top: 0.5rem; }
</style>
</head>
<body>
<div class="card">
  <div class="check">✓</div>
  <h1>CONNECTED</h1>
  <p>You can close this tab and return to IrisLink.</p>
</div>
</body>
</html>`

// startAuthReceiver starts a local HTTP server on a random port.
// It returns a channel that emits the API key once submitted, the port, and any error.
// The server shuts itself down after receiving one valid key.
func startAuthReceiver() (<-chan string, int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, fmt.Errorf("could not start auth receiver: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	keyCh := make(chan string, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err == nil {
				key := strings.TrimSpace(r.FormValue("key"))
				if key != "" {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					fmt.Fprint(w, doneHTML)
					keyCh <- key
					// shut down after response is flushed
					go srv.Close()
					return
				}
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, authHTML)
	})

	go srv.Serve(ln) //nolint:errcheck
	return keyCh, port, nil
}
