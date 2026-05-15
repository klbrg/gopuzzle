package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klbrg/gopuzzle/internal/progress"
	"github.com/klbrg/gopuzzle/internal/puzzle"
	"github.com/klbrg/gopuzzle/internal/tui"
)

func main() {
	var candidates []string
	if env := os.Getenv("GOPUZZLE_DIR"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "puzzles")
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "gopuzzle", "puzzles"))
	}
	var puzzleDir string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			puzzleDir = c
			break
		}
	}
	if puzzleDir == "" {
		fmt.Fprintln(os.Stderr, "puzzles/ directory not found (set GOPUZZLE_DIR, run from a directory with puzzles/, or install to ~/.config/gopuzzle/puzzles)")
		os.Exit(1)
	}
	puzzle.Dir = puzzleDir

	puzzles, err := puzzle.LoadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading puzzles: %v\n", err)
		os.Exit(1)
	}
	if len(puzzles) == 0 {
		fmt.Fprintln(os.Stderr, "No puzzles found.")
		os.Exit(1)
	}

	prog, err := progress.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load progress: %v\n", err)
		prog, _ = progress.Load()
	}

	model := tui.New(prog, puzzles)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running app: %v\n", err)
		os.Exit(1)
	}
}
