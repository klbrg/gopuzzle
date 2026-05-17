package main

import (
	"io/fs"
	"testing"

	"github.com/klbrg/gopuzzle/internal/puzzle"
)

// TestEmbedLoads confirms the embedded puzzles fs.FS resolves and
// LoadAllFS returns the same set you'd get from the source tree.
// Guards against the embed directive silently picking up an empty
// tree (e.g. wrong path glob), which would cause PruneOrphans to
// wipe progress on first run.
func TestEmbedLoads(t *testing.T) {
	sub, err := fs.Sub(embeddedPuzzles, "puzzles")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	puzzles, err := puzzle.LoadAllFS(sub)
	if err != nil {
		t.Fatalf("LoadAllFS: %v", err)
	}
	if len(puzzles) == 0 {
		t.Fatal("embedded puzzles loaded ZERO entries — embed directive is broken; this would wipe user progress via PruneOrphans on first run")
	}
	if len(puzzles) < 40 {
		t.Errorf("embedded puzzles loaded only %d entries; expected 50+. PruneOrphans would aggressively trim progress.", len(puzzles))
	}
}
