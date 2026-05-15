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

	// systemPrompt is shared across requests; ephemeral cache control on it
	// gives us a 5-minute prompt-cache window so back-to-back reviews are
	// cheaper.
	systemPrompt = `You are a senior Go developer giving a short, friendly review of a beginner's solution to a puzzle from "Learning Go, 2nd Edition" Chapter 2.

Rules:
- Keep your response to 2-4 sentences total. Be concise.
- Focus on whether the code is idiomatic Go and what (if anything) you'd change.
- If the solution matches the canonical or is already clean, say so briefly and stop.
- Be encouraging but honest. Plain prose only — no headings, no lists, no markdown formatting.
- The student is learning the basics. Don't reach for advanced patterns they haven't met yet.`
)

// ReviewRequest is the per-puzzle context passed to Review.
type ReviewRequest struct {
	Title       string
	Description string
	Canonical   string
	UserCode    string
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
// student's solution. Errors include a missing API key and HTTP failures.
func Review(ctx context.Context, r ReviewRequest) (string, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not set")
	}

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
