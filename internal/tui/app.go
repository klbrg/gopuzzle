package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klbrg/gopuzzle/internal/ai"
	"github.com/klbrg/gopuzzle/internal/progress"
	"github.com/klbrg/gopuzzle/internal/puzzle"
	"github.com/klbrg/gopuzzle/internal/runner"
)

type state int

const (
	stateBrowse state = iota
	statePuzzleInfo
	statePredictInput
	stateRunning
	stateResult
	stateDone
)

type viExitMsg struct{ err error }

type runResultMsg struct {
	result   *runner.Result
	solution string
	err      error
}

type aiReviewMsg struct {
	review string
	err    error
}

// browseRow is one row in the browse list: a source header, section header, or puzzle entry.
type browseRow struct {
	isHeader    bool
	isSource    bool
	headerTxt   string
	sourceName  string // set on source headers
	sectionPath string // set on section headers: "source/section"
	puzzle      *puzzle.Puzzle
}

type Model struct {
	state         state
	spinner       spinner.Model
	progress      *progress.Progress
	puzzles       []*puzzle.Puzzle
	browseRows    []browseRow
	browseIdx     int
	browseOffset  int
	collapsed     map[string]bool
	current       *puzzle.Puzzle
	scratchDir    string
	result        *runner.Result
	userSolution  string
	hintShown     bool
	solutionShown bool
	width         int
	height        int

	searchMode   bool
	searchQuery  string
	preSearchIdx int

	showHelp bool
	flash    string

	// Predict-output state.
	answerInput string // what the user has typed so far
	answerOK    bool   // true after a passing predict-output attempt

	// AI review state.
	aiReview  string // cached review text for the current puzzle attempt
	aiErr     string // last error text, if review failed
	aiLoading bool   // request in flight

	totalPuzzles  int
	sectionTotals map[string]int
	sourceTotals  map[string]int
}

func New(p *progress.Progress, puzzles []*puzzle.Puzzle) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorBlue)

	scratchDir := defaultScratchDir()
	_ = os.MkdirAll(scratchDir, 0755)

	m := Model{
		state:         stateBrowse,
		spinner:       sp,
		progress:      p,
		puzzles:       puzzles,
		collapsed:     make(map[string]bool),
		scratchDir:    scratchDir,
		sectionTotals: make(map[string]int),
		sourceTotals:  make(map[string]int),
	}
	validIDs := make(map[string]bool, len(puzzles))
	for _, pz := range puzzles {
		validIDs[pz.ID] = true
		m.totalPuzzles++
		m.sectionTotals[pz.Source+"/"+pz.Section]++
		m.sourceTotals[pz.Source]++
	}
	if removed := p.PruneOrphans(validIDs); removed > 0 {
		_ = p.Save()
	}
	m.rebuildRows()
	for i, row := range m.browseRows {
		if row.puzzle != nil && !p.Solved[row.puzzle.ID] {
			m.browseIdx = i
			break
		}
	}
	return m
}

func defaultScratchDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gopuzzle-scratch")
	}
	return filepath.Join(home, ".gopuzzle", "scratch")
}

func (m *Model) rebuildRows() {
	filtered := make([]*puzzle.Puzzle, 0, len(m.puzzles))
	for _, pz := range m.puzzles {
		if m.searchMatches(pz) {
			filtered = append(filtered, pz)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Source != filtered[j].Source {
			return filtered[i].Source < filtered[j].Source
		}
		if filtered[i].Section != filtered[j].Section {
			return filtered[i].Section < filtered[j].Section
		}
		return filtered[i].ID < filtered[j].ID
	})

	var rows []browseRow
	lastSource, lastSection := "", ""
	for _, p := range filtered {
		if p.Source != lastSource {
			rows = append(rows, browseRow{isHeader: true, isSource: true, headerTxt: p.Source, sourceName: p.Source})
			lastSource = p.Source
			lastSection = ""
		}
		if p.Section != lastSection {
			rows = append(rows, browseRow{isHeader: true, headerTxt: p.Section, sectionPath: p.Source + "/" + p.Section})
			lastSection = p.Section
		}
		rows = append(rows, browseRow{puzzle: p})
	}
	m.browseRows = rows
	if m.browseIdx >= len(rows) {
		m.browseIdx = 0
	}
}

func (m Model) searchMatches(p *puzzle.Puzzle) bool {
	if m.searchQuery == "" {
		return true
	}
	q := strings.ToLower(m.searchQuery)
	hay := strings.ToLower(p.ID + " " + p.Title + " " + p.Section + " " + p.Source)
	return strings.Contains(hay, q)
}

func (m Model) Init() tea.Cmd {
	return nil
}

// scratchDirFor returns the per-puzzle directory holding scratch files.
// Each puzzle gets its own subdirectory so neighbouring scratches don't
// share a Go package (which would trigger "main redeclared" or "main not
// declared" diagnostics in editors via gopls).
func (m *Model) scratchDirFor() string {
	if m.current == nil {
		return filepath.Join(m.scratchDir, "_scratch")
	}
	return filepath.Join(m.scratchDir, m.current.ID)
}

func (m *Model) scratchPath() string {
	return filepath.Join(m.scratchDirFor(), "main.go")
}

