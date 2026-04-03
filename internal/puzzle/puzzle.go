package puzzle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Puzzle struct {
	ID          string `yaml:"id"`
	Title       string `yaml:"title"`
	Concept     string `yaml:"concept"`
	Difficulty  int    `yaml:"difficulty"`
	Description string `yaml:"description"`
	Template    string `yaml:"template"`
	TestCode    string `yaml:"test_code"`
	Hint        string `yaml:"hint"`
	Solution    string `yaml:"solution"`
	Explanation string `yaml:"explanation"`
	Reference   string   `yaml:"reference"`

	// Set at load time from the file path.
	Source  string `yaml:"-"` // e.g. "gobyexample", "learning_go"
	Section string `yaml:"-"` // e.g. "01_basics", "ch03"
	Dir     string `yaml:"-"` // full relative dir: "gobyexample/01_basics"
	Stem    string `yaml:"-"`
}

// Dir is the path to the puzzles directory, set by main.
var Dir string

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
		puzzles = append(puzzles, &p)
		return nil
	})
	return puzzles, err
}
