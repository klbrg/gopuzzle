package puzzle

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/klbrg/gopuzzle/internal/runner"
)

// TestAllSolutionsPass loads every puzzle YAML and verifies the canonical
// content runs correctly. For code puzzles it runs the solution against
// test_code; for predict-output puzzles it runs the snippet and checks the
// recorded expected_output matches actual stdout. Catches authoring bugs
// in bulk.
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
			switch p.Kind {
			case KindCode:
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
			case KindPredictOutput:
				if p.Snippet == "" {
					t.Fatalf("missing snippet")
				}
				if p.ExpectedOutput == "" {
					t.Fatalf("missing expected_output")
				}
				out, err := runner.RunSnippet(p.Snippet)
				if err != nil {
					t.Fatalf("snippet runner error: %v\n%s", err, out)
				}
				if normalize(out) != normalize(p.ExpectedOutput) {
					t.Fatalf("snippet output mismatch:\nWANT:\n%q\nGOT:\n%q", p.ExpectedOutput, out)
				}
			case KindQuiz:
				if p.Question == "" {
					t.Fatalf("missing question")
				}
				if len(p.Choices) < 2 {
					t.Fatalf("quiz needs at least 2 choices, got %d", len(p.Choices))
				}
				if p.Answer == "" {
					t.Fatalf("missing answer")
				}
				found := false
				for _, c := range p.Choices {
					if c == p.Answer {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("answer %q is not one of the choices %v", p.Answer, p.Choices)
				}
			case KindFix:
				if p.Solution == "" {
					t.Fatalf("missing solution (the canonical fixed code)")
				}
				if p.ExpectedOutput == "" {
					t.Fatalf("missing expected_output")
				}
				out, err := runner.RunSnippet(p.Solution)
				if err != nil {
					t.Fatalf("fixed-snippet runner error: %v\n%s", err, out)
				}
				if normalize(out) != normalize(p.ExpectedOutput) {
					t.Fatalf("fix output mismatch:\nWANT:\n%q\nGOT:\n%q", p.ExpectedOutput, out)
				}
			default:
				t.Fatalf("unknown puzzle kind %q", p.Kind)
			}
		})
	}
}

func normalize(s string) string {
	return strings.TrimRight(s, " \t\n")
}
