package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klbrg/gopuzzle/internal/progress"
	"github.com/klbrg/gopuzzle/internal/puzzle"
	"github.com/klbrg/gopuzzle/internal/tui"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			printVersion()
			return
		}
	}

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

func printVersion() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		fmt.Println("gopuzzle: build info unavailable")
		return
	}
	rev, commitTime, dirty := "unknown", "unknown", ""
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
			if len(rev) > 12 {
				rev = rev[:12]
			}
		case "vcs.time":
			commitTime = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = " (dirty)"
			}
		}
	}
	buildTime := "unknown"
	if exe, err := os.Executable(); err == nil {
		if st, err := os.Stat(exe); err == nil {
			buildTime = st.ModTime().UTC().Format(time.RFC3339)
		}
	}
	fmt.Printf("gopuzzle %s\n", info.Main.Version)
	fmt.Printf("  commit:    %s%s\n", rev, dirty)
	fmt.Printf("  committed: %s\n", commitTime)
	fmt.Printf("  built:     %s\n", buildTime)
	fmt.Printf("  go:        %s\n", info.GoVersion)
}
