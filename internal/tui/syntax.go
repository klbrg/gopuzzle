package tui

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// predictFormatHint infers a short description of the expected output's
// shape, so the student knows whether to type one token, a bracketed
// slice, several values, multiple lines, etc. Deliberately structural —
// it doesn't reveal the actual values.
func predictFormatHint(expected string) string {
	e := strings.TrimRight(expected, "\n")
	if e == "" {
		return ""
	}
	if strings.Contains(e, "\n") {
		n := strings.Count(e, "\n") + 1
		return fmt.Sprintf("Output spans %d lines — ctrl+j adds a newline, enter submits.", n)
	}
	fields := strings.Fields(e)
	n := len(fields)
	switch {
	case strings.HasPrefix(e, "map["):
		return "Format: a Go map like `map[key:value key:value]` (one space between pairs)."
	case strings.HasPrefix(e, "[") && strings.HasSuffix(e, "]"):
		return "Format: a Go slice like `[v1 v2 v3]` (one space between elements)."
	case n == 1:
		return "Format: a single token."
	default:
		return fmt.Sprintf("Format: %d tokens separated by single spaces.", n)
	}
}

// cleanTestOutput strips the `go test -v` framework chatter (=== RUN,
// --- FAIL, FAIL, ok lines, etc.) so only the actual t.Errorf messages
// and compile errors remain. Falls back to the original output if the
// cleanup would leave nothing useful.
func cleanTestOutput(raw string) string {
	var keep []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "=== RUN") ||
			strings.HasPrefix(trimmed, "=== PAUSE") ||
			strings.HasPrefix(trimmed, "=== CONT") ||
			strings.HasPrefix(trimmed, "--- PASS") ||
			strings.HasPrefix(trimmed, "--- FAIL") ||
			strings.HasPrefix(trimmed, "--- SKIP") ||
			trimmed == "PASS" ||
			trimmed == "FAIL" ||
			strings.HasPrefix(trimmed, "ok  ") ||
			strings.HasPrefix(trimmed, "FAIL\t") ||
			strings.HasPrefix(trimmed, "exit status ") {
			continue
		}
		keep = append(keep, line)
	}
	if len(keep) == 0 {
		return raw
	}
	return strings.Join(keep, "\n")
}

// stripLeadingComments drops leading // comment lines and empty lines,
// so the user's scratch (which has the puzzle header comments prepended)
// can be compared against a canonical solution that doesn't.
func stripLeadingComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		return strings.TrimSpace(strings.Join(lines[i:], "\n"))
	}
	return ""
}

// kindBadge renders a small tag describing a puzzle's kind, suitable for
// appending to a puzzle row in the browse list. Colors hint at the flow
// (e.g. predict/quiz are interactive, fix/code involve the editor).
func kindBadge(kind string) string {
	var label string
	var color lipgloss.AdaptiveColor
	switch kind {
	case "predict_output":
		label = "predict"
		color = lipgloss.AdaptiveColor{Light: "#5f5fd7", Dark: "#87afff"}
	case "quiz":
		label = "quiz"
		color = lipgloss.AdaptiveColor{Light: "#8700af", Dark: "#af87ff"}
	case "fix":
		label = "fix"
		color = lipgloss.AdaptiveColor{Light: "#af5f00", Dark: "#ffaf5f"}
	default: // "code" or empty
		label = "code"
		color = lipgloss.AdaptiveColor{Light: "#5f8700", Dark: "#87d75f"}
	}
	return lipgloss.NewStyle().Foreground(color).Render("[" + label + "]")
}

// renderCodeBlock formats a code block (or output snippet) inside the
// app's bordered box. When isGo is true the body is syntax-highlighted as
// Go; otherwise it falls back to styleOutput (gray plain text).
func renderCodeBlock(content string, isGo bool) string {
	body := strings.TrimSpace(content)
	if isGo {
		body = highlightGo(body)
	} else {
		body = styleOutput.Render(body)
	}
	return styleBorder.Render(body)
}

// highlightGo returns the given Go source as ANSI-styled text suitable for a
// 256-color terminal. On any error it falls back to the plain input — the TUI
// stays readable even if chroma can't tokenise something.
func highlightGo(code string) string {
	if strings.TrimSpace(code) == "" {
		return code
	}
	lexer := lexers.Get("go")
	if lexer == nil {
		return code
	}
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return code
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return code
	}
	// Chroma adds a trailing newline; the surrounding lipgloss border looks
	// cleaner without it.
	return strings.TrimRight(buf.String(), "\n")
}