func (m *Model) writeTemplate(includeHint bool) error {
	dir := m.scratchDirFor()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Standalone module so gopls treats this directory as self-contained
	// rather than as part of a larger package.
	gomod := filepath.Join(dir, "go.mod")
	if _, err := os.Stat(gomod); err != nil {
		if err := os.WriteFile(gomod, []byte("module scratch\n\ngo 1.23\n"), 0o644); err != nil {
			return err
		}
	}

	desc := strings.TrimSpace(m.current.Description)
	var b strings.Builder
	b.WriteString("// ")
	b.WriteString(m.current.Title)
	b.WriteString("\n")
	for _, line := range strings.Split(desc, "\n") {
		b.WriteString("// ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if m.current.Reference != "" {
		b.WriteString("// ref: ")
		b.WriteString(m.current.Reference)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(m.current.Template)
	if includeHint && m.current.Hint != "" {
		b.WriteString("\n// HINT: ")
		b.WriteString(m.current.Hint)
		b.WriteString("\n")
	}
	return os.WriteFile(m.scratchPath(), []byte(b.String()), 0o644)
}

// ensureScratch makes sure the scratch file for the current puzzle exists
// and is compatible with the current template. Returns (rewrote, err): a
// rewrite happens when the file is missing or its on-disk content no longer
// matches the puzzle's `func` signatures (e.g. the puzzle was edited since
// the scratch was last opened). Otherwise prior edits are preserved.
func (m *Model) ensureScratch(includeHint bool) (bool, error) {
	data, err := os.ReadFile(m.scratchPath())
	if err != nil {
		return true, m.writeTemplate(includeHint)
	}
	if m.scratchHasTemplateDrift(string(data)) {
		return true, m.writeTemplate(includeHint)
	}
	return false, nil
}

// scratchHasTemplateDrift returns true when the puzzle's template declares
// at least one `func ...` signature that no longer appears in the scratch.
// A signature mismatch means the puzzle's tests cannot bind to the scratch,
// so any prior body is unusable and rewriting is the right move.
func (m *Model) scratchHasTemplateDrift(scratch string) bool {
	for _, line := range strings.Split(m.current.Template, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "func ") {
			continue
		}
		if !strings.Contains(scratch, trimmed) {
			return true
		}
	}
	return false
}

func (m Model) openEditor() tea.Cmd {
	cmd := exec.Command(editorBin(), m.scratchPath())
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return viExitMsg{err: err}
	})
}

func editorBin() string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "nvim"
}

func openURL(url string) {
	switch runtime.GOOS {
	case "darwin":
		_ = exec.Command("open", url).Start()
	case "linux":
		_ = exec.Command("xdg-open", url).Start()
	case "windows":
		_ = exec.Command("cmd", "/c", "start", url).Start()
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case viExitMsg:
		if msg.err != nil {
			m.state = statePuzzleInfo
			return m, nil
		}
		// Any prior AI review is for the previous code — drop it so the
		// user can press `a` again after their next run.
		m.aiReview = ""
		m.aiErr = ""
		m.aiLoading = false
		m.state = stateRunning
		solution, readErr := os.ReadFile(m.scratchPath())
		cur := m.current
		return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
			if readErr != nil {
				return runResultMsg{err: readErr}
			}
			var res *runner.Result
			var err error
			switch cur.Kind {
			case puzzle.KindFix:
				out, runErr := runner.RunSnippet(string(solution))
				passed := runErr == nil && strings.TrimRight(out, " \t\n") == strings.TrimRight(cur.ExpectedOutput, " \t\n")
				output := out
				if !passed {
					output = "Expected:\n" + cur.ExpectedOutput + "\n\nGot:\n" + out
					if runErr != nil {
						output += "\n\n(snippet error: " + runErr.Error() + ")"
					}
				}
				res = &runner.Result{Passed: passed, Output: output}
			default:
				res, err = runner.Run(string(solution), cur.TestCode)
			}
			if err == nil && res != nil && res.Passed {
				_ = progress.SaveSolution(cur.ID, cur.Title, cur.Dir, cur.Stem, string(solution))
			}
			return runResultMsg{result: res, solution: string(solution), err: err}
		})

	case runResultMsg:
		if msg.err != nil {
			m.result = &runner.Result{Output: fmt.Sprintf("Error: %v", msg.err)}
		} else {
			m.result = msg.result
		}
		m.userSolution = msg.solution
		if m.result != nil && m.result.Passed {
			m.progress.RecordAttempt(m.current.ID, true)
			_ = m.progress.Save()
		}
		m.state = stateResult
		return m, nil

	case spinner.TickMsg:
		if m.state == stateRunning || m.aiLoading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case aiReviewMsg:
		m.aiLoading = false
		if msg.err != nil {
			m.aiErr = msg.err.Error()
			m.aiReview = ""
		} else {
			m.aiReview = msg.review
			m.aiErr = ""
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.flash = ""
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	if m.searchMode {
		return m.handleSearchKey(msg)
	}
	switch m.state {
	case stateBrowse:
		return m.handleBrowseKey(msg)
	case statePuzzleInfo:
		return m.handlePuzzleInfoKey(msg)
	case statePredictInput:
		return m.handlePredictInputKey(msg)
	case stateResult:
		return m.handleResultKey(msg)
	case stateDone:
		switch msg.String() {
		case "q", "ctrl+q", "ctrl+c":
			return m, tea.Quit
		case "b", "esc", "enter", " ":
			m.state = stateBrowse
		case "?":
			m.showHelp = true
		}
	}
	return m, nil
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch s {
	case "enter":
		m.searchMode = false
		m.placeOnFirstPuzzle()
		m.clampScroll()
	case "esc":
		m.searchMode = false
		m.searchQuery = ""
		m.rebuildRows()
		if m.preSearchIdx < len(m.browseRows) {
			m.browseIdx = m.preSearchIdx
		}
		m.clampScroll()
	case "backspace":
		if len(m.searchQuery) > 0 {
			m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
			m.rebuildRows()
			m.placeOnFirstPuzzle()
			m.clampScroll()
		}
	case "ctrl+c", "ctrl+q":
		return m, tea.Quit
	default:
		if len(msg.Runes) > 0 {
			m.searchQuery += string(msg.Runes)
			m.rebuildRows()
			m.placeOnFirstPuzzle()
			m.clampScroll()
		}
	}
	return m, nil
}

func (m *Model) placeOnFirstPuzzle() {
	for i, row := range m.browseRows {
		if row.puzzle != nil {
			m.browseIdx = i
			return
		}
	}
	if len(m.browseRows) > 0 {
		m.browseIdx = 0
	} else {
		m.browseIdx = 0
	}
}

