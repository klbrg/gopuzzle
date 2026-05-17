# Puzzle YAML format

Every puzzle is a single YAML file under
`puzzles/<source>/<section>/<NNN>_<slug>.yaml`.

The schema is language-agnostic. Code-bearing fields (`template`,
`test_code`, `snippet`, `solution`) contain source in whatever language
the puzzle's source belongs to. Today the runner only handles Go; the
multi-language plan in `docs/multi-language-plan.md` covers adding
Python and beyond.

## Required fields (all kinds)

| Field | Type | Description |
|---|---|---|
| `id` | string | Globally unique, e.g. `lg-ch02-005`. Used as the progress key. |
| `title` | string | Short human title shown in browse and puzzle info. |
| `concept` | string | High-level grouping (e.g. `loops`, `data model`). Not currently used for filtering, but reserve. |
| `kind` | enum | One of `code` (default), `predict_output`, `quiz`, `fix`. |
| `description` | string (block) | What the puzzle asks. Don't spoil values; don't reveal the answer in examples. |
| `hint` | string | Opt-in via `h`. Can be more direct than the description. |
| `explanation` | string (block) | Shown on PASS. Teach the concept; don't just restate the description. |
| `reference` | URL | Section anchor on the source publisher's site (e.g. O'Reilly). |

## Kind-specific fields

### `code` (default)

| Field | Description |
|---|---|
| `template` | Source written to the user's scratch. Should include the function signature; TODO body. |
| `test_code` | A test file (e.g. `_test.go` in Go, `test_main.py` in Python). Imports/uses the user's solution. |
| `solution` | Canonical source. Used by the test harness and shown on PASS / on `s`. |

### `predict_output`

| Field | Description |
|---|---|
| `snippet` | A self-contained program (e.g. `package main + func main()` in Go; a script in Python). |
| `expected_output` | The exact stdout the snippet produces. Newline-trimming is applied at compare time. |

### `quiz`

| Field | Description |
|---|---|
| `question` | The question text. |
| `choices` | List of 2-4 strings. |
| `answer` | Must be exactly equal to one of the entries in `choices`. |

### `fix`

| Field | Description |
|---|---|
| `template` | Broken source written to the user's scratch. Must not produce `expected_output` when run. |
| `expected_output` | What a correct fix should print to stdout. |
| `solution` | Canonical fixed version. Used by the test harness and shown on `s`. |

## Conventions

- Use YAML block scalars (`|`) for multi-line strings; no trailing
  whitespace.
- `id` is the same string across all references; use a stable scheme
  like `<source-abbrev>-ch<NN>-<NNN>` (e.g. `lg-ch02-005` for Learning
  Go ch02 puzzle 5; `ep-ch01-005` for Effective Python ch01 puzzle 5).
- Filenames sort alphabetically with `<NNN>_<slug>.yaml` (3-digit pad).
- Indent code blocks consistently with the language's convention
  (tabs in Go, 4 spaces in Python).

## Go code puzzle (example)

```yaml
id: "lg-ch04-006"
title: "Sum a Slice"
concept: "loops"
description: |
  Implement sum(nums) that returns the sum of all integers in the
  slice. Use a for-range loop to iterate.
template: |
  package puzzle

  func sum(nums []int) int {
  	// TODO: iterate with for-range and accumulate the total.
  }
test_code: |
  package puzzle

  import "testing"

  func TestSum(t *testing.T) {
  	if sum([]int{1, 2, 3}) != 6 {
  		t.Fatalf("want 6")
  	}
  }
solution: |
  package puzzle

  func sum(nums []int) int {
  	total := 0
  	for _, n := range nums {
  		total += n
  	}
  	return total
  }
hint: "..."
explanation: "..."
reference: "https://learning.oreilly.com/library/view/learning-go-2nd/9781098139285/ch04.html#for_range"
```

## Python predict_output puzzle (example)

See also `docs/examples/001_mutable_default.yaml`.

```yaml
id: "ep-examples-001"
title: "Mutable Default Arguments"
concept: "data_model"
kind: "predict_output"
description: |
  Python evaluates default arguments once, at function definition
  time. So a mutable default is shared across every call that doesn't
  pass a value. Predict the output.
snippet: |
  def append_one(items=[]):
      items.append(1)
      return items

  print(append_one())
  print(append_one())
  print(append_one())
expected_output: |
  [1]
  [1, 1]
  [1, 1, 1]
hint: "..."
explanation: "..."
reference: "https://learning.oreilly.com/library/view/effective-python-third/..."
```

## Quiz puzzle (language-agnostic)

```yaml
id: "ep-examples-002"
title: "is vs =="
concept: "data_model"
kind: "quiz"
question: "When should you use `is` rather than `==`?"
choices:
  - "Only when comparing against None, True, False, or other known singletons."
  - "Whenever the two operands are objects."
  - "When you want structural equality of nested data structures."
  - "Always — `is` is the safer default."
answer: "Only when comparing against None, True, False, or other known singletons."
hint: "..."
explanation: "..."
reference: "..."
```

## Fix puzzle (Python example)

See `docs/examples/003_late_binding_fix.yaml` for a worked closure
late-binding bug with a one-line fix.

## A note on language detection

For now, gopuzzle assumes all puzzles are Go. Once Python (or other)
support lands, the loader will detect the language from one of:

- An explicit `lang:` field on the YAML
- The `source/` path prefix (e.g. `learning_go/` → go, `effective_python/` → python)

Until that ships, Python puzzles should stay under `docs/examples/` as
reference samples — the current `make test-puzzles` would try to
compile them with `go test` and fail.
