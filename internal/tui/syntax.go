package tui

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/klbrg/gopuzzle/internal/puzzle"
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
		return "Format: a Go-formatted map (`map[key:value key:value]`)."
	case strings.HasPrefix(e, "{") && strings.HasSuffix(e, "}"):
		return "Format: a dict / set / mapping in braces (e.g. `{'a': 1, 'b': 2}`)."
	case strings.HasPrefix(e, "[") && strings.HasSuffix(e, "]"):
		return "Format: a list / slice in brackets (e.g. `[v1, v2, v3]`)."
	case n == 1:
		return "Format: a single token."
	default:
		return fmt.Sprintf("Format: %d tokens.", n)
	}
}

// normalizePrediction loosens the predict-output comparison so trivial
// formatting differences don't fail otherwise-correct answers. Per line:
//   - leading and trailing whitespace is trimmed
//   - whitespace adjacent to a non-word character (e.g. `, `, ` ]`,
//     ` = `) is collapsed away
//
// So `[1, 1]` and `[1,1]` compare equal, and `a = 1` matches `a=1`. But
// `hello world` still differs from `helloworld` because the space sits
// between two word characters.
func normalizePrediction(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		line = wsAfterNonWord.ReplaceAllString(line, "$1")
		line = wsBeforeNonWord.ReplaceAllString(line, "$1")
		lines[i] = line
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n \t")
}

var (
	wsAfterNonWord  = regexp.MustCompile(`(\W)\s+`)
	wsBeforeNonWord = regexp.MustCompile(`\s+(\W)`)
)

// cleanTestOutput strips the framework chatter from a test runner's
// output so only the actually-useful diagnostic remains. Branches per
// language since `go test -v` and `python -m unittest -v` have very
// different output shapes. Falls back to the original output if the
// cleanup would leave nothing useful.
func cleanTestOutput(raw, lang string) string {
	switch lang {
	case "python":
		return cleanUnittestOutput(raw)
	default:
		return cleanGoTestOutput(raw)
	}
}

func cleanGoTestOutput(raw string) string {
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

// cleanUnittestOutput strips the `python -m unittest -v` framework
// chatter: separator lines (rows of `=` or `-`), the "Ran N tests in"
// summary, and the trailing `OK` / `FAILED (...)` status. Keeps the
// per-test "test_x ... ok/FAIL" lines and the FAIL: blocks with
// tracebacks, which is what the student needs.
func cleanUnittestOutput(raw string) string {
	var keep []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if isAllOf(trimmed, '=') || isAllOf(trimmed, '-') {
			continue
		}
		if strings.HasPrefix(trimmed, "Ran ") && strings.Contains(trimmed, " test") {
			continue
		}
		if trimmed == "OK" || strings.HasPrefix(trimmed, "FAILED (") {
			continue
		}
		keep = append(keep, line)
	}
	if len(keep) == 0 {
		return raw
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

func isAllOf(s string, c byte) bool {
	if len(s) < 3 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}

// stripLeadingComments drops leading comment and empty lines so the
// user's scratch (which has the puzzle header comments prepended) can
// be compared against a canonical solution that doesn't. Handles both
// `//` (Go) and `#` (Python).
func stripLeadingComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return strings.TrimSpace(strings.Join(lines[i:], "\n"))
	}
	return ""
}

// displayLanguage humanises a language ID for the UI ("go" -> "Go").
func displayLanguage(lang string) string {
	switch lang {
	case "go":
		return "Go"
	case "python":
		return "Python"
	case "":
		return "(unknown language)"
	default:
		// Capitalise first byte (good enough for short lowercase IDs).
		return strings.ToUpper(lang[:1]) + lang[1:]
	}
}

// codeLang returns the language string to pass to renderCodeBlock for
// content that is genuinely code (the user's submitted solution, the
// canonical solution, a fix-puzzle scratch). Quiz answers and
// predict-output expected outputs are plain text, not code — pass ""
// directly in those cases instead of calling this.
func codeLang(p *puzzle.Puzzle) string {
	if p == nil {
		return ""
	}
	switch p.Kind {
	case puzzle.KindCode, puzzle.KindFix:
		return p.Lang
	default:
		return ""
	}
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
// app's bordered box. When lang is "go" or "python" the body is
// syntax-highlighted with chroma; otherwise (empty string, unknown
// language, or plain output) it falls back to styleOutput.
func renderCodeBlock(content, lang string) string {
	body := strings.TrimSpace(content)
	if highlighted, ok := highlightCode(body, lang); ok {
		body = highlighted
	} else {
		body = styleOutput.Render(body)
	}
	return styleBorder.Render(body)
}

// highlightCode returns the given source as ANSI-styled text using
// chroma's lexer for `lang`. Returns (highlighted, true) on success,
// ("", false) when no lexer matched or an error occurred — the caller
// then falls back to plain styling so the TUI stays readable.
func highlightCode(code, lang string) (string, bool) {
	if lang == "" || strings.TrimSpace(code) == "" {
		return "", false
	}
	lexer := lexers.Get(lang)
	if lexer == nil {
		return "", false
	}
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", false
	}
	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return "", false
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "", false
	}
	// Chroma adds a trailing newline; the surrounding lipgloss border
	// looks cleaner without it.
	return strings.TrimRight(buf.String(), "\n"), true
}