func (m Model) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup", "ctrl+u":
		step := m.visibleRowCount() / 2
		if step < 1 {
			step = 1
		}
		m.moveCursor(-step)
	case "pgdown", "ctrl+d":
		step := m.visibleRowCount() / 2
		if step < 1 {
			step = 1
		}
		m.moveCursor(step)
	case "g", "home":
		m.placeOnFirstPuzzle()
		m.clampScroll()
	case "G", "end":
		for i := len(m.browseRows) - 1; i >= 0; i-- {
			if m.browseRows[i].puzzle != nil && m.isRowVisible(i) {
				m.browseIdx = i
				break
			}
		}
		m.clampScroll()
	case "tab":
		src := m.sourceForRow(m.browseIdx)
		if src != "" {
			m.collapsed[src] = !m.collapsed[src]
			if !m.isRowVisible(m.browseIdx) {
				for i := m.browseIdx; i >= 0; i-- {
					if m.browseRows[i].isSource && m.browseRows[i].headerTxt == src {
						m.browseIdx = i
						break
					}
				}
			}
			m.clampScroll()
		}
	case "enter", " ":
		if m.browseIdx < len(m.browseRows) {
			row := m.browseRows[m.browseIdx]
			if row.isSource {
				m.collapsed[row.headerTxt] = !m.collapsed[row.headerTxt]
			} else if !row.isHeader {
				m.current = row.puzzle
				m.hintShown = false
				m.solutionShown = false
				m.result = nil
				m.aiReview = ""
				m.aiErr = ""
				m.aiLoading = false
				m.state = statePuzzleInfo
			}
		}
	case "/":
		m.searchMode = true
		m.searchQuery = ""
		m.preSearchIdx = m.browseIdx
	case "esc":
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.rebuildRows()
			m.placeOnFirstPuzzle()
			m.clampScroll()
		}
	case "R":
		before := m.progress.TotalSolved
		m.progress.Reset()
		_ = m.progress.Save()
		m.flash = fmt.Sprintf("progress reset (%d solved entries cleared)", before)
	case "u":
		if m.browseIdx < len(m.browseRows) {
			row := m.browseRows[m.browseIdx]
			if row.puzzle != nil {
				nowSolved := m.progress.ToggleSolved(row.puzzle.ID)
				_ = m.progress.Save()
				if nowSolved {
					m.flash = "marked solved: " + row.puzzle.Title
				} else {
					m.flash = "unmarked: " + row.puzzle.Title
				}
			}
		}
	case "?":
		m.showHelp = true
	case "q", "ctrl+q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	if delta == 0 || len(m.browseRows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
		delta = -delta
	}
	for moved := 0; moved < delta; {
		next := m.browseIdx + step
		if next < 0 || next >= len(m.browseRows) {
			break
		}
		m.browseIdx = next
		if m.isRowVisible(next) && (m.browseRows[next].isSource || !m.browseRows[next].isHeader) {
			moved++
		}
	}
	m.clampScroll()
}

