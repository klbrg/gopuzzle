// Package ai talks to the Anthropic Messages API to produce a short review
// of a user's solution. ANTHROPIC_API_KEY is read from the environment.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	apiURL     = "https://api.anthropic.com/v1/messages"
	apiVersion = "2023-06-01"
	model      = "claude-sonnet-4-6"

	// reviewSystem and hintSystem are ephemeral-cached so back-to-back
	// AI calls in the same 5-minute window hit the prompt cache.
	reviewSystem = `You are a senior Go developer giving a short, friendly review of a beginner's solution to a puzzle from "Learning Go, 2nd Edition".

Rules:
- Keep your response to 2-4 sentences total. Be concise.
- Focus on whether the code is idiomatic Go and what (if anything) you'd change.
- If the solution matches the canonical or is already clean, say so briefly and stop.
- Be encouraging but honest. Plain prose only — no headings, no lists, no markdown formatting.
- The student is learning the basics. Don't reach for advanced patterns they haven't met yet.`

	hintSystem = `You are a senior Go developer giving a short, targeted hint to a beginner whose code FAILED its tests in a puzzle from "Learning Go, 2nd Edition".

Rules:
- Keep your response to 2-4 sentences total. Be concise.
- Identify the specific mistake in the student's reasoning or code, based on the failing tests.
- Give a HINT, not the answer. Point at the rule or concept they're missing; suggest what to try.
- DO NOT paste a corrected version of their code or the canonical solution. They are learning by struggling productively — don't deprive them of that.
- Be encouraging. Plain prose only — no headings, no lists, no markdown formatting.
- The student is learning the basics. Don't reach for advanced patterns they haven't met yet.`
)

// ReviewRequest is the per-puzzle context passed to Review (PASS case).
type ReviewRequest struct {
	Title       string
	Description string
	Canonical   string
	UserCode    string
}

// HintRequest is the per-puzzle context passed to Hint (FAIL case). It
// intentionally does NOT include the canonical solution — we don't want
// the model tempted to leak it as a "hint".
type HintRequest struct {
	Title       string
	Description string
	UserCode    string
	Failure     string
}

type apiRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    []systemBlock  `json:"system"`
	Messages  []messageBlock `json:"messages"`
}

type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type messageBlock struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error,omitempty"`
	Usage   *usage         `json:"usage,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type usage struct {
	InputTokens             int `json:"input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	OutputTokens            int `json:"output_tokens"`
}

// Review calls the Anthropic API and returns a short text review of the
// student's passing solution.
func Review(ctx context.Context, r ReviewRequest) (string, error) {
	userPrompt := fmt.Sprintf(`Puzzle: %s

%s

Canonical solution:
%s

Student's solution:
%s

Give the student a short review.`,
		r.Title,
		strings.TrimSpace(r.Description),
		strings.TrimSpace(r.Canonical),
		strings.TrimSpace(r.UserCode),
	)
	return call(ctx, reviewSystem, userPrompt)
}

// Hint calls the Anthropic API and returns a short, targeted hint for a
// failing attempt — pointing at the rule or concept being missed without
// pasting a corrected version of the code.
func Hint(ctx context.Context, r HintRequest) (string, error) {
	userPrompt := fmt.Sprintf(`Puzzle: %s

%s

Student's code:
%s

Test failure:
%s

Give the student one short hint to help them unstick. Do NOT paste a corrected version of their code.`,
		r.Title,
		strings.TrimSpace(r.Description),
		strings.TrimSpace(r.UserCode),
		strings.TrimSpace(r.Failure),
	)
	return call(ctx, hintSystem, userPrompt)
}

// call performs the actual Anthropic Messages API request with prompt
// caching on the system block.
func call(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}
	body := apiRequest{
		Model:     model,
		MaxTokens: 400,
		System: []systemBlock{{
			Type:         "text",
			Text:         systemPrompt,
			CacheControl: &cacheControl{Type: "ephemeral"},
		}},
		Messages: []messageBlock{{
			Role:    "user",
			Content: userPrompt,
		}},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	var out apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("%s: %s", out.Error.Type, out.Error.Message)
	}
	if len(out.Content) == 0 {
		return "", fmt.Errorf("empty response")
	}
	var sb strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			sb.WriteString(b.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}
