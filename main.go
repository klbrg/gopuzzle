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
	// Look for puzzles/ next to the binary, then next to the working directory.
	exe, err := os.Executable()
	if err != nil {
		exe = "."
	}
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "puzzles"),
		"puzzles",
	}
	var puzzleDir string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			puzzleDir = c
			break
		}
	}
	if puzzleDir == "" {
		fmt.Fprintln(os.Stderr, "puzzles/ directory not found")
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