func (m Model) handlePuzzleInfoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.current != nil {
		switch m.current.Kind {
		case puzzle.KindPredictOutput:
			return m.handlePuzzleInfoKeyPredict(msg)
		case puzzle.KindQuiz:
			return m.handlePuzzleInfoKeyQuiz(msg)
		}
	}
	switch msg.String() {
	case "enter", " ":
		rewrote, err := m.ensureScratch(m.hintShown)
		if err != nil {
			return m, nil
		}
		if rewrote {
			m.flash = "scratch rewritten from template"
		}
		return m, m.openEditor()
	case "h":
		if !m.hintShown {
			m.hintShown = true
		}
	case "s":
		m.solutionShown = true
	case "o":
		if m.current.Reference != "" {
			openURL(m.current.Reference)
		}
	case "r":
		if err := m.writeTemplate(m.hintShown); err != nil {
			m.flash = "reset failed: " + err.Error()
		} else {
			m.flash = "scratch reset to template"
		}
	case "D":
		m.flash = m.deleteScratchFlash()
	case "?":
		m.showHelp = true
	case "b", "esc":
		m.state = stateBrowse
	case "q", "ctrl+q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// handlePuzzleInfoKeyPredict handles keys in the puzzle-info view for
// predict-output puzzles. The action keys differ (no editor, no scratch).
func (m Model) handlePuzzleInfoKeyPredict(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", " ":
		m.answerInput = ""
		m.state = statePredictInput
	case "h":
		m.hintShown = true
	case "s":
		m.solutionShown = true
	case "o":
		if m.current.Reference != "" {
			openURL(m.current.Reference)
		}
	case "?":
		m.showHelp = true
	case "b", "esc":
		m.state = stateBrowse
	case "q", "ctrl+q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// handlePuzzleInfoKeyQuiz handles keys for quiz puzzles. Multiple-choice:
// a/b/c/d (or 1/2/3/4) picks a choice and immediately routes to stateResult.
func (m Model) handlePuzzleInfoKeyQuiz(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	if idx, ok := quizChoiceIndex(s); ok {
		if idx < len(m.current.Choices) {
			return m.submitQuizAnswer(m.current.Choices[idx])
		}
	}
	switch s {
	case "h":
		m.hintShown = true
	case "s":
		m.solutionShown = true
	case "o":
		if m.current.Reference != "" {
			openURL(m.current.Reference)
		}
	case "?":
		m.showHelp = true
	case "b", "esc":
		m.state = stateBrowse
	case "q", "ctrl+q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// quizChoiceIndex maps a keypress to a choice index. a/b/c/d -> 0..3, and
// 1/2/3/4 also work for keyboard-friendly numeric input.
func quizChoiceIndex(s string) (int, bool) {
	switch s {
	case "a", "A", "1":
		return 0, true
	case "b", "B", "2":
		return 1, true
	case "c", "C", "3":
		return 2, true
	case "d", "D", "4":
		return 3, true
	}
	return -1, false
}

// submitQuizAnswer compares the picked choice to the recorded answer and
// routes to stateResult with a synthetic Result.
func (m Model) submitQuizAnswer(pick string) (tea.Model, tea.Cmd) {
	passed := pick == m.current.Answer
	m.userSolution = pick
	output := ""
	if !passed {
		output = "You chose: " + pick + "\nCorrect answer: " + m.current.Answer
	}
	m.result = &runner.Result{Passed: passed, Output: output}
	if passed {
		m.progress.RecordAttempt(m.current.ID, true)
		_ = m.progress.Save()
	}
	m.state = stateResult
	return m, nil
}

// handlePredictInputKey processes typing in the predict-output answer field.
// ctrl+j inserts a literal newline so the user can match multi-line
// expected output; enter submits.
func (m Model) handlePredictInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.submitPredictAnswer()
	case "ctrl+j":
		m.answerInput += "\n"
	case "esc":
		m.answerInput = ""
		m.state = statePuzzleInfo
	case "backspace":
		if n := len(m.answerInput); n > 0 {
			// Trim one byte; safe for ASCII. For multi-byte runes we'd
			// trim the trailing rune, but the typical answer is ASCII.
			m.answerInput = m.answerInput[:n-1]
		}
	case "ctrl+u":
		m.answerInput = ""
	case "ctrl+c", "ctrl+q":
		return m, tea.Quit
	default:
		if len(msg.Runes) > 0 {
			m.answerInput += string(msg.Runes)
		}
	}
	return m, nil
}

// submitPredictAnswer compares the typed answer against the expected output
// (whitespace-trimmed) and routes to stateResult with a synthetic runner.Result.
func (m Model) submitPredictAnswer() (tea.Model, tea.Cmd) {
	got := strings.TrimRight(m.answerInput, " \t\n")
	want := strings.TrimRight(m.current.ExpectedOutput, " \t\n")
	passed := got == want
	m.answerOK = passed
	m.userSolution = m.answerInput
	output := ""
	if !passed {
		output = "Expected:\n" + want + "\n\nGot:\n" + got
	}
	m.result = &runner.Result{Passed: passed, Output: output}
	if passed {
		m.progress.RecordAttempt(m.current.ID, true)
		_ = m.progress.Save()
	}
	m.state = stateResult
	return m, nil
}

func (m Model) handleResultKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", " ":
		if m.result != nil && m.result.Passed {
			if !m.advanceToNextUnsolved() {
				m.state = stateDone
			}
			return m, nil
		}
		if m.current != nil {
			switch m.current.Kind {
			case puzzle.KindPredictOutput:
				m.answerInput = ""
				m.state = statePredictInput
				return m, nil
			case puzzle.KindQuiz:
				m.state = statePuzzleInfo
				return m, nil
			}
		}
		return m, m.openEditor()
	case "n":
		if m.result != nil && m.result.Passed {
			if !m.advanceToNextUnsolved() {
				m.state = stateDone
			}
		}
	case "a":
		if m.result == nil || m.current == nil {
			break
		}
		if m.current.Kind != puzzle.KindCode && m.current.Kind != puzzle.KindFix {
			break
		}
		if m.aiLoading || m.aiReview != "" || m.aiErr != "" {
			break
		}
		m.aiLoading = true
		if m.result.Passed {
			return m, tea.Batch(m.spinner.Tick, m.requestAIReview())
		}
		return m, tea.Batch(m.spinner.Tick, m.requestAIHint())
	case "e":
		if m.result != nil && m.result.Passed && m.current != nil {
			switch m.current.Kind {
			case puzzle.KindCode, puzzle.KindFix:
				return m, m.openEditor()
			}
		}
	case "h":
		m.hintShown = true
	case "s":
		m.solutionShown = true
	case "o":
		if m.current.Reference != "" {
			openURL(m.current.Reference)
		}
	case "r":
		if m.current != nil && m.current.Kind == puzzle.KindPredictOutput {
			break // predict puzzles have no scratch to reset
		}
		if err := m.writeTemplate(m.hintShown); err != nil {
			m.flash = "reset failed: " + err.Error()
		} else {
			m.flash = "scratch reset to template"
		}
	case "D":
		if m.current != nil && m.current.Kind == puzzle.KindPredictOutput {
			break
		}
		m.flash = m.deleteScratchFlash()
	case "?":
		m.showHelp = true
	case "b", "esc":
		m.state = stateBrowse
	case "q", "ctrl+q", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// requestAIReview kicks off an asynchronous Anthropic call for a short
// review of the user's solution. The returned tea.Cmd produces an
// aiReviewMsg when the request finishes (or errors).
func (m Model) requestAIReview() tea.Cmd {
	cur := m.current
	user := m.userSolution
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		review, err := ai.Review(ctx, ai.ReviewRequest{
			Title:       cur.Title,
			Description: cur.Description,
			Canonical:   cur.Solution,
			UserCode:    user,
		})
		return aiReviewMsg{review: review, err: err}
	}
}

// requestAIHint kicks off an asynchronous Anthropic call for a short,
// targeted hint on a failing attempt. The canonical solution is NOT
// included in the prompt — the model is instructed to hint, not solve.
func (m Model) requestAIHint() tea.Cmd {
	cur := m.current
	user := m.userSolution
	failure := ""
	if m.result != nil {
		failure = m.result.Output
		if cur.Kind == "" || cur.Kind == puzzle.KindCode {
			failure = cleanTestOutput(failure)
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		hint, err := ai.Hint(ctx, ai.HintRequest{
			Title:       cur.Title,
			Description: cur.Description,
			UserCode:    user,
			Failure:     failure,
		})
		return aiReviewMsg{review: hint, err: err}
	}
}

// deleteScratchFlash removes the current puzzle's scratch directory and
// returns a flash message describing the result. Missing dirs are treated
// as success — the operation is idempotent.
func (m Model) deleteScratchFlash() string {
	dir := m.scratchDirFor()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return "no scratch file to delete"
	}
	if err := os.RemoveAll(dir); err != nil {
		return "delete failed: " + err.Error()
	}
	return "scratch file deleted"
}

// advanceToNextUnsolved moves to the next puzzle in browse order that hasn't
// been solved yet (skipping the current one). Returns false if none remain.
func (m *Model) advanceToNextUnsolved() bool {
	// Walk full puzzle list, not filtered browseRows, so an active search
	// can't strand the user.
	var ordered []*puzzle.Puzzle
	ordered = append(ordered, m.puzzles...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Source != ordered[j].Source {
			return ordered[i].Source < ordered[j].Source
		}
		if ordered[i].Section != ordered[j].Section {
			return ordered[i].Section < ordered[j].Section
		}
		return ordered[i].ID < ordered[j].ID
	})

	curID := ""
	if m.current != nil {
		curID = m.current.ID
	}
	for _, p := range ordered {
		if p.ID == curID {
			continue
		}
		if m.progress.Solved[p.ID] {
			continue
		}
		m.current = p
		m.hintShown = false
		m.solutionShown = false
		m.result = nil
		m.userSolution = ""
		m.aiReview = ""
		m.aiErr = ""
		m.aiLoading = false
		m.state = statePuzzleInfo
		// move browse cursor too
		for i, row := range m.browseRows {
			if row.puzzle != nil && row.puzzle.ID == p.ID {
				m.browseIdx = i
				break
			}
		}
		m.clampScroll()
		return true
	}
	return false
}

func (m Model) visibleIndex(idx int) int {
	n := 0
	for i := 0; i < idx; i++ {
		if m.isRowVisible(i) {
			n++
		}
	}
	return n
}

