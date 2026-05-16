# Plan: add Python (and beyond) as a second source language

## Context

Today gopuzzle is Go-only: the TUI, loader, harness, AI, and progress
machinery are all language-agnostic, but `internal/runner/` and a few
hard-coded strings ("Go developer" in the AI prompts, `.go` scratch
extension, etc.) assume Go.

The pedagogical model (4-kind taxonomy, real-world skill focus, AI
review/hint, drift detection) is general — there's nothing
language-specific about it. So rather than re-implementing the whole
TUI in Python for pypuzzle, we generalise gopuzzle to support multiple
source languages.

## Target end state

```
gopuzzle <id>            launch TUI, optionally jump to a puzzle by id
puzzles/
  learning_go/           Go puzzles (current)
  effective_python/      Python puzzles (new)
  effective_typescript/  hypothetical future
```

Each puzzle has an implicit (path-derived) or explicit (`lang:` field)
language. The loader picks the right `Runner` for each puzzle. The TUI
doesn't know or care about the language — it just renders Puzzles and
hands their code+tests to a Runner.

## Phased plan

Each phase is independently shippable.

### Phase 1 — Runner abstraction

Goal: make Go a concrete `Runner` implementation, no behavior change yet.

- [ ] Define an interface in `internal/runner/`:
  ```go
  type Runner interface {
      Run(solution, testCode string) (*Result, error)
      RunSnippet(code string) (string, error)
  }
  ```
- [ ] Move current `Run` / `RunSnippet` into a `GoRunner` struct
      implementing this interface.
- [ ] Add `Language` type, `Lang` field on `Puzzle`, `LangGo`/`LangPython`
      constants. Default `LangGo` when missing. Loader can derive from
      `source/` path prefix as a fallback.
- [ ] `runner.For(lang)` returns the right Runner implementation.
- [ ] Update callers (the test harness, the viExitMsg handler in TUI)
      to fetch the right runner per puzzle.

Test: existing puzzles continue to pass `make test-puzzles`.

### Phase 2 — Python runner

Goal: Python puzzles can be authored and verified.

- [ ] `PythonRunner` in `internal/runner/python.go`:
  - `RunSnippet`: writes `main.py` to tempdir, runs `python3 main.py`,
    captures stdout. Timeout 10s.
  - `Run`: writes `solution.py` + `test_solution.py`, runs
    `python3 -m pytest -q`. Parse pass/fail from exit code + stdout.
- [ ] Per-puzzle scratch dir for Python:
  `~/.gopuzzle/scratch/<id>/main.py` + empty `__init__.py` to give it
  module scope (analogous to the Go `go.mod` trick).
- [ ] Move the 4 reference puzzles from `docs/examples/` into
  `puzzles/effective_python/ch01/` and let `make test-puzzles` exercise
  them.
- [ ] Document `python3` and `pytest` as prerequisites (or use uv to
  manage them in a hermetic venv).

Test: `make test-puzzles` passes Go + Python puzzles together.

### Phase 3 — TUI tweaks

Goal: the TUI handles Python puzzles indistinguishably from Go.

- [ ] Syntax highlighting `renderCodeBlock` takes a language hint
      (`go`, `python`, ...) instead of `isGo bool`.
- [ ] FAIL output cleanup (`cleanTestOutput`) gets a Python variant
      that parses pytest output (strip `============`, `collected N
      items`, `PASSED`/`FAILED` summary, leave just the
      `assert ...` line + traceback).
- [ ] Format hint (`predictFormatHint`) — already language-agnostic;
      just check it's not Go-specific in any wording.
- [ ] Scratch path uses `.py` extension when puzzle is Python; drift
      detection looks for `def`/`class` signatures.

Test: a Python predict_output puzzle renders with Python syntax
highlighting; a Python FAIL surfaces the relevant assertion.

### Phase 4 — AI prompt parameterisation

Goal: AI prompts mention the right language and source.

- [ ] Parameterise prompts on the language name and source title.
  ```go
  type ReviewRequest struct {
      Language   string  // "Go" / "Python"
      Source     string  // "Learning Go, 2nd Edition"
      Title, Description, Canonical, UserCode string
  }
  ```
- [ ] Hint and review prompts read "a senior {Language} developer
      ... puzzle from {Source}".
- [ ] Map `(source, language)` from the puzzle's metadata.

Test: a Python puzzle gets a Python-flavored AI review.

### Phase 5 — Polish

- [ ] Browse view groups by source and shows language tags
      (`[go]`/`[python]`) alongside kind tags.
- [ ] `gopuzzle version` mentions supported runners.
- [ ] Optional: rename the binary / repo from `gopuzzle` to something
      language-neutral (`bookpuzzle`, `studypuzzle`). Defer until
      Python ships and we're sure the multi-language story sticks.

## Effective Python curriculum

Once Phases 1-3 land, the first Python chapter can begin. Plan:

- Use Effective Python (3rd ed) as the source. ~90 Items, each is
  roughly one section.
- Aim for ~10-14 puzzles per chapter, same shape as Learning Go.
- Strong kind mix expected: Python's gotchas (mutable default args,
  late binding, dict ordering, `is` vs `==`, scope rules) skew the
  mix toward `predict` and `fix` more than Go did.
- Start with Effective Python Ch01 (Pythonic Thinking) and proceed
  chapter by chapter.

The 4 reference puzzles in `docs/examples/` are starter material:
- `001_mutable_default.yaml` — predict_output
- `002_is_vs_eq.yaml` — quiz
- `003_late_binding_fix.yaml` — fix
- `004_parse_query.yaml` — code

Move them under `puzzles/effective_python/ch01/` once Phase 2 lands.

## Out of scope

- Multi-language runners beyond Python in the initial work. Add more
  (TypeScript, Rust, ...) only if there's a real curriculum need.
- A `gopuzzle config` subcommand to pick which sources to enable.
  The directory contents are sufficient.
- Cross-language puzzles (e.g. "what's the equivalent in language X?").
  Too clever; not the point.

## Cost estimate

Phase 1 (runner abstraction): ~half day  
Phase 2 (Python runner): ~half day  
Phase 3 (TUI tweaks): ~half day  
Phase 4 (AI parameterisation): ~hour  
Phase 5 (polish): ~hour  

Total: ~2 days to ship a working multi-language gopuzzle. Versus a
"reimplement in Python from scratch" path estimated at ~2 weeks for
parity with current gopuzzle features.
