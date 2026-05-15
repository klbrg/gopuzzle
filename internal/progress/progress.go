package progress

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Progress struct {
	TotalSolved int             `json:"totalSolved"`
	Solved      map[string]bool `json:"solved"`
}

func defaultProgress() *Progress {
	return &Progress{
		Solved: make(map[string]bool),
	}
}

func gopuzzleDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gopuzzle"), nil
}

func filePath() (string, error) {
	dir, err := gopuzzleDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "progress.json"), nil
}

// SaveSolution writes the passing solution to ~/.gopuzzle/solutions/<dir>/<stem>.go
// and commits it to a git repo in the solutions directory.
func SaveSolution(puzzleID, title, puzzleDir, stem, code string) error {
	base, err := gopuzzleDir()
	if err != nil {
		return err
	}
	solutionsDir := filepath.Join(base, "solutions")

	// Init git repo if it doesn't exist yet.
	gitDir := filepath.Join(solutionsDir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		if err := os.MkdirAll(solutionsDir, 0755); err != nil {
			return err
		}
		if err := git(solutionsDir, "init"); err != nil {
			return err
		}
	}

	subDir := filepath.Join(solutionsDir, puzzleDir)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return err
	}

	relPath := filepath.Join(puzzleDir, stem+".go")
	filePath := filepath.Join(solutionsDir, relPath)

	// Check if this is an update to an existing solution.
	_, existsErr := os.Stat(filePath)
	isUpdate := existsErr == nil

	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return err
	}

	if err := git(solutionsDir, "add", relPath); err != nil {
		return err
	}

	prefix := "feat(solve)"
	if isUpdate {
		prefix = "refactor(solve)"
	}
	msg := fmt.Sprintf("%s: %s — %s", prefix, puzzleID, title)
	return git(solutionsDir, "commit", "-m", msg)
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func Load() (*Progress, error) {
	path, err := filePath()
	if err != nil {
		return defaultProgress(), nil
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaultProgress(), nil
	}
	if err != nil {
		return defaultProgress(), nil
	}

	var p Progress
	if err := json.Unmarshal(data, &p); err != nil {
		return defaultProgress(), nil
	}
	if p.Solved == nil {
		p.Solved = make(map[string]bool)
	}
	return &p, nil
}

func (p *Progress) Save() error {
	path, err := filePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (p *Progress) RecordAttempt(puzzleID string, solved bool) {
	if solved && !p.Solved[puzzleID] {
		p.TotalSolved++
		p.Solved[puzzleID] = true
	}
}

// Reset wipes all solved state. TotalSolved goes back to 0.
func (p *Progress) Reset() {
	p.Solved = make(map[string]bool)
	p.TotalSolved = 0
}

// ToggleSolved flips a puzzle's solved status and returns the new state.
func (p *Progress) ToggleSolved(id string) bool {
	if p.Solved[id] {
		delete(p.Solved, id)
		p.TotalSolved--
		return false
	}
	p.Solved[id] = true
	p.TotalSolved++
	return true
}

// PruneOrphans removes solved entries whose IDs are not in validIDs and
// rebuilds TotalSolved from the surviving entries. Returns the number of
// entries removed.
func (p *Progress) PruneOrphans(validIDs map[string]bool) int {
	removed := 0
	for id := range p.Solved {
		if !validIDs[id] {
			delete(p.Solved, id)
			removed++
		}
	}
	p.TotalSolved = len(p.Solved)
	return removed
}