func (m *Model) clampScroll() {
	visible := m.visibleRowCount()
	cursorPos := m.visibleIndex(m.browseIdx)
	if cursorPos < m.browseOffset {
		m.browseOffset = cursorPos
	}
	if cursorPos >= m.browseOffset+visible {
		m.browseOffset = cursorPos - visible + 1
	}
	if m.browseOffset < 0 {
		m.browseOffset = 0
	}
}

func (m Model) visibleRowCount() int {
	visible := m.height - 8
	if m.searchMode || m.searchQuery != "" {
		visible -= 2
	}
	if visible < 1 {
		visible = 10
	}
	return visible
}

func (m Model) View() string {
	if m.showHelp {
		return m.viewHelp()
	}
	switch m.state {
	case stateBrowse:
		return m.viewBrowse()
	case statePuzzleInfo:
		return m.viewPuzzleInfo()
	case statePredictInput:
		return m.viewPredictInput()
	case stateRunning:
		return fmt.Sprintf("\n\n  %s Compiling and running tests...\n", m.spinner.View())
	case stateResult:
		return m.viewResult()
	case stateDone:
		return m.viewDone()
	}
	return ""
}

func (m Model) sourceForRow(idx int) string {
	for i := idx; i >= 0; i-- {
		if m.browseRows[i].isSource {
			return m.browseRows[i].headerTxt
		}
	}
	return ""
}

func (m Model) isRowVisible(idx int) bool {
	if idx < 0 || idx >= len(m.browseRows) {
		return false
	}
	row := m.browseRows[idx]
	if row.isSource {
		return true
	}
	return !m.collapsed[m.sourceForRow(idx)]
}

func (m Model) viewBrowse() string {
	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s",
		styleTitle.Render("gopuzzle"),
		styleScore.Render(fmt.Sprintf("%d / %d solved", m.currentSolvedCount(), m.totalPuzzles)),
	))

	var searchLine string
	if m.searchMode || m.searchQuery != "" {
		caret := ""
		if m.searchMode {
			caret = "▎"
		}
		hitCount := m.countPuzzleRows()
		status := styleKeybind.Render(fmt.Sprintf("  (%d match)", hitCount))
		if hitCount == 0 {
			status = styleHint.Render("  (no matches)")
		}
		searchLine = "  " + styleKeyName.Render("/") + " " + styleDescription.Render(m.searchQuery) + caret + status
	}

	visible := m.visibleRowCount()
	var lines []string
	shown := 0
	skipped := 0
	for i := 0; i < len(m.browseRows) && shown < visible; i++ {
		if !m.isRowVisible(i) {
			continue
		}
		if skipped < m.browseOffset {
			skipped++
			continue
		}
		row := m.browseRows[i]
		switch {
		case row.isSource:
			lines = append(lines, m.renderSourceRow(i, row))
		case row.isHeader:
			lines = append(lines, m.renderSectionRow(row))
		default:
			lines = append(lines, m.renderPuzzleRow(i, row))
		}
		shown++
	}

	if len(lines) == 0 && (m.searchMode || m.searchQuery != "") {
		lines = []string{"  " + styleHint.Render("No puzzles match this filter.")}
	}

	var keys string
	if m.searchMode {
		keys = styleKeybind.Render(
			styleKeyName.Render("enter") + " accept  " +
				styleKeyName.Render("esc") + " cancel  " +
				styleKeyName.Render("⌫") + " delete",
		)
	} else {
		keys = styleKeybind.Render(
			styleKeyName.Render("↑↓") + " move  " +
				styleKeyName.Render("enter") + " open  " +
				styleKeyName.Render("tab") + " collapse  " +
				styleKeyName.Render("/") + " search  " +
				styleKeyName.Render("u") + " toggle solved  " +
				styleKeyName.Render("R") + " reset all  " +
				styleKeyName.Render("?") + " help  " +
				styleKeyName.Render("q") + " quit",
		)
	}

	parts := []string{header}
	if searchLine != "" {
		parts = append(parts, searchLine, "")
	}
	parts = append(parts, strings.Join(lines, "\n"))
	if m.flash != "" {
		parts = append(parts, "", "  "+styleHint.Render(m.flash))
	}
	parts = append(parts, "", "  "+keys)
	return strings.Join(parts, "\n")
}

func (m Model) renderSourceRow(idx int, row browseRow) string {
	arrow := "▼"
	if m.collapsed[row.headerTxt] {
		arrow = "▶"
	}
	cursor := "  "
	nameStyle := styleTitle
	if idx == m.browseIdx {
		cursor = styleKeyName.Render("▶ ")
		nameStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	}
	solved, total := m.sourceProgress(row.sourceName)
	return fmt.Sprintf("  %s%s %s  %s", cursor, arrow, nameStyle.Render(row.headerTxt), progressBadge(solved, total))
}

func (m Model) renderSectionRow(row browseRow) string {
	solved, total := m.sectionProgress(row.sectionPath)
	return fmt.Sprintf("      %s  %s", styleConcept.Render(row.headerTxt), progressBadge(solved, total))
}

func (m Model) renderPuzzleRow(idx int, row browseRow) string {
	p := row.puzzle
	check := "  "
	if m.progress.Solved[p.ID] {
		check = stylePass.Render("✓ ")
	}
	cursor := "  "
	titleStyle := styleDescription
	if idx == m.browseIdx {
		cursor = styleKeyName.Render("▶ ")
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	}
	return fmt.Sprintf("      %s%s%s  %s  %s",
		cursor,
		check,
		styleOutput.Render(p.ID),
		titleStyle.Render(p.Title),
		kindBadge(p.Kind),
	)
}

func progressBadge(solved, total int) string {
	if total == 0 {
		return ""
	}
	txt := fmt.Sprintf("%d/%d", solved, total)
	switch {
	case solved == total:
		return stylePass.Render(txt)
	case solved == 0:
		return styleOutput.Render(txt)
	default:
		return styleHint.Render(txt)
	}
}

// currentSolvedCount returns the number of *currently loaded* puzzles that are
// marked solved in progress. Reading progress.TotalSolved directly is wrong
// when puzzles get deleted or renamed — those IDs linger in progress.json but
// no longer exist, inflating the score.
func (m Model) currentSolvedCount() int {
	n := 0
	for _, p := range m.puzzles {
		if m.progress.Solved[p.ID] {
			n++
		}
	}
	return n
}

