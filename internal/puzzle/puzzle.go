package puzzle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Kind identifies how a puzzle is presented and verified.
const (
	KindCode          = "code"           // template + test_code + solution (default)
	KindPredictOutput = "predict_output" // show snippet, accept the printed output as the answer
	KindQuiz          = "quiz"           // multiple-choice question, pick the letter
	KindFix           = "fix"            // edit broken main, verified by running and matching expected_output
)

type Puzzle struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Concept     string `yaml:"concept"`
	Kind        string `yaml:"kind"` // default: "code"
	Lang        string `yaml:"lang"` // default: derived from Source path; falls back to "go"
	Description string `yaml:"description"`
	Hint        string `yaml:"hint"`
	Explanation string `yaml:"explanation"`
	Reference   string `yaml:"reference"`

	// KindCode fields (also used by KindFix; for fix the template is broken).
	Template string `yaml:"template"`
	TestCode string `yaml:"test_code"`
	Solution string `yaml:"solution"`

	// KindPredictOutput and KindFix share these.
	Snippet        string `yaml:"snippet"`         // code shown for predict_output
	ExpectedOutput string `yaml:"expected_output"` // stdout the program should produce

	// KindQuiz fields
	Question string   `yaml:"question"`
	Choices  []string `yaml:"choices"`
	Answer   string   `yaml:"answer"` // must equal one of the choices

	// Set at load time from the file path.
	Source  string `yaml:"-"` // e.g. "gobyexample", "learning_go"
	Section string `yaml:"-"` // e.g. "01_basics", "ch03"
	Dir     string `yaml:"-"` // full relative dir: "gobyexample/01_basics"
	Stem    string `yaml:"-"`
}

// Dir is the path to the puzzles directory, set by main.
var Dir string

// inferLang derives a language from the source directory name when the
// YAML doesn't supply an explicit `lang:` field. Today's sources:
//
//	learning_go/...    -> "go"
//	effective_python/... -> "python"
//
// Anything else defaults to "go" so existing Go puzzles keep working
// without churn.
func inferLang(source string) string {
	s := strings.ToLower(source)
	switch {
	case strings.HasSuffix(s, "_python"), strings.Contains(s, "python"):
		return "python"
	default:
		return "go"
	}
}

func LoadAll() ([]*Puzzle, error) {
	var puzzles []*Puzzle

	err := filepath.WalkDir(Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".yaml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		var p Puzzle
		if err := yaml.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		rel, _ := filepath.Rel(Dir, path)
		p.Dir = filepath.Dir(rel)
		parts := strings.SplitN(p.Dir, string(filepath.Separator), 2)
		p.Source = parts[0]
		if len(parts) > 1 {
			p.Section = parts[1]
		}
		p.Stem = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if p.Kind == "" {
			p.Kind = KindCode
		}
		if p.Lang == "" {
			p.Lang = inferLang(p.Source)
		}
		puzzles = append(puzzles, &p)
		return nil
	})
	return puzzles, err
}
