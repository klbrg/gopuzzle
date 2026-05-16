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

// pythonRunner runs Python solutions via the stdlib `unittest` module
// (no third-party dependency required) and snippets via direct
// `python3` invocation. Convention for code-kind puzzles:
//
//	main.py        -- the student's / canonical solution
//	test_main.py   -- a unittest module with TestCase classes that
//	                   import from `main`.
//
// `python3 -m unittest test_main` exits 0 if every test passes; any
// other exit is treated as a failure.
type pythonRunner struct{}

func (p *pythonRunner) Run(solutionCode, testCode string) (*Result, error) {
	dir, err := os.MkdirTemp("", "pypuzzle-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	files := map[string]string{
		"main.py":      solutionCode,
		"test_main.py": testCode,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, pythonBin(), "-m", "unittest", "-v", "test_main")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()

	output := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return &Result{Passed: false, Output: "Timed out after 10 seconds."}, nil
	}

	// unittest exits 0 only when every test passes.
	passed := err == nil
	return &Result{Passed: passed, Output: output}, nil
}

func (p *pythonRunner) RunSnippet(code string) (string, error) {
	dir, err := os.MkdirTemp("", "pypuzzle-snippet-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := os.WriteFile(filepath.Join(dir, "main.py"), []byte(code), 0644); err != nil {
		return "", fmt.Errorf("writing main.py: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, pythonBin(), "main.py")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("snippet timed out after 10s")
	}
	if err != nil {
		return string(out), fmt.Errorf("python: %w\n%s", err, string(out))
	}
	return string(out), nil
}

// pythonBin returns "python3" when on PATH, otherwise falls back to
// "python". macOS Homebrew Python ships as python3; Linux distros vary.
func pythonBin() string {
	if _, err := exec.LookPath("python3"); err == nil {
		return "python3"
	}
	return "python"
}