func (m Model) sourceProgress(source string) (int, int) {
	total := m.sourceTotals[source]
	solved := 0
	for _, p := range m.puzzles {
		if p.Source == source && m.progress.Solved[p.ID] {
			solved++
		}
	}
	return solved, total
}

func (m Model) sectionProgress(path string) (int, int) {
	total := m.sectionTotals[path]
	solved := 0
	for _, p := range m.puzzles {
		if p.Source+"/"+p.Section == path && m.progress.Solved[p.ID] {
			solved++
		}
	}
	return solved, total
}

func (m Model) countPuzzleRows() int {
	n := 0
	for _, r := range m.browseRows {
		if r.puzzle != nil {
			n++
		}
	}
	return n
}

func (m Model) viewPuzzleInfo() string {
	if m.current == nil {
		return ""
	}
	switch m.current.Kind {
	case puzzle.KindPredictOutput:
		return m.viewPuzzleInfoPredict()
	case puzzle.KindQuiz:
		return m.viewPuzzleInfoQuiz()
	}

	solvedBadge := ""
	if m.progress.Solved[m.current.ID] {
		solvedBadge = "  " + stylePass.Render("✓ solved")
	}

	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s / %s%s",
		styleTitle.Render("gopuzzle"),
		styleConcept.Render(m.current.Source),
		styleConcept.Render(m.current.Section),
		solvedBadge,
	))

	title := styleTitle.Render(m.current.Title)
	desc := renderInline(wordWrap(m.current.Description, m.width-4), styleDescription)

	var hintLine string
	if m.hintShown {
		hintLine = "\n" + "  " + renderInline("Hint: " + m.current.Hint, styleHint)
	}

	keys := styleKeybind.Render(
		styleKeyName.Render("enter") + " open in " + editorBin() + "  " +
			styleKeyName.Render("h") + " hint  " +
			styleKeyName.Render("s") + " solution  " +
			styleKeyName.Render("o") + " ref  " +
			styleKeyName.Render("r") + " reset  " +
			styleKeyName.Render("D") + " del  " +
			styleKeyName.Render("b") + " back  " +
			styleKeyName.Render("?") + " help  " +
			styleKeyName.Render("q") + " quit",
	)

	parts := []string{
		header,
		"  " + title,
		"",
		"  " + desc,
	}
	if m.current.Reference != "" {
		parts = append(parts, "  "+styleKeybind.Render("ref: ")+styleKeyName.Render(m.current.Reference))
	}
	if hintLine != "" {
		parts = append(parts, hintLine)
	}
	if m.solutionShown {
		if m.current.Solution != "" {
			parts = append(parts, "\n"+renderCodeBlock(m.current.Solution, true))
		} else {
			parts = append(parts, "\n  "+styleHint.Render("No suggested solution available for this puzzle."))
		}
	}
	if m.flash != "" {
		parts = append(parts, "", "  "+styleHint.Render(m.flash))
	}
	parts = append(parts, "", "  "+keys)
	return strings.Join(parts, "\n")
}

// viewPuzzleInfoPredict renders the puzzle-info screen for predict-output
// puzzles: shows the snippet and prompts the user to press enter to type
// their predicted output.
func (m Model) viewPuzzleInfoPredict() string {
	solvedBadge := ""
	if m.progress.Solved[m.current.ID] {
		solvedBadge = "  " + stylePass.Render("✓ solved")
	}
	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s / %s%s  %s",
		styleTitle.Render("gopuzzle"),
		styleConcept.Render(m.current.Source),
		styleConcept.Render(m.current.Section),
		solvedBadge,
		styleHint.Render("predict-output"),
	))

	title := styleTitle.Render(m.current.Title)
	desc := renderInline(wordWrap(m.current.Description, m.width-4), styleDescription)
	snippet := renderCodeBlock(strings.TrimRight(m.current.Snippet, "\n"), true)

	keys := styleKeybind.Render(
		styleKeyName.Render("enter") + " type your prediction  " +
			styleKeyName.Render("h") + " hint  " +
			styleKeyName.Render("s") + " expected output  " +
			styleKeyName.Render("o") + " ref  " +
			styleKeyName.Render("b") + " back  " +
			styleKeyName.Render("?") + " help  " +
			styleKeyName.Render("q") + " quit",
	)

	parts := []string{header, "  " + title, "", "  " + desc, "", snippet}
	if m.current.Reference != "" {
		parts = append(parts, "  "+styleKeybind.Render("ref: ")+styleKeyName.Render(m.current.Reference))
	}
	if m.hintShown && m.current.Hint != "" {
		parts = append(parts, "  " + renderInline("Hint: " + m.current.Hint, styleHint))
	}
	if m.solutionShown {
		parts = append(parts, "", "  "+styleKeybind.Render("Expected output:"),
			renderCodeBlock(m.current.ExpectedOutput, false))
	}
	parts = append(parts, "", "  "+keys)
	return strings.Join(parts, "\n")
}

// viewPuzzleInfoQuiz renders a multiple-choice question with lettered
// choices. The user picks by pressing the corresponding letter.
func (m Model) viewPuzzleInfoQuiz() string {
	solvedBadge := ""
	if m.progress.Solved[m.current.ID] {
		solvedBadge = "  " + stylePass.Render("✓ solved")
	}
	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s / %s%s  %s",
		styleTitle.Render("gopuzzle"),
		styleConcept.Render(m.current.Source),
		styleConcept.Render(m.current.Section),
		solvedBadge,
		styleHint.Render("quiz"),
	))
	title := styleTitle.Render(m.current.Title)
	desc := renderInline(wordWrap(m.current.Description, m.width-4), styleDescription)
	question := renderInline(wordWrap(m.current.Question, m.width-4), styleDescription)

	letters := []string{"a", "b", "c", "d"}
	var choiceLines []string
	for i, choice := range m.current.Choices {
		if i >= len(letters) {
			break
		}
		choiceLines = append(choiceLines,
			"    "+styleKeyName.Render(letters[i])+")  "+styleDescription.Render(choice))
	}

	keys := styleKeybind.Render(
		styleKeyName.Render("a-d") + " pick  " +
			styleKeyName.Render("h") + " hint  " +
			styleKeyName.Render("s") + " answer  " +
			styleKeyName.Render("o") + " ref  " +
			styleKeyName.Render("b") + " back  " +
			styleKeyName.Render("?") + " help  " +
			styleKeyName.Render("q") + " quit",
	)

	parts := []string{header, "  " + title, "", "  " + desc, "", "  " + question, ""}
	parts = append(parts, choiceLines...)
	if m.current.Reference != "" {
		parts = append(parts, "", "  "+styleKeybind.Render("ref: ")+styleKeyName.Render(m.current.Reference))
	}
	if m.hintShown && m.current.Hint != "" {
		parts = append(parts, "", "  " + renderInline("Hint: " + m.current.Hint, styleHint))
	}
	if m.solutionShown {
		parts = append(parts, "", "  "+styleKeybind.Render("Answer:"),
			"    "+stylePass.Render(m.current.Answer))
	}
	parts = append(parts, "", "  "+keys)
	return strings.Join(parts, "\n")
}

