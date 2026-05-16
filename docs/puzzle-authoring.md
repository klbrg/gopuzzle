# Puzzle authoring rules

Distilled from authoring ~40 puzzles across Ch02-Ch04 of "Learning Go".
Each rule below came from a real failure mode that hit the curriculum.

These rules apply to *any* source language — Go today, Python next.

## 1. Match the kind to the concept

| Concept type | Use kind |
|---|---|
| "Recall this fact / spelling" | `quiz` |
| "What does this code print?" / surprising behavior | `predict_output` |
| "This is broken; fix it" / common anti-pattern | `fix` |
| "Implement this with varied inputs" / logic | `code` |

Avoid forcing every concept into `code`. The whole pedagogical
contribution of this tool is using the right shape per concept.

## 2. The TODO describes the contract, not the algorithm

❌ `// TODO: subtract 'a' from r and add 1`
✅ `// TODO: return r's 1-based position in the alphabet`

If the student wants the technique, they press `h` for the hint. The
hint can be specific:

> "Try `r - 'a' + 1`. Rune subtraction works because rune is int32."

## 3. Don't spoil values in the description

❌

```
description: |
  Return the zero value for each type:
    bool   -> false
    int    -> 0
    string -> ""
```

✅

```
description: |
  Each builtin type has a "zero value" — the value you get when you
  don't initialize. Implement allZero(b, i, s) that returns true
  exactly when each argument equals its type's default.
```

The student should have to recall what the values *are*.

## 4. Don't require trivia memorization for predict-output

If a predict-output puzzle's expected output requires the student to
recall an ASCII code, a specific Unicode mapping, or a magic constant
they wouldn't otherwise know:

- Reframe to test a *type*, *behavior*, or *relationship* instead.
- Or put the reference fact in the description as a clearly-labelled
  reference.

Real example: an early Ch03 puzzle asked the student to predict that
`fmt.Println(s[0])` for `s := "Hi"` prints `72`. That required knowing
that 'H' is ASCII 72. The reframe: snippet uses `fmt.Printf("%T\n", s[0])`
and predicts `uint8` — same lesson (indexing a string gives a byte,
not a character), no trivia.

## 5. Predict-output FAIL must not spoil the answer

When the user mis-predicts, the result screen shows ONLY what they
typed, plus "press `s` to reveal expected, or enter to try again." Do
NOT print "Expected: X / Got: Y" — that hands them the answer.

The user can still press `s` explicitly to reveal the expected output
when they want to learn from the diff. The reveal is opt-in.

## 6. Fix puzzles must have a real bug

The template must either not compile or produce wrong output. If it
produces the right output as-is, the puzzle is broken — the student
would "fix" nothing and still pass.

The `solution` field is the canonical fixed version. The test harness
runs `solution` and compares its stdout to `expected_output`.

## 7. Predict-output snippets are single-line by default

The TUI's answer field accepts one line. If you need multi-line
output, the user has to press ctrl+j for newlines. Most snippets
should print on one line where possible (e.g. `fmt.Println(a, b, c)`
instead of three separate `Println` calls), to keep the interaction
tight.

When you DO need multi-line output, the puzzle's format hint
auto-detects it and tells the user how to enter newlines.

## 8. AI review prompt credits good engineering

If the student's code is genuinely better than the canonical — more
robust, handles edge cases the puzzle doesn't test — the AI is
prompted to acknowledge that explicitly. Same goes for defensive code
(validation, guards): frame as production-quality instinct, not
redundancy.

This is the AI's system prompt, not per-puzzle work. See
`docs/architecture.md` for the exact text.

## 9. Stay real-world

When picking what to make a puzzle out of, prefer concepts the student
will encounter daily over chapter trivia.

Examples of what got cut from the Learning Go curriculum even though
the book covers them:

- Complex numbers (the book itself says you probably won't use them)
- Float scientific notation format trivia
- Sized-int overflow specifics
- Typed-vs-untyped const edge cases
- The Universe Block (shadowing `true`, `false`, etc.)
- `goto`

Removing the bottom 30% of candidate puzzles for each chapter
noticeably tightens the curriculum. Be willing to skip.

## 10. `make test-puzzles` is non-negotiable

Every puzzle's canonical content runs through the harness:
- `code`: solution + test_code via the language's test runner, must pass
- `predict_output`: snippet runs, stdout matches expected_output
- `quiz`: answer is in choices
- `fix`: solution runs, stdout matches expected_output

This catches authoring drift instantly. Don't ship a puzzle without
the harness green.

## 11. Reference URLs should be specific

Don't just link the chapter. Link the section anchor (the publisher's
page usually has stable `#anchor-id` URLs). When the student presses
`o`, they should land at the section that actually covers the
concept, not the chapter intro.

For O'Reilly chapters this is the `#id...` or human-named anchor
on the chapter page. You can extract them by inspecting the rendered
chapter DOM: `document.querySelectorAll('[id]')` on the chapter page
gives you everything.
