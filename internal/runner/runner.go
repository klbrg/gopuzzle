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

type Result struct {
	Passed bool
	Output string
}

const goModTemplate = `module puzzle

go 1.23
`

func Run(solutionCode, testCode string) (*Result, error) {
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

// RunSnippet compiles a self-contained main package and returns its stdout.
// Used to verify predict-output puzzles by actually running the snippet.
func RunSnippet(code string) (string, error) {
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
