package main

import "embed"

// Embed the entire puzzles/ tree into the binary so `gopuzzle` works
// from any directory and presents the same puzzle set everywhere.
// GOPUZZLE_DIR (set at runtime) overrides this with a filesystem
// path — useful for authoring new puzzles without recompiling.

//go:embed all:puzzles
var embeddedPuzzles embed.FS
