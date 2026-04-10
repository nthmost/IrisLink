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

// Mediate rewrites msg for clarity (mediate mode) or adds GM narrative
// (game-master mode). Returns original msg if mode is relay or no API key.
// model may be "" to use the default mediation model.
func Mediate(apiKey, model, mode, msg string) (string, error) {
	if apiKey == "" || mode == "relay" {
		return msg, nil
	}
	if model == "" {
		model = modelMediate
	}

	var prompt string
	switch mode {
	case "mediate":
		prompt = fmt.Sprintf(
			"Rewrite the following message to be clearer and more considerate, keeping the original meaning. Output only the rewritten message, nothing else.\n\nMessage: %s",
			msg)
	case "game-master":
		prompt = fmt.Sprintf(
			"You are a creative game master. Add a brief narrative flourish to accompany this message. Output the original message followed by a GM note in italics (using *asterisks*), nothing else.\n\nMessage: %s",
			msg)
	default:
		return msg, nil
	}

	return callClaudeModel(apiKey, model, prompt)
}
