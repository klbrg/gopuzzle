package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
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

	// Default: read puzzles from the binary's embedded fs.FS so
	// `gopuzzle` works identically from any cwd. GOPUZZLE_DIR
	// overrides for puzzle authoring (no rebuild needed).
	var puzzleFS fs.FS
	if override := os.Getenv("GOPUZZLE_DIR"); override != "" {
		if _, err := os.Stat(override); err != nil {
			fmt.Fprintf(os.Stderr, "GOPUZZLE_DIR=%q not found: %v\n", override, err)
			os.Exit(1)
		}
		puzzleFS = os.DirFS(override)
		puzzle.Dir = override
	} else {
		sub, err := fs.Sub(embeddedPuzzles, "puzzles")
		if err != nil {
			fmt.Fprintf(os.Stderr, "embedded puzzles unreachable: %v\n", err)
			os.Exit(1)
		}
		puzzleFS = sub
	}

	puzzles, err := puzzle.LoadAllFS(puzzleFS)
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

	// Skip orphan pruning when running with a runtime override —
	// the override set might be a deliberate subset, and we don't
	// want to wipe canonical progress.
	pruneOrphans := os.Getenv("GOPUZZLE_DIR") == ""
	model := tui.New(prog, puzzles, pruneOrphans)
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
	fmt.Println("  runners:")
	for _, r := range probeRunners() {
		mark := "✓"
		if !r.available {
			mark = "✗"
		}
		fmt.Printf("    %s  %-7s  %s\n", mark, r.lang, r.detail)
	}
}

type runnerProbe struct {
	lang      string
	available bool
	detail    string
}

// probeRunners checks each supported source language's runtime by
// running its version-printing subcommand. Useful so you can see at a
// glance whether a Python puzzle will actually run on this machine.
func probeRunners() []runnerProbe {
	return []runnerProbe{
		probeCmd("go", "go run / go test", "go", "version"),
		probeCmd("python", "python -m unittest / python main.py", "python3", "--version"),
	}
}

func probeCmd(lang, description, bin string, args ...string) runnerProbe {
	if _, err := exec.LookPath(bin); err != nil {
		// Try the alias "python" as a fallback for Python.
		if lang == "python" {
			if _, err := exec.LookPath("python"); err == nil {
				bin = "python"
			} else {
				return runnerProbe{lang: lang, detail: description + " (not installed)"}
			}
		} else {
			return runnerProbe{lang: lang, detail: description + " (not installed)"}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	version := strings.TrimSpace(string(out))
	if err != nil || version == "" {
		return runnerProbe{lang: lang, available: true, detail: description + " (" + bin + " present)"}
	}
	return runnerProbe{lang: lang, available: true, detail: version + "  —  " + description}
}
