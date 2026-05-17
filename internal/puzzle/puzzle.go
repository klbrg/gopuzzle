package puzzle

import (
	"fmt"
	"io/fs"
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

// DisplayLanguage humanises a language ID for the UI / AI prompts
// ("go" -> "Go").
func DisplayLanguage(lang string) string {
	switch lang {
	case "go":
		return "Go"
	case "python":
		return "Python"
	case "":
		return "(unknown language)"
	default:
		return strings.ToUpper(lang[:1]) + lang[1:]
	}
}

// DisplaySource humanises a source directory ID for the UI / AI prompts.
// Falls back to the raw ID for unknown sources.
func DisplaySource(source string) string {
	switch source {
	case "learning_go":
		return "Learning Go, 2nd Edition"
	case "effective_python":
		return "Effective Python: 125 Specific Ways to Write Better Python, 3rd Edition"
	case "100_go_mistakes":
		return "100 Go Mistakes and How to Avoid Them"
	case "gobyexample":
		return "Go by Example"
	}
	return source
}

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

// LoadAll walks the filesystem directory at Dir for puzzle YAMLs.
// Preserved for tests and for backward compatibility; main.go uses
// LoadAllFS so it can read from an embedded fs.FS or from a runtime
// override directory.
func LoadAll() ([]*Puzzle, error) {
	return LoadAllFS(os.DirFS(Dir))
}

// LoadAllFS walks any fs.FS rooted at the puzzles tree (puzzles
// directly under root, no "puzzles/" prefix). Works for embed.FS via
// fs.Sub and for filesystem paths via os.DirFS.
func LoadAllFS(root fs.FS) ([]*Puzzle, error) {
	var puzzles []*Puzzle

	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".yaml" {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		var p Puzzle
		if err := yaml.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		// fs.FS paths use forward slashes; convert to native for Dir.
		p.Dir = filepath.Dir(filepath.FromSlash(path))
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