// viewPredictInput renders the snippet plus a single-line answer field while
// the user types their predicted output.
func (m Model) viewPredictInput() string {
	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s",
		styleTitle.Render("gopuzzle"),
		styleConcept.Render(m.current.Title),
	))
	prompt := styleDescription.Render(wordWrap(
		"Type the exact output the snippet would print, then press enter.",
		m.width-4,
	))
	snippet := renderCodeBlock(strings.TrimRight(m.current.Snippet, "\n"), true)
	caret := styleKeyName.Render("▎")
	field := styleBorder.Render(styleDescription.Render(m.answerInput) + caret)
	multiLine := strings.Contains(strings.TrimRight(m.current.ExpectedOutput, "\n"), "\n")
	keybinds := styleKeyName.Render("enter") + " submit  " +
		styleKeyName.Render("⌫") + " backspace  " +
		styleKeyName.Render("ctrl+u") + " clear  "
	if multiLine {
		keybinds += styleKeyName.Render("ctrl+j") + " newline  "
	}
	keybinds += styleKeyName.Render("esc") + " back  " +
		styleKeyName.Render("q") + " quit"
	keys := styleKeybind.Render(keybinds)

	hint := predictFormatHint(m.current.ExpectedOutput)
	return strings.Join([]string{
		header,
		"  " + prompt,
		"",
		snippet,
		"",
		"  " + styleHint.Render(hint),
		"  " + styleKeybind.Render("Your prediction:"),
		field,
		"",
		"  " + keys,
	}, "\n")
}

func (m Model) viewResult() string {
	if m.result == nil {
		return ""
	}

	var lines []string

	if m.result.Passed {
		lines = append(lines, stylePass.Render("  PASS ✓"), "")
		userTrim := strings.TrimSpace(m.userSolution)
		canonTrim := strings.TrimSpace(m.current.Solution)
		switch m.current.Kind {
		case puzzle.KindPredictOutput:
			canonTrim = strings.TrimSpace(m.current.ExpectedOutput)
		case puzzle.KindQuiz:
			canonTrim = strings.TrimSpace(m.current.Answer)
		}
		if userTrim != "" {
			label := "Your solution:"
			switch m.current.Kind {
			case puzzle.KindPredictOutput:
				label = "Your prediction:"
			case puzzle.KindQuiz:
				label = "Your answer:"
			}
			isGo := m.current.Kind == puzzle.KindCode || m.current.Kind == puzzle.KindFix
			lines = append(lines,
				"  "+styleKeybind.Render(label),
				renderCodeBlock(userTrim, isGo),
				"",
			)
		}
		// For code/fix puzzles the user's scratch always begins with a
		// comment header, so the literal-string comparison is never
		// equal. Strip those leading comments to detect "body matches".
		userBody := userTrim
		switch m.current.Kind {
		case puzzle.KindCode, puzzle.KindFix:
			userBody = stripLeadingComments(userTrim)
		}
		if canonTrim != "" && canonTrim != userBody {
			label := "Suggested solution:"
			switch m.current.Kind {
			case puzzle.KindPredictOutput:
				label = "Expected output:"
			case puzzle.KindQuiz:
				label = "Correct answer:"
			}
			isGo := m.current.Kind == puzzle.KindCode || m.current.Kind == puzzle.KindFix
			lines = append(lines,
				"  "+styleKeybind.Render(label),
				renderCodeBlock(canonTrim, isGo),
				"",
			)
		}
		if strings.TrimSpace(m.current.Explanation) != "" {
			lines = append(lines, "  "+renderInline(wordWrap(m.current.Explanation, m.width-4), styleExplanation), "")
		}
		// AI review block (only for code/fix kinds; appears once requested).
		switch m.current.Kind {
		case puzzle.KindCode, puzzle.KindFix:
			if m.aiLoading {
				lines = append(lines,
					"  "+m.spinner.View()+" "+styleHint.Render("AI review loading..."),
					"",
				)
			} else if m.aiErr != "" {
				lines = append(lines,
					"  "+styleKeybind.Render("AI review:"),
					"  "+styleFail.Render(m.aiErr),
					"",
				)
			} else if m.aiReview != "" {
				lines = append(lines,
					"  "+styleKeybind.Render("AI review:"),
					styleBorder.Render(renderInline(wordWrap(m.aiReview, m.width-6), styleDescription)),
					"",
				)
			}
		}
		nextKeys := styleKeyName.Render("enter") + " next puzzle  "
		switch m.current.Kind {
		case puzzle.KindCode, puzzle.KindFix:
			nextKeys += styleKeyName.Render("e") + " edit  "
			if m.aiReview == "" && m.aiErr == "" && !m.aiLoading {
				nextKeys += styleKeyName.Render("a") + " AI review  "
			}
		}
		lines = append(lines,
			"  "+styleKeybind.Render(
				nextKeys+
					styleKeyName.Render("b")+" browser  "+
					styleKeyName.Render("q")+" quit",
			),
		)
	} else {
		output := m.result.Output
		// Only code-kind output is `go test -v` raw text. Other kinds
		// (predict/quiz/fix) already format their own messages.
		if m.current.Kind == "" || m.current.Kind == puzzle.KindCode {
			output = cleanTestOutput(output)
		}
		outputBox := styleBorder.Render(styleOutput.Render(truncate(output, 30)))
		lines = append(lines,
			styleFail.Render("  FAIL ✗"),
			"",
			outputBox,
		)
		if m.current.Reference != "" {
			lines = append(lines, "  "+styleKeybind.Render("ref: ")+styleKeyName.Render(m.current.Reference))
		}
		if m.hintShown {
			lines = append(lines, "", "  " + renderInline("Hint: " + m.current.Hint, styleHint))
		}
		if m.solutionShown {
			shown := m.current.Solution
			switch m.current.Kind {
			case puzzle.KindPredictOutput:
				shown = m.current.ExpectedOutput
			case puzzle.KindQuiz:
				shown = m.current.Answer
			}
			if shown != "" {
				isGo := m.current.Kind == puzzle.KindCode || m.current.Kind == puzzle.KindFix
				lines = append(lines, "", renderCodeBlock(shown, isGo))
			} else {
				lines = append(lines, "", "  "+styleHint.Render("No suggested solution available for this puzzle."))
			}
		}
		// AI hint block (only for code/fix kinds, populated after `a`).
		switch m.current.Kind {
		case puzzle.KindCode, puzzle.KindFix:
			if m.aiLoading {
				lines = append(lines, "", "  "+m.spinner.View()+" "+styleHint.Render("AI hint loading..."))
			} else if m.aiErr != "" {
				lines = append(lines,
					"",
					"  "+styleKeybind.Render("AI hint:"),
					"  "+styleFail.Render(m.aiErr),
				)
			} else if m.aiReview != "" {
				lines = append(lines,
					"",
					"  "+styleKeybind.Render("AI hint:"),
					styleBorder.Render(renderInline(wordWrap(m.aiReview, m.width-6), styleDescription)),
				)
			}
		}
		if m.flash != "" {
			lines = append(lines, "", "  "+styleHint.Render(m.flash))
		}
		retry := "retry in " + editorBin()
		switch m.current.Kind {
		case puzzle.KindPredictOutput, puzzle.KindQuiz:
			retry = "try again"
		}
		failKeys := styleKeyName.Render("enter") + " " + retry +
			"  " + styleKeyName.Render("h") + " hint" +
			"  " + styleKeyName.Render("s") + " solution"
		switch m.current.Kind {
		case puzzle.KindCode, puzzle.KindFix:
			if m.aiReview == "" && m.aiErr == "" && !m.aiLoading {
				failKeys += "  " + styleKeyName.Render("a") + " AI hint"
			}
		}
		failKeys += "  " + styleKeyName.Render("o") + " ref" +
			"  " + styleKeyName.Render("b") + " back" +
			"  " + styleKeyName.Render("?") + " help" +
			"  " + styleKeyName.Render("q") + " quit"
		lines = append(lines, "", "  "+styleKeybind.Render(failKeys))
	}

	return "\n" + strings.Join(lines, "\n")
}

