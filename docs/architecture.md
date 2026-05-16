# Architecture

Current state: a Go-only puzzle TUI. Target state: a puzzle TUI with
a pluggable runner so Python (and eventually other languages) can be
added without forking. The pluggability work is planned in
`docs/multi-language-plan.md`.

## Current package layout

```
main.go                      CLI entry (`gopuzzle`, `gopuzzle version`)
internal/puzzle/             YAML loader, Puzzle struct, TestAllSolutionsPass
internal/runner/             Go-specific: `Run` (go test) and `RunSnippet` (go run)
internal/progress/           ~/.gopuzzle/progress.json read/write
internal/ai/                 Anthropic Messages API client
internal/tui/                bubbletea TUI: browse, puzzle-info, predict input, result
puzzles/<source>/<section>/  YAML puzzles
```

## Module responsibilities

### `internal/puzzle/`

Source of truth for the puzzle data model.

```go
type Kind = string
const (
    KindCode          = "code"
    KindPredictOutput = "predict_output"
    KindQuiz          = "quiz"
    KindFix           = "fix"
)

type Puzzle struct {
    ID, Title, Concept, Kind, Description, Hint, Explanation, Reference string

    // code/fix
    Template, TestCode, Solution string

    // predict_output / fix share these
    Snippet, ExpectedOutput string

    // quiz
    Question string
    Choices  []string
    Answer   string

    // Derived from path
    Source, Section, Dir, Stem string
}
```

`LoadAll(dir)` walks `puzzles/<source>/<section>/*.yaml` and parses each
into a Puzzle.

`all_test.go` is the `TestAllSolutionsPass` harness: for every puzzle it
runs the canonical content per kind and asserts it's correct. This is
what `make test-puzzles` invokes.

### `internal/runner/`

Today: Go-specific.

```go
func Run(solution, testCode string) (*Result, error)
// Writes solution.go + solution_test.go in a tempdir, runs `go test -v`.

func RunSnippet(code string) (string, error)
// Writes main.go in a tempdir, runs `go run .`. Returns stdout.
```

Planned (see `docs/multi-language-plan.md`): introduce a `Runner`
interface so non-Go languages get their own implementation. The TUI
and the harness pick a runner per puzzle based on a `lang:` field
or the source/ path.

### `internal/progress/`

```go
type Progress struct {
    TotalSolved int
    Solved      map[string]bool
}

func Load() (*Progress, error)
func (p *Progress) Save() error
func (p *Progress) RecordAttempt(id string, solved bool)
func (p *Progress) Reset()
func (p *Progress) ToggleSolved(id string) bool
func (p *Progress) PruneOrphans(validIDs map[string]bool) int
```

JSON file at `~/.gopuzzle/progress.json`. On TUI startup, any solved
entry whose ID is no longer in the loaded puzzle set is pruned.

### `internal/ai/`

Direct HTTP to `https://api.anthropic.com/v1/messages` via `net/http`.
No SDK dependency.

- Model: `claude-sonnet-4-6`
- `max_tokens: 400`, 30s timeout
- `cache_control: {"type": "ephemeral"}` on the system block

Two distinct prompts:

**review (called on PASS):**

> You are a senior Go developer giving a short, friendly review of a beginner's solution to a puzzle from "Learning Go, 2nd Edition".
>
> Rules:
> - Keep your response to 2-4 sentences total. Be concise.
> - Focus on whether the code is idiomatic Go and what (if anything) you'd change.
> - If the student's solution is genuinely BETTER than the canonical — more robust, clearer naming, or handling edge cases the canonical doesn't — say so explicitly. Give credit for good engineering judgment, not just for matching the canonical. The puzzle author may have stopped at the minimum that passes the tests.
> - If the student added validation, guards, or error handling that the puzzle's tests don't exercise, frame it as good production-quality instinct, NOT as redundancy or over-engineering. Briefly note which inputs the extra logic would protect against.
> - If the solution matches the canonical and is already clean, say so briefly and stop.
> - Be encouraging but honest. Plain prose only — no headings, no lists, no markdown formatting.
> - The student is learning the basics. Don't reach for advanced patterns they haven't met yet.

**hint (called on FAIL):**

> You are a senior Go developer giving a short, targeted hint to a beginner whose code FAILED its tests in a puzzle from "Learning Go, 2nd Edition".
>
> Rules:
> - Keep your response to 2-4 sentences total. Be concise.
> - Identify the specific mistake in the student's reasoning or code, based on the failing tests.
> - Give a HINT, not the answer. Point at the rule or concept they're missing; suggest what to try.
> - DO NOT paste a corrected version of their code or the canonical solution. They are learning by struggling productively — don't deprive them of that.
> - Be encouraging. Plain prose only — no headings, no lists, no markdown formatting.
> - The student is learning the basics. Don't reach for advanced patterns they haven't met yet.

For non-Go languages, the prompts should be parameterised on the
language name and source book.

### `internal/tui/`

bubbletea + lipgloss. States:

- `stateBrowse` — list of puzzles grouped by source/section
- `statePuzzleInfo` — info screen, variant per kind
- `statePredictInput` — text input for predict_output
- `stateRunning` — spinner while runner is executing
- `stateResult` — PASS/FAIL screen with kind-specific extras
- `stateDone` — all puzzles solved

`syntax.go` has all the rendering helpers:
- `renderCodeBlock(text, isGo)` — bordered box with optional Chroma highlighting
- `renderInline(text, baseStyle)` — backtick → bold (per-line, with Inline(true) to avoid block padding)
- `kindBadge(kind)` — colored `[code]`/`[predict]`/`[quiz]`/`[fix]` tag
- `stripLeadingComments(s)` — used to suppress "Your solution" when body matches canonical
- `cleanTestOutput(raw)` — strips `=== RUN` / `--- FAIL` / framework chatter
- `predictFormatHint(expected)` — infers "Format: a Go slice like [v1 v2 v3]" or similar

## Per-puzzle scratch isolation

This is critical: each scratch lives in its own subdirectory.

```
~/.gopuzzle/scratch/
├── lg-ch02-005/
│   ├── go.mod        (module scratch, go 1.23)
│   └── main.go       ← what the editor opens
├── lg-ch02-006/
│   ├── go.mod
│   └── main.go
└── ...
```

Without this, gopls sees all the scratches at once and reports
"main redeclared in this block" or "function main is undeclared".
The per-subdir + go.mod approach gives each scratch its own module
scope.

For Python the equivalent will be `~/.gopuzzle/scratch/<id>/main.py`
plus an empty `__init__.py` so the directory is its own module scope
for pyright/ruff.

## Scratch drift detection

When `ensureScratch` finds an existing file, compare it against the
current puzzle's template. If the template's declared `func` (or
`def`) signatures don't all appear in the on-disk file, the scratch is
stale (puzzle was renamed or reshaped since last opened) — overwrite
and tell the user via a flash message.

## CLI

```
gopuzzle                  launch TUI
gopuzzle version          print version + build/commit time
```

No other subcommands. The TUI is the product.

## Test harness

`internal/puzzle/all_test.go::TestAllSolutionsPass` runs every puzzle
through the right verification path:

| Kind | Verification |
|---|---|
| `code` | `runner.Run(solution, test_code)` — go test -v, must pass |
| `predict_output` | `runner.RunSnippet(snippet)` — stdout must match `expected_output` |
| `quiz` | `answer` must be in `choices` |
| `fix` | `runner.RunSnippet(solution)` — stdout must match `expected_output` |

`make test-puzzles` invokes this. It catches authoring bugs the moment
they ship.
