package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/nthmost/IrisLink/internal/transport"
)

const (
	apiURL        = "https://api.anthropic.com/v1/messages"
	apiVer        = "2023-06-01"
	modelContext  = "claude-haiku-4-5-20251001"  // fast/cheap for context selection
	modelMediate  = "claude-sonnet-4-6"           // mediate + game-master
	maxCtx        = 200 * 1024                    // 200 KB total file budget
	maxFileSize   = 50 * 1024                     // 50 KB per individual file
)

// skipDirs are directories we never walk into when collecting context.
var skipDirs = map[string]bool{
	"node_modules":     true,
	".git":             true,
	"irislink-context": true,
	".cache":           true,
	"vendor":           true,
	"dist":             true,
	"build":            true,
}

// apiRequest is the minimal Anthropic messages request body.
type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	Messages  []apiMessage `json:"messages"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func callClaude(apiKey, prompt string) (string, error) {
	return callClaudeModel(apiKey, modelContext, prompt)
}

func callClaudeModel(apiKey, model, prompt string) (string, error) {
	body, err := json.Marshal(apiRequest{
		Model:     model,
		MaxTokens: 8192,
		Messages:  []apiMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", apiVer)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("claude API error: %s", resp.Status)
	}

	var ar apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", err
	}
	if len(ar.Content) == 0 {
		return "", fmt.Errorf("empty response from claude")
	}
	return ar.Content[0].Text, nil
}

// collectFiles walks dir, returns map[relpath]content up to maxCtx bytes total.
func collectFiles(dir string) map[string]string {
	files := map[string]string{}
	total := 0

	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		// skip large files quickly
		info, err := d.Info()
		if err != nil || info.Size() > int64(maxFileSize) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// skip binary content
		if !utf8.Valid(data) {
			return nil
		}
		if total+len(data) > maxCtx {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		files[rel] = string(data)
		total += len(data)
		return nil
	})
	return files
}

// SelectContext reads files in dir, asks Claude which excerpts are relevant to
// msg, and returns ContextBlocks to attach to the outgoing envelope.
// Returns nil if no API key is provided or nothing relevant is found.
func SelectContext(apiKey, msg, dir string) ([]transport.ContextBlock, error) {
	if apiKey == "" {
		return nil, nil
	}

	files := collectFiles(dir)
	if len(files) == 0 {
		return nil, nil
	}

	// Build full file contents for the prompt.
	var sb strings.Builder
	for rel, content := range files {
		fmt.Fprintf(&sb, "### %s\n%s\n\n", rel, content)
	}

	prompt := fmt.Sprintf(
		`Given this message: '%s'

Which of these files are most relevant to include as context for the recipient? Return a JSON array of {"source": "<filename>", "content": "<full file content>"} including the complete content of each relevant file. Only include files that are genuinely relevant. Return an empty JSON array [] if nothing is relevant. Return ONLY the JSON array, no other text.

Files:
%s`, msg, sb.String())

	text, err := callClaude(apiKey, prompt)
	if err != nil {
		return nil, err
	}

	text = strings.TrimSpace(text)
	// Extract JSON array if wrapped in markdown code fences.
	if idx := strings.Index(text, "["); idx >= 0 {
		text = text[idx:]
	}
	if idx := strings.LastIndex(text, "]"); idx >= 0 {
		text = text[:idx+1]
	}

	var blocks []transport.ContextBlock
	if err := json.Unmarshal([]byte(text), &blocks); err != nil {
		return nil, nil // best-effort: ignore parse errors
	}
	return blocks, nil
}

// Ask responds to a direct query (triggered by "Hey Claude" in a message).
// It answers concisely without rewriting or mediating.
func Ask(apiKey, model, query string) (string, error) {
	if apiKey == "" {
		return "", fmt.Errorf("no API key")
	}
	if model == "" {
		model = modelMediate
	}
	prompt := fmt.Sprintf("Answer the following question or request directly and concisely. Do not add preamble.\n\n%s", query)
	return callClaudeModel(apiKey, model, prompt)
}

// MediateResult is the structured response from Mediate.
// If Send is true, Text is the (possibly rewritten) message to forward.
// If Send is false, Text contains clarifying questions to show the sender locally.
type MediateResult struct {
	Send bool
	Text string
}

// mediateJSON is the JSON shape Claude returns for mediate mode.
type mediateJSON struct {
	Action string `json:"action"` // "send" or "clarify"
	Text   string `json:"text"`
}

// Mediate rewrites msg for clarity (mediate mode) or adds GM narrative
// (game-master mode). Returns original msg if mode is relay or no API key.
// In mediate mode, Claude may instead return clarifying questions (Send=false).
// model may be "" to use the default mediation model.
func Mediate(apiKey, model, mode, msg string) (MediateResult, error) {
	passthrough := MediateResult{Send: true, Text: msg}
	if apiKey == "" || mode == "relay" {
		return passthrough, nil
	}
	if model == "" {
		model = modelMediate
	}

	switch mode {
	case "mediate":
		prompt := fmt.Sprintf(`You are mediating a conversation to keep it constructive and clear.

Message: %q

If this message is clear and expresses a coherent intent (even if blunt or emotionally charged), rewrite it to be clearer and more considerate while preserving the core meaning.

Only if the message is genuinely ambiguous — meaning you would need to make a significant guess about what the sender intends — ask 1-2 targeted clarifying questions instead. Set a high bar: most messages should be rewritten and sent.

Respond with JSON only, no other text:
{"action": "send", "text": "<rewritten message>"}
or
{"action": "clarify", "text": "<your 1-2 clarifying questions>"}`, msg)

		raw, err := callClaudeModel(apiKey, model, prompt)
		if err != nil {
			return passthrough, err
		}
		// Extract JSON from response.
		raw = strings.TrimSpace(raw)
		if i := strings.Index(raw, "{"); i >= 0 {
			raw = raw[i:]
		}
		if i := strings.LastIndex(raw, "}"); i >= 0 {
			raw = raw[:i+1]
		}
		var result mediateJSON
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			// Parse failure: fall back to sending original.
			return passthrough, nil
		}
		if result.Text == "" {
			return passthrough, nil
		}
		return MediateResult{Send: result.Action != "clarify", Text: result.Text}, nil

	case "game-master":
		prompt := fmt.Sprintf(
			"You are a creative game master. Add a brief narrative flourish to accompany this message. Output the original message followed by a GM note in italics (using *asterisks*), nothing else.\n\nMessage: %s",
			msg)
		text, err := callClaudeModel(apiKey, model, prompt)
		if err != nil {
			return passthrough, err
		}
		return MediateResult{Send: true, Text: text}, nil

	default:
		return passthrough, nil
	}
}
