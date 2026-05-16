# gopuzzle

A terminal puzzle app for practicing Go, organized by chapter. Each puzzle
ships as a single YAML file under `puzzles/<source>/<section>/` (currently
`learning_go/ch02/`, `learning_go/ch03/`, `learning_go/ch04/`).

The pedagogical model is language-agnostic. A plan to generalise the
runner so Python (and beyond) can become a second source language lives
in `docs/multi-language-plan.md`. See also `docs/architecture.md`,
`docs/puzzle-format.md`, and `docs/puzzle-authoring.md` for the
extracted design notes.

## Layout

```
main.go                      entry point + `version` subcommand
Makefile                     build/install/test/check targets
internal/ai/                 Anthropic Messages API client (code review)
internal/progress/           ~/.gopuzzle/progress.json read/write + prune/toggle/reset
internal/puzzle/             YAML loader, Puzzle struct, TestAllSolutionsPass harness
internal/runner/             go test runner (Run) and go run runner (RunSnippet)
internal/tui/                bubbletea TUI (browse, puzzle-info, predict input, result)
puzzles/                     puzzle YAMLs grouped by source/section
```

## Puzzle kinds

A `kind:` field on each YAML selects how the puzzle is presented and verified.
The harness in `internal/puzzle/all_test.go` validates every puzzle's
canonical content for the matching kind.

| Kind | What the user does | YAML fields | Verified by |
|---|---|---|---|
| `code` (default) | Edit scratch file in `$EDITOR`, run tests | `template`, `test_code`, `solution` | `runner.Run` → `go test -v` |
| `predict_output` | Type one-line prediction of stdout | `snippet`, `expected_output` | `runner.RunSnippet` on `snippet` must produce `expected_output` |
| `quiz` | Press a/b/c/d to pick a choice | `question`, `choices`, `answer` | `answer` must be one of `choices` |
| `fix` | Edit broken scratch in `$EDITOR`, run main | `template` (broken), `expected_output`, `solution` (fixed) | `runner.RunSnippet` on `solution` must produce `expected_output` |

## Authoring rules

1. **TODOs in templates describe what to return, not how.** "TODO: return r's
   1-based position in the alphabet" — not "TODO: subtract 'a' from r". The
   `hint` field is where the technique goes (opt-in via `h` keypress).
2. **Don't spoil values in the description.** For zero-value puzzles, don't
   list `false, 0, "", nil` in example calls — the student should have to
   recall them. Use behavior descriptions, not literal values.
3. **One concept per puzzle.** If the puzzle requires multiple Ch02 concepts
   plus boilerplate from later chapters (function signatures, return types),
   pre-fill that scaffolding in `template` so the student only edits the body.
4. **Predict-output answers are single-line.** Snippets should `fmt.Print` or
   `fmt.Println` everything on one line. The TUI input field doesn't accept
   newlines.
5. **Fix puzzles need a real bug.** The `template` must not compile or must
   produce wrong output. `solution` is the canonical fixed version. The user
   edits the scratch toward `solution` and we verify by running and matching
   `expected_output`.
6. **Run `make test-puzzles` after authoring.** It catches: code solutions
   that fail their tests, predict snippets whose stdout doesn't match the
   recorded `expected_output`, quiz answers that aren't in the choices list,
   and fix solutions that don't produce the expected output.

## TUI keybindings

**Browse:** `↑↓` move · `enter` open · `tab` collapse source · `/` search ·
`u` toggle solved on highlighted puzzle · `R` reset all progress · `?` help ·
`q` quit

**Puzzle info (code/fix):** `enter` open editor · `h` hint · `s` suggested
solution · `o` open reference URL · `r` reset scratch to template ·
`D` delete scratch file · `b` back

**Puzzle info (predict_output):** `enter` type prediction · `h` hint ·
`s` expected output · `o` ref · `b` back

**Puzzle info (quiz):** `a/b/c/d` (or `1-4`) pick · `h` hint · `s` answer ·
`o` ref · `b` back

**Result PASS:** `enter` next puzzle · `e` re-open editor (code/fix only) ·
`a` AI review (code/fix only, needs `ANTHROPIC_API_KEY`) · `b` browser · `q`

**Result FAIL:** `enter` retry · `h` hint · `s` solution · `o` ref ·
`r` reset scratch · `D` delete scratch · `b` back

## AI review

Pressing `a` on a passing code or fix puzzle calls Anthropic's Messages API
for a short review of the user's solution.

- Model: `claude-sonnet-4-6` (hard-coded in `internal/ai/ai.go`)
- API key: `ANTHROPIC_API_KEY` env var; if missing, the result view shows the
  error inline
- System prompt has `cache_control: ephemeral` so back-to-back reviews in the
  same 5-min window hit the prompt cache
- `max_tokens: 400`, 30-second HTTP timeout
- Typical cost: ~half a cent per review

## Scratch lifecycle

Scratch files live at `~/.gopuzzle/scratch/<id>/main.go`, with a tiny
per-puzzle `go.mod` so each one is its own standalone module — gopls
treats them independently and doesn't surface cross-puzzle "main
redeclared" diagnostics. Code-kind puzzles use `package scratch` (no
`func main()` needed); fix-kind puzzles use `package main`. Behavior:

- `ensureScratch` writes the template only when the file is missing OR when
  the on-disk content no longer contains every `func` signature declared in
  the puzzle's template (drift detection — catches puzzles renamed/reshaped
  since the scratch was last opened). Otherwise prior edits are preserved.
- `r` rewrites the template in place; `D` deletes the whole subdirectory.
- `make clean-scratch` wipes `~/.gopuzzle/scratch/`.

## Progress lifecycle

Stored as JSON at `~/.gopuzzle/progress.json`. On TUI startup, any solved
entry whose ID is not in the currently-loaded puzzle set is pruned and
`TotalSolved` is rebuilt. The score header in the browse view always derives
from currently-loaded puzzles, not the raw JSON counter, so deleted puzzles
can't inflate it. `u` toggles a single puzzle's solved state; `R` resets
everything.

## Common commands

```
make                    show all targets
make install            install to $GOPATH/bin
make run                install and launch the TUI
make version            print build info to verify the installed binary
make test               run all tests
make test-puzzles       run only the puzzle-verification suite
make check              fmt + vet + test
make clean-scratch      wipe ~/.gopuzzle/scratch
gopuzzle version        show commit/build/go version of the installed binary
```
