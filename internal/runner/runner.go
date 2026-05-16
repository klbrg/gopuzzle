// Package runner executes puzzle code in a sandbox per source language.
// Each language has its own Runner implementation; the For(lang) factory
// returns the right one.
//
// Today only Go is wired up. The interface is shaped so adding a Python
// runner (Phase 2 of docs/multi-language-plan.md) is a self-contained
// addition rather than a refactor.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Result is the outcome of a Runner.Run call.
type Result struct {
	Passed bool
	Output string
}

// Lang identifies a source language.
type Lang = string

const (
	LangGo     Lang = "go"
	LangPython Lang = "python" // runner not yet implemented; will fail loudly via For
)

// Runner is the language-agnostic interface for compiling, testing, and
// running puzzle code.
type Runner interface {
	// Run compiles and runs solutionCode against testCode (the test
	// file or its equivalent for the language). Returns pass/fail plus
	// combined output.
	Run(solutionCode, testCode string) (*Result, error)

	// RunSnippet executes a self-contained program and returns stdout.
	// Used to verify predict_output and fix puzzles.
	RunSnippet(code string) (string, error)
}

// For returns the Runner implementation for the given language. An empty
// string defaults to Go. Unknown languages return a Runner that errors
// on every call with a clear message — so puzzles authored for a
// language whose runner isn't built yet fail loudly rather than silently.
func For(lang Lang) Runner {
	switch lang {
	case "", LangGo:
		return &goRunner{}
	case LangPython:
		return &pythonRunner{}
	default:
		return &unsupportedRunner{lang: lang}
	}
}

// ---- Go runner ----

const goModTemplate = `module puzzle

go 1.23
`

type goRunner struct{}

func (g *goRunner) Run(solutionCode, testCode string) (*Result, error) {
	dir, err := os.MkdirTemp("", "gopuzzle-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	files := map[string]string{
		"solution.go":      solutionCode,
		"solution_test.go": testCode,
		"go.mod":           goModTemplate,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "test", "-v", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	output := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return &Result{Passed: false, Output: "Timed out after 10 seconds."}, nil
	}

	passed := err == nil && strings.Contains(output, "PASS")
	return &Result{Passed: passed, Output: output}, nil
}

func (g *goRunner) RunSnippet(code string) (string, error) {
	dir, err := os.MkdirTemp("", "gopuzzle-snippet-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0644); err != nil {
		return "", fmt.Errorf("writing main.go: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goModTemplate), 0644); err != nil {
		return "", fmt.Errorf("writing go.mod: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("snippet timed out after 10s")
	}
	if err != nil {
		return string(out), fmt.Errorf("go run: %w\n%s", err, string(out))
	}
	return string(out), nil
}

// ---- Unsupported fallback ----

type unsupportedRunner struct{ lang Lang }

func (u *unsupportedRunner) Run(string, string) (*Result, error) {
	return nil, fmt.Errorf("runner for language %q is not implemented yet", u.lang)
}

func (u *unsupportedRunner) RunSnippet(string) (string, error) {
	return "", fmt.Errorf("runner for language %q is not implemented yet", u.lang)
}