func (m Model) viewDone() string {
	return "\n\n  " + stylePass.Render(fmt.Sprintf("All %d puzzles solved.", m.totalPuzzles)) +
		"\n\n  " + styleExplanation.Render("That's the whole catalog. Beautiful work.") +
		"\n\n  " + styleKeybind.Render("press "+styleKeyName.Render("q")+" to quit, or "+styleKeyName.Render("b")+" to keep browsing")
}

func (m Model) viewHelp() string {
	rows := []struct {
		k, v string
	}{
		{"Browser", ""},
		{"↑ ↓  j k", "move cursor"},
		{"g  G", "first / last puzzle"},
		{"pgup  pgdn", "page up / down"},
		{"enter", "open puzzle"},
		{"tab", "collapse / expand source"},
		{"/", "search puzzles"},
		{"u", "toggle solved on highlighted puzzle"},
		{"R", "reset all progress"},
		{"", ""},
		{"Puzzle & result", ""},
		{"enter", "open editor · retry on fail · next on pass"},
		{"e", "re-open editor on PASS (code · fix)"},
		{"a", "AI review on PASS, AI hint on FAIL (code/fix; needs ANTHROPIC_API_KEY)"},
		{"h", "show hint"},
		{"s", "show suggested solution"},
		{"o", "open reference URL in browser"},
		{"r", "reset scratch file to template"},
		{"D", "delete scratch file (no rewrite)"},
		{"b  esc", "back to browser"},
		{"", ""},
		{"Anywhere", ""},
		{"?", "this help"},
		{"q  ctrl+c", "quit"},
	}

	var b strings.Builder
	b.WriteString(styleHeader.Render(styleTitle.Render("gopuzzle — keys")))
	b.WriteString("\n")
	for _, r := range rows {
		if r.k == "" && r.v == "" {
			b.WriteString("\n")
			continue
		}
		if r.v == "" {
			b.WriteString("  ")
			b.WriteString(styleConcept.Render(r.k))
			b.WriteString("\n")
			continue
		}
		b.WriteString(fmt.Sprintf("    %-14s  %s\n", styleKeyName.Render(r.k), styleDescription.Render(r.v)))
	}
	b.WriteString("\n  ")
	b.WriteString(styleKeybind.Render("press any key to dismiss"))
	return b.String()
}

// renderInline takes prose with `code` spans marked by backticks and renders
// each span bold while the surrounding text uses the supplied base style.
// Backticks themselves are removed from the output. Unterminated spans are
// rendered as plain base text (the stray backtick is dropped).
//
// We pass each line through separately and call Inline(true) on the styles
// so lipgloss doesn't apply block-level padding when a segment happens to
// span newlines (which would left-pad continuation lines to match width).
func renderInline(text string, base lipgloss.Style) string {
	base = base.Inline(true)
	codeStyle := base.Bold(true)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = renderInlineLine(line, base, codeStyle)
	}
	return strings.Join(lines, "\n")
}

func renderInlineLine(line string, base, codeStyle lipgloss.Style) string {
	var b strings.Builder
	var seg strings.Builder
	inCode := false
	flush := func() {
		s := seg.String()
		seg.Reset()
		if s == "" {
			return
		}
		if inCode {
			b.WriteString(codeStyle.Render(s))
		} else {
			b.WriteString(base.Render(s))
		}
	}
	for _, r := range line {
		if r == '`' {
			flush()
			inCode = !inCode
			continue
		}
		seg.WriteRune(r)
	}
	if inCode {
		inCode = false
	}
	flush()
	return b.String()
}

func wordWrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			lines = append(lines, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > width {
				lines = append(lines, line)
				line = w
			} else {
				line += " " + w
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n  ")
}

func truncate(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") + "\n  ..."
}
