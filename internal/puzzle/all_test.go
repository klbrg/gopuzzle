package puzzle

import (
	"path/filepath"
	"testing"

	"github.com/klbrg/gopuzzle/internal/runner"
)

// TestAllSolutionsPass loads every puzzle YAML and runs the canonical
// solution against its own test_code. Catches authoring bugs in bulk.
func TestAllSolutionsPass(t *testing.T) {
	absDir, err := filepath.Abs(filepath.Join("..", "..", "puzzles"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	Dir = absDir

	puzzles, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(puzzles) == 0 {
		t.Fatal("no puzzles loaded")
	}

	for _, p := range puzzles {
		t.Run(p.ID+"_"+p.Stem, func(t *testing.T) {
			t.Parallel()
			if p.Solution == "" {
				t.Skip("no solution provided")
			}
			if p.TestCode == "" {
				t.Fatalf("missing test_code")
			}
			res, err := runner.Run(p.Solution, p.TestCode)
			if err != nil {
				t.Fatalf("runner error: %v", err)
			}
			if !res.Passed {
				t.Fatalf("canonical solution failed its own tests:\n%s", res.Output)
			}
		})
	}
}
