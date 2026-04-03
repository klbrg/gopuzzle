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
