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

// browseRow is one row in the browse list: a language banner, source
// header, section header, or puzzle entry.
type browseRow struct {
	isHeader    bool
	isSource    bool
	isLanguage  bool   // language banner (top-level grouping)
	headerTxt   string
	sourceName  string // set on source headers
	sectionPath string // set on section headers: "source/section"
	language    string // set on language banners (e.g. "go", "python")
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

	// Scroll offset for the result screen (PASS/FAIL with long
	// explanations). Reset to 0 whenever we enter stateResult.
	resultScroll int

	// Scroll offset for the puzzle-info screen (description / question
	// / snippet often exceed terminal height). Reset to 0 whenever
	// statePuzzleInfo is entered fresh.
	infoScroll int

	totalPuzzles   int
	sectionTotals  map[string]int
	sourceTotals   map[string]int
	languageTotals map[string]int
}

// pruneOrphans=true when running with the canonical (embedded) puzzle
// set — IDs in progress not in puzzles are deleted/renamed puzzles and
// safe to drop. Pass false when running with a runtime puzzle override
// (GOPUZZLE_DIR), where "absent" usually just means "not in this
// subset I'm authoring on" — pruning would wipe canonical progress.
func New(p *progress.Progress, puzzles []*puzzle.Puzzle, pruneOrphans bool) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorBlue)

	scratchDir := defaultScratchDir()
	_ = os.MkdirAll(scratchDir, 0755)

	m := Model{
		state:          stateBrowse,
		spinner:        sp,
		progress:       p,
		puzzles:        puzzles,
		collapsed:      make(map[string]bool),
		scratchDir:     scratchDir,
		sectionTotals:  make(map[string]int),
		sourceTotals:   make(map[string]int),
		languageTotals: make(map[string]int),
	}
	validIDs := make(map[string]bool, len(puzzles))
	sectionSolved := make(map[string]int)
	for _, pz := range puzzles {
		validIDs[pz.ID] = true
		m.totalPuzzles++
		m.sectionTotals[pz.Source+"/"+pz.Section]++
		m.sourceTotals[pz.Source]++
		m.languageTotals[pz.Lang]++
		if p.Solved[pz.ID] {
			sectionSolved[pz.Source+"/"+pz.Section]++
		}
	}
	if pruneOrphans {
		if removed := p.PruneOrphans(validIDs); removed > 0 {
			_ = p.Save()
		}
	}
	// Fully-solved sections start collapsed so the browse view
	// foregrounds work the user still has to do. They can still
	// expand them manually with `space`.
	for path, total := range m.sectionTotals {
		if sectionSolved[path] == total {
			m.collapsed["sec:"+path] = true
		}
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
		if filtered[i].Lang != filtered[j].Lang {
			return filtered[i].Lang < filtered[j].Lang
		}
		if filtered[i].Source != filtered[j].Source {
			return filtered[i].Source < filtered[j].Source
		}
		if filtered[i].Section != filtered[j].Section {
			return filtered[i].Section < filtered[j].Section
		}
		return filtered[i].ID < filtered[j].ID
	})

	var rows []browseRow
	lastLang, lastSource, lastSection := "", "", ""
	for _, p := range filtered {
		if p.Lang != lastLang {
			rows = append(rows, browseRow{isHeader: true, isLanguage: true, headerTxt: puzzle.DisplayLanguage(p.Lang), language: p.Lang})
			lastLang = p.Lang
			lastSource = ""
			lastSection = ""
		}
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
	return filepath.Join(m.scratchDirFor(), "main."+m.scratchExt())
}

// scratchExt returns the source extension for the current puzzle's
// language. Defaults to "go" when there's no current puzzle (e.g. before
// one is opened).
func (m *Model) scratchExt() string {
	if m.current == nil || m.current.Lang == "" || m.current.Lang == "go" {
		return "go"
	}
	if m.current.Lang == "python" {
		return "py"
	}
	return "go"
}

// scratchCommentPrefix returns the line-comment marker for the puzzle's
// language. Go uses `//`, Python uses `#`.
func (m *Model) scratchCommentPrefix() string {
	if m.scratchExt() == "py" {
		return "#"
	}
	return "//"
}

func (m *Model) writeTemplate(includeHint bool) error {
	dir := m.scratchDirFor()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Drop a per-language module-scope marker so the editor's LSP
	// treats this directory as self-contained rather than as part of
	// some larger package.
	switch m.scratchExt() {
	case "go":
		gomod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(gomod); err != nil {
			if err := os.WriteFile(gomod, []byte("module puzzle\n\ngo 1.23\n"), 0o644); err != nil {
				return err
			}
		}
	case "py":
		// An empty __init__.py gives Pyright / Ruff LSP a clean module
		// boundary, so they don't try to resolve symbols against any
		// neighbouring scratch.
		initPy := filepath.Join(dir, "__init__.py")
		if _, err := os.Stat(initPy); err != nil {
			if err := os.WriteFile(initPy, nil, 0o644); err != nil {
				return err
			}
		}
	}

	cmt := m.scratchCommentPrefix()
	var b strings.Builder
	// Header: title, ref, then the description as comments so the
	// puzzle's instructions stay visible inside the editor.
	// stripLeadingComments removes this block before display in the
	// "Your solution" box so the result screen stays compact.
	b.WriteString(cmt + " ")
	b.WriteString(m.current.Title)
	b.WriteString("\n")
	if m.current.Reference != "" {
		b.WriteString(cmt + " ref: ")
		b.WriteString(m.current.Reference)
		b.WriteString("\n")
	}
	if desc := strings.TrimSpace(m.current.Description); desc != "" {
		b.WriteString(cmt + "\n")
		for _, line := range strings.Split(desc, "\n") {
			if line == "" {
				b.WriteString(cmt + "\n")
				continue
			}
			b.WriteString(cmt + " ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(m.current.Template)
	if includeHint && m.current.Hint != "" {
		b.WriteString("\n" + cmt + " HINT: ")
		b.WriteString(m.current.Hint)
		b.WriteString("\n")
	}
	return os.WriteFile(m.scratchPath(), []byte(b.String()), 0o644)
}

// ensureScratch makes the scratch file ready to open. Returns the
// source the scratch was populated from:
//   "" = no change (existing scratch is fine)
//   "template" = rewrote from the puzzle's template
//   "solution" = restored from the user's previously saved passing solution
//
// For solved code/fix puzzles with a saved solution, the saved solution
// wins — when you revisit a solved puzzle you see what you submitted,
// not whatever stale junk was last in scratch.
func (m *Model) ensureScratch(includeHint bool) (string, error) {
	data, err := os.ReadFile(m.scratchPath())
	scratchMissing := err != nil

	// Prefer restoring from a saved solution when this puzzle is solved.
	if m.current != nil && m.progress.Solved[m.current.ID] {
		if m.current.Kind == puzzle.KindCode || m.current.Kind == puzzle.KindFix {
			if sol, lerr := progress.LoadSolution(m.current.Dir, m.current.Stem); lerr == nil {
				// Already in sync — no write needed.
				if !scratchMissing && string(data) == string(sol) {
					return "", nil
				}
				if err := m.writeRawScratch(sol); err != nil {
					return "", err
				}
				return "solution", nil
			}
		}
	}

	if scratchMissing {
		return "template", m.writeTemplate(includeHint)
	}
	if m.scratchHasTemplateDrift(string(data)) {
		return "template", m.writeTemplate(includeHint)
	}
	return "", nil
}

// writeRawScratch writes literal bytes into the scratch file, also
// ensuring the per-puzzle module marker (go.mod / __init__.py) is in
// place so the editor's LSP gets the right scope. Used when restoring
// from a saved solution where the content is already finished code —
// no template rendering needed.
func (m *Model) writeRawScratch(data []byte) error {
	dir := m.scratchDirFor()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	switch m.scratchExt() {
	case "go":
		gomod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(gomod); err != nil {
			if err := os.WriteFile(gomod, []byte("module puzzle\n\ngo 1.23\n"), 0o644); err != nil {
				return err
			}
		}
	case "py":
		initPy := filepath.Join(dir, "__init__.py")
		if _, err := os.Stat(initPy); err != nil {
			if err := os.WriteFile(initPy, nil, 0o644); err != nil {
				return err
			}
		}
	}
	return os.WriteFile(m.scratchPath(), data, 0o644)
}

// scratchHasTemplateDrift returns true when the puzzle's template declares
// at least one signature (`func`, `def`, or `class`) or a `package X` line
// that no longer appears in the scratch. A signature mismatch means the
// puzzle's tests cannot bind to the scratch; a package mismatch means
// the runner's solution + test_code won't compile together. Either way,
// the prior body is unusable and rewriting is the right move.
func (m *Model) scratchHasTemplateDrift(scratch string) bool {
	for _, line := range strings.Split(m.current.Template, "\n") {
		trimmed := strings.TrimSpace(line)
		if !isSignatureLine(trimmed) && !strings.HasPrefix(trimmed, "package ") {
			continue
		}
		if !strings.Contains(scratch, trimmed) {
			return true
		}
	}
	return false
}

// isSignatureLine returns true if a stripped line introduces a top-level
// definition we want to anchor on for drift detection. Catches Go's
// `func ...` and Python's `def ...` / `class ...` headers.
func isSignatureLine(s string) bool {
	return strings.HasPrefix(s, "func ") ||
		strings.HasPrefix(s, "def ") ||
		strings.HasPrefix(s, "class ")
}

func (m Model) openEditor() tea.Cmd {
	bin := editorBin()
	args := editorArgs(bin)
	args = append(args, m.scratchPath())
	cmd := exec.Command(bin, args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return viExitMsg{err: err}
	})
}

// editorArgs returns the flags to pass before the filename. For vim
// and nvim we add `--noplugin` so the user's LSP / completion /
// formatter / Treesitter plugins don't attach — puzzles are short
// scratches where gopls diagnostics and semantic highlighting add
// noise rather than signal. We also force `:syntax on` so the
// built-in regex syntax engine still colours the file (it ships
// with nvim and doesn't require any plugin).
// Override by setting GOPUZZLE_EDITOR_NO_PLUGINS=0.
func editorArgs(bin string) []string {
	if os.Getenv("GOPUZZLE_EDITOR_NO_PLUGINS") == "0" {
		return nil
	}
	base := filepath.Base(bin)
	if base == "nvim" || base == "vim" {
		return []string{"--noplugin", "-c", "syntax on", "-c", "filetype on"}
	}
	return nil
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
		m.infoScroll = 0
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
			r := runner.For(cur.Lang)
			switch cur.Kind {
			case puzzle.KindFix:
				out, runErr := r.RunSnippet(string(solution))
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
				res, err = r.Run(string(solution), cur.TestCode)
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
	case "left", "h":
		m.jumpToParent()
		m.clampScroll()
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
	case "tab", " ":
		m.toggleCollapseAtCursor()
		m.clampScroll()
	case "enter":
		if m.browseIdx < len(m.browseRows) {
			row := m.browseRows[m.browseIdx]
			if row.isHeader {
				m.toggleCollapseAtCursor()
				m.clampScroll()
			} else if !row.isHeader {
				m.current = row.puzzle
				m.hintShown = false
				m.solutionShown = false
				m.result = nil
				m.aiReview = ""
				m.aiErr = ""
				m.aiLoading = false
				m.state = statePuzzleInfo
		m.infoScroll = 0
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
		if m.isRowVisible(next) {
			moved++
		}
	}
	m.clampScroll()
}

func (m Model) handlePuzzleInfoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Scroll keys apply across all puzzle kinds. Handle them before
	// delegating so j/k/pgup/pgdn work whether you're on a code,
	// predict, or quiz puzzle.
	switch msg.String() {
	case "j", "down":
		m.infoScroll++
		m.clampInfoScroll()
		return m, nil
	case "k", "up":
		m.infoScroll--
		m.clampInfoScroll()
		return m, nil
	case "pgdown", "ctrl+d":
		m.infoScroll += m.resultPageStep()
		m.clampInfoScroll()
		return m, nil
	case "pgup", "ctrl+u":
		m.infoScroll -= m.resultPageStep()
		m.clampInfoScroll()
		return m, nil
	case "g", "home":
		m.infoScroll = 0
		return m, nil
	case "G", "end":
		m.infoScroll = 1 << 30
		m.clampInfoScroll()
		return m, nil
	}
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
		src, err := m.ensureScratch(m.hintShown)
		if err != nil {
			return m, nil
		}
		switch src {
		case "template":
			m.flash = "scratch rewritten from template"
		case "solution":
			m.flash = "scratch restored from your saved solution"
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
	m.resultScroll = 0
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
		m.infoScroll = 0
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
// and routes to stateResult with a synthetic runner.Result. Comparison is
// whitespace-tolerant — a missing space after a comma shouldn't fail you
// when you got the lesson right. See normalizePrediction for the rules.
// On FAIL we deliberately do NOT print the expected output — that would
// spoil the puzzle. The user can press s to reveal it explicitly.
func (m Model) submitPredictAnswer() (tea.Model, tea.Cmd) {
	got := strings.TrimRight(m.answerInput, " \t\n")
	want := strings.TrimRight(m.current.ExpectedOutput, " \t\n")
	passed := normalizePrediction(got) == normalizePrediction(want)
	m.answerOK = passed
	m.userSolution = m.answerInput
	output := ""
	if !passed {
		shown := got
		if shown == "" {
			shown = "(empty)"
		}
		output = "Your prediction did not match the snippet's output.\n\nYou typed:\n" + shown + "\n\nPress s to reveal the expected output, or enter to try again."
	}
	m.result = &runner.Result{Passed: passed, Output: output}
	if passed {
		m.progress.RecordAttempt(m.current.ID, true)
		_ = m.progress.Save()
	}
	m.state = stateResult
	m.resultScroll = 0
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
		m.infoScroll = 0
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
	case "?":
		m.showHelp = true
	case "b", "esc":
		m.state = stateBrowse
	case "q", "ctrl+q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.resultScroll++
		m.clampResultScroll()
	case "k", "up":
		m.resultScroll--
		m.clampResultScroll()
	case "pgdown", "ctrl+d":
		m.resultScroll += m.resultPageStep()
		m.clampResultScroll()
	case "pgup", "ctrl+u":
		m.resultScroll -= m.resultPageStep()
		m.clampResultScroll()
	case "g", "home":
		m.resultScroll = 0
	case "G", "end":
		m.resultScroll = 1 << 30
		m.clampResultScroll()
	}
	return m, nil
}

// resultPageStep is the number of lines to scroll for pgup/pgdown on
// the result screen. About 80% of the visible area, mirroring less(1).
func (m Model) resultPageStep() int {
	step := (m.height - 4) * 4 / 5
	if step < 1 {
		step = 10
	}
	return step
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
			Language:    puzzle.DisplayLanguage(cur.Lang),
			Source:      puzzle.DisplaySource(cur.Source),
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
			failure = cleanTestOutput(failure, cur.Lang)
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		hint, err := ai.Hint(ctx, ai.HintRequest{
			Language:    puzzle.DisplayLanguage(cur.Lang),
			Source:      puzzle.DisplaySource(cur.Source),
			Title:       cur.Title,
			Description: cur.Description,
			UserCode:    user,
			Failure:     failure,
		})
		return aiReviewMsg{review: hint, err: err}
	}
}

// advanceToNextUnsolved moves to the next puzzle in browse order that hasn't
// been solved yet. Walks STRICTLY FORWARD from the current puzzle's position
// — never jumps back to an earlier unsolved item in another source — and
// stays within the current language. Returns false if nothing unsolved
// remains ahead.
func (m *Model) advanceToNextUnsolved() bool {
	// Walk full puzzle list, not filtered browseRows, so an active search
	// can't strand the user.
	var ordered []*puzzle.Puzzle
	ordered = append(ordered, m.puzzles...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Lang != ordered[j].Lang {
			return ordered[i].Lang < ordered[j].Lang
		}
		if ordered[i].Source != ordered[j].Source {
			return ordered[i].Source < ordered[j].Source
		}
		if ordered[i].Section != ordered[j].Section {
			return ordered[i].Section < ordered[j].Section
		}
		// Stem is the filename prefix (e.g. "006_map_write") and is
		// what the browse view sorts by — using ID here was a bug
		// that decoupled "next" from the visible order.
		return ordered[i].Stem < ordered[j].Stem
	})

	curID := ""
	curLang := ""
	if m.current != nil {
		curID = m.current.ID
		curLang = m.current.Lang
	}
	// Find current position. start = 0 if no current (first run).
	start := 0
	for i, p := range ordered {
		if p.ID == curID {
			start = i + 1
			break
		}
	}
	for _, p := range ordered[start:] {
		if curLang != "" && p.Lang != curLang {
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
		m.infoScroll = 0
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
			return m.browseRows[i].sourceName
		}
	}
	return ""
}

func (m Model) languageForRow(idx int) string {
	for i := idx; i >= 0; i-- {
		if m.browseRows[i].isLanguage {
			return m.browseRows[i].language
		}
	}
	return ""
}

// collapseKeyForRow returns the map key that toggling tab/enter on this
// row should flip. For headers it's the matching ancestor (lang/src/sec).
// For puzzle rows, tab folds the puzzle's section.
func (m Model) collapseKeyForRow(idx int) string {
	if idx < 0 || idx >= len(m.browseRows) {
		return ""
	}
	row := m.browseRows[idx]
	switch {
	case row.isLanguage:
		return "lang:" + row.language
	case row.isSource:
		return "src:" + row.sourceName
	case row.isHeader: // section header
		return "sec:" + row.sectionPath
	case row.puzzle != nil:
		return "sec:" + row.puzzle.Source + "/" + row.puzzle.Section
	}
	return ""
}

// jumpToParent moves the cursor up to the nearest enclosing header.
// Puzzle -> its section. Section -> its source. Source -> its language.
// Language -> no-op (already at the top).
func (m *Model) jumpToParent() {
	if m.browseIdx <= 0 || m.browseIdx >= len(m.browseRows) {
		return
	}
	row := m.browseRows[m.browseIdx]
	var stopOn func(browseRow) bool
	switch {
	case row.puzzle != nil:
		stopOn = func(r browseRow) bool { return r.isHeader && !r.isSource && !r.isLanguage }
	case row.isHeader && !row.isSource && !row.isLanguage: // section header
		stopOn = func(r browseRow) bool { return r.isSource }
	case row.isSource:
		stopOn = func(r browseRow) bool { return r.isLanguage }
	case row.isLanguage:
		return
	default:
		return
	}
	for i := m.browseIdx - 1; i >= 0; i-- {
		if stopOn(m.browseRows[i]) {
			m.browseIdx = i
			return
		}
	}
}

// toggleCollapseAtCursor flips the collapse state for whatever container
// the highlighted row implies, then nudges the cursor up to the nearest
// still-visible row if the highlighted row got hidden.
func (m *Model) toggleCollapseAtCursor() {
	key := m.collapseKeyForRow(m.browseIdx)
	if key == "" {
		return
	}
	m.collapsed[key] = !m.collapsed[key]
	if !m.isRowVisible(m.browseIdx) {
		for i := m.browseIdx; i >= 0; i-- {
			if m.isRowVisible(i) {
				m.browseIdx = i
				return
			}
		}
	}
}

// isRowVisible returns true when the row is currently displayed. A row
// is hidden if any of its ancestor collapse keys are set: language >
// source > section.
func (m Model) isRowVisible(idx int) bool {
	if idx < 0 || idx >= len(m.browseRows) {
		return false
	}
	row := m.browseRows[idx]

	// Language banner is the top — always visible.
	if row.isLanguage {
		return true
	}

	// Check language collapse first.
	if lang := m.languageForRow(idx); lang != "" && m.collapsed["lang:"+lang] {
		return false
	}

	// Source headers visible iff language not collapsed (checked above).
	if row.isSource {
		return true
	}

	// Check source collapse.
	if src := m.sourceForRow(idx); src != "" && m.collapsed["src:"+src] {
		return false
	}

	// Section headers visible iff source not collapsed.
	if row.isHeader {
		return true
	}

	// Puzzle rows: also subject to section collapse.
	if row.puzzle != nil {
		secKey := "sec:" + row.puzzle.Source + "/" + row.puzzle.Section
		if m.collapsed[secKey] {
			return false
		}
	}
	return true
}

func (m Model) viewBrowse() string {
	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s",
		styleTitle.Render("puzzles"),
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
	seenLanguage := false
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
		case row.isLanguage:
			// Visual break between language groups (skip before the
			// very first one — no leading blank line).
			if seenLanguage {
				lines = append(lines, "")
			}
			seenLanguage = true
			lines = append(lines, m.renderLanguageRow(i, row))
		case row.isSource:
			lines = append(lines, m.renderSourceRow(i, row))
		case row.isHeader:
			lines = append(lines, m.renderSectionRow(i, row))
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
				styleKeyName.Render("←") + " parent  " +
				styleKeyName.Render("enter") + " open  " +
				styleKeyName.Render("space") + " collapse  " +
				styleKeyName.Render("/") + " search  " +
				styleKeyName.Render("u") + " toggle solved  " +
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

func (m Model) renderLanguageRow(idx int, row browseRow) string {
	solved, total := m.languageProgress(row.language)
	arrow := "▼"
	if m.collapsed["lang:"+row.language] {
		arrow = "▶"
	}
	cursor := "  "
	nameStyle := styleTitle
	if idx == m.browseIdx {
		cursor = styleKeyName.Render("▶ ")
		nameStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	}
	return fmt.Sprintf("  %s%s %s  %s", cursor, arrow, nameStyle.Render(row.headerTxt), progressBadge(solved, total))
}

func (m Model) renderSourceRow(idx int, row browseRow) string {
	arrow := "▼"
	if m.collapsed["src:"+row.sourceName] {
		arrow = "▶"
	}
	cursor := "  "
	// Source is the middle tier — give it a distinct accent
	// (bold purple) so it doesn't blur into either the language
	// banner above (bold blue) or the section row below (muted).
	nameStyle := styleConcept
	if idx == m.browseIdx {
		cursor = styleKeyName.Render("▶ ")
		nameStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	}
	solved, total := m.sourceProgress(row.sourceName)
	return fmt.Sprintf("    %s%s %s  %s", cursor, arrow, nameStyle.Render(row.headerTxt), progressBadge(solved, total))
}

func (m Model) renderSectionRow(idx int, row browseRow) string {
	arrow := "▽"
	if m.collapsed["sec:"+row.sectionPath] {
		arrow = "▷"
	}
	cursor := "  "
	// Section is the deepest header tier — render quietly (no bold,
	// muted gray) so it reads as a grouping label rather than another
	// peer of source. Hollow arrows reinforce the "lighter" feel.
	nameStyle := styleOutput
	if idx == m.browseIdx {
		cursor = styleKeyName.Render("▶ ")
		nameStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
	}
	solved, total := m.sectionProgress(row.sectionPath)
	return fmt.Sprintf("      %s%s %s  %s", cursor, arrow, nameStyle.Render(row.headerTxt), progressBadge(solved, total))
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
	// Indent puzzles deeper than their section header (8 vs 6
	// spaces) so the section header reads as the parent.
	return fmt.Sprintf("        %s%s%s  %s  %s",
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

func (m Model) languageProgress(lang string) (int, int) {
	total := m.languageTotals[lang]
	solved := 0
	for _, p := range m.puzzles {
		if p.Lang == lang && m.progress.Solved[p.ID] {
			solved++
		}
	}
	return solved, total
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
	content := m.viewPuzzleInfoContent()
	if content == "" {
		return ""
	}
	return m.applyInfoScroll(content)
}

// viewPuzzleInfoContent renders the full puzzle-info screen without
// applying scroll. Used by viewPuzzleInfo (display) and by
// clampInfoScroll (line counting). Branches on Kind to delegate to
// per-kind renderers; code/fix uses the body below.
func (m Model) viewPuzzleInfoContent() string {
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
		styleTitle.Render("puzzles"),
		styleConcept.Render(m.current.Source),
		styleConcept.Render(m.current.Section),
		solvedBadge,
	))

	title := styleTitle.Render(m.current.Title)
	desc := renderProseWithCode(m.current.Description, codeLang(m.current), "  ", m.width-4, styleDescription)

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
			parts = append(parts, "\n"+renderCodeBlock(m.current.Solution, m.current.Lang))
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
		styleTitle.Render("puzzles"),
		styleConcept.Render(m.current.Source),
		styleConcept.Render(m.current.Section),
		solvedBadge,
		styleHint.Render("predict-output"),
	))

	title := styleTitle.Render(m.current.Title)
	desc := renderProseWithCode(m.current.Description, codeLang(m.current), "  ", m.width-4, styleDescription)
	snippet := renderCodeBlock(strings.TrimRight(m.current.Snippet, "\n"), m.current.Lang)

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
			renderCodeBlock(m.current.ExpectedOutput, ""))
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
		styleTitle.Render("puzzles"),
		styleConcept.Render(m.current.Source),
		styleConcept.Render(m.current.Section),
		solvedBadge,
		styleHint.Render("quiz"),
	))
	title := styleTitle.Render(m.current.Title)
	desc := renderProseWithCode(m.current.Description, codeLang(m.current), "  ", m.width-4, styleDescription)
	question := renderProseWithCode(m.current.Question, codeLang(m.current), "  ", m.width-4, styleDescription)

	letters := []string{"a", "b", "c", "d"}
	var choiceLines []string
	const choicePrefixWidth = 8 // "    a)  " visual width
	indent := strings.Repeat(" ", choicePrefixWidth)
	for i, choice := range m.current.Choices {
		if i >= len(letters) {
			break
		}
		// wordWrap operates on raw text (not yet rendered with ANSI),
		// then renderInline applies code-span styling per line.
		wrapped := wordWrap(choice, m.width-choicePrefixWidth)
		parts := strings.Split(wrapped, "\n  ")
		built := "    " + styleKeyName.Render(letters[i]) + ")  " +
			renderInline(parts[0], styleDescription)
		for _, p := range parts[1:] {
			built += "\n" + indent + renderInline(p, styleDescription)
		}
		choiceLines = append(choiceLines, built)
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
		styleTitle.Render("puzzles"),
		styleConcept.Render(m.current.Title),
	))
	prompt := styleDescription.Render(wordWrap(
		"Type the exact output the snippet would print, then press enter.",
		m.width-4,
	))
	snippet := renderCodeBlock(strings.TrimRight(m.current.Snippet, "\n"), m.current.Lang)
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
	content := m.viewResultContent()
	if content == "" {
		return ""
	}
	return m.applyResultScroll(content)
}

// viewResultContent renders the full result screen without applying
// any scroll window. Used by viewResult (to display) and by
// clampResultScroll (to count lines for clamp bounds).
func (m Model) viewResultContent() string {
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
			display := userTrim
			switch m.current.Kind {
			case puzzle.KindPredictOutput:
				label = "Your prediction:"
			case puzzle.KindQuiz:
				label = "Your answer:"
			case puzzle.KindCode, puzzle.KindFix:
				// Strip the puzzle's instruction header (title /
				// ref / description as comments) so the box shows
				// just the code the user wrote.
				display = stripLeadingComments(userTrim)
			}
			lines = append(lines,
				"  "+styleKeybind.Render(label),
				renderAnswerBox(display, m.current, m.width),
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
			lines = append(lines,
				"  "+styleKeybind.Render(label),
				renderAnswerBox(canonTrim, m.current, m.width),
				"",
			)
		}
		if strings.TrimSpace(m.current.Explanation) != "" {
			lines = append(lines, renderProseWithCode(m.current.Explanation, codeLang(m.current), "  ", m.width-4, styleExplanation), "")
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
			output = cleanTestOutput(output, m.current.Lang)
		}
		outputBox := styleBorder.Render(styleOutput.Render(truncate(output, 30)))
		lines = append(lines,
			styleFail.Render("  FAIL ✗"),
			"",
			outputBox,
			"",
			"  "+styleHint.Render("It'll be frustrating at first. But if you keep trying,"),
			"  "+styleHint.Render("you'll get it — and it'll feel amazing!"),
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
				lines = append(lines, "", renderCodeBlock(shown, codeLang(m.current)))
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

	return strings.Join(lines, "\n")
}

// applyInfoScroll wraps the puzzle-info screen content with a scroll
// window matching the terminal height. Same shape as applyResultScroll.
func (m Model) applyInfoScroll(content string) string {
	all := strings.Split(content, "\n")
	available := m.resultViewportLines()
	if available <= 0 || len(all) <= available {
		return "\n" + content
	}
	scroll := m.infoScroll
	if scroll < 0 {
		scroll = 0
	}
	maxScroll := len(all) - available
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + available
	visible := all[scroll:end]

	var indicator string
	switch {
	case scroll == 0:
		indicator = fmt.Sprintf("↓ %d more lines — j/↓ scroll, pgdn page, G end", len(all)-end)
	case scroll == maxScroll:
		indicator = fmt.Sprintf("↑ %d above — k/↑ scroll, pgup page, g top", scroll)
	default:
		indicator = fmt.Sprintf("↑ %d above · ↓ %d below — j/k scroll, pgup/pgdn page", scroll, len(all)-end)
	}
	return "\n" + strings.Join(visible, "\n") + "\n  " + styleHint.Render(indicator)
}

// clampInfoScroll keeps m.infoScroll in range after key handlers
// mutate it (mirrors clampResultScroll).
func (m *Model) clampInfoScroll() {
	if m.infoScroll < 0 {
		m.infoScroll = 0
		return
	}
	available := m.resultViewportLines()
	if available <= 0 {
		m.infoScroll = 0
		return
	}
	total := strings.Count(m.viewPuzzleInfoContent(), "\n") + 1
	maxScroll := total - available
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.infoScroll > maxScroll {
		m.infoScroll = maxScroll
	}
}

// applyResultScroll takes the fully-rendered result-screen content and
// returns a viewport-sized slice based on the (already-clamped) scroll
// offset. Appends a status line indicator when there's content above
// or below the viewport. Read-only — clamping happens in the key
// handler (clampResultScroll).
func (m Model) applyResultScroll(content string) string {
	all := strings.Split(content, "\n")
	available := m.resultViewportLines()
	if available <= 0 || len(all) <= available {
		return "\n" + content
	}
	scroll := m.resultScroll
	if scroll < 0 {
		scroll = 0
	}
	maxScroll := len(all) - available
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + available
	visible := all[scroll:end]

	var indicator string
	switch {
	case scroll == 0:
		indicator = fmt.Sprintf("↓ %d more lines — j/↓ scroll, pgdn page, G end", len(all)-end)
	case scroll == maxScroll:
		indicator = fmt.Sprintf("↑ %d above — k/↑ scroll, pgup page, g top", scroll)
	default:
		indicator = fmt.Sprintf("↑ %d above · ↓ %d below — j/k scroll, pgup/pgdn page", scroll, len(all)-end)
	}
	return "\n" + strings.Join(visible, "\n") + "\n  " + styleHint.Render(indicator)
}

// resultViewportLines is the number of lines available for content on
// the result screen (excluding the indicator line + blank padding).
func (m Model) resultViewportLines() int {
	// Reserve 2 lines for the indicator + blank above it.
	avail := m.height - 2
	if avail < 5 {
		return 0
	}
	return avail
}

// clampResultScroll keeps m.resultScroll in range. Call after any
// mutation that might leave it out of bounds (notably the "G"/"end"
// shortcut which sets it to a deliberate sentinel).
func (m *Model) clampResultScroll() {
	if m.resultScroll < 0 {
		m.resultScroll = 0
		return
	}
	available := m.resultViewportLines()
	if available <= 0 {
		m.resultScroll = 0
		return
	}
	total := strings.Count(m.viewResultContent(), "\n") + 1
	maxScroll := total - available
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.resultScroll > maxScroll {
		m.resultScroll = maxScroll
	}
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
		{"←  h", "jump up to parent (puzzle → section → source → language)"},
		{"g  G", "first / last puzzle"},
		{"pgup  pgdn", "page up / down"},
		{"enter", "open puzzle (or toggle a collapsed section/source/language)"},
		{"space  tab", "toggle collapse on the highlighted row's container"},
		{"/", "search puzzles"},
		{"u", "toggle solved on highlighted puzzle"},
		{"", ""},
		{"Puzzle & result", ""},
		{"enter", "open editor · retry on fail · next on pass"},
		{"e", "re-open editor on PASS (code · fix)"},
		{"a", "AI review on PASS, AI hint on FAIL (code/fix; needs ANTHROPIC_API_KEY)"},
		{"h", "show hint"},
		{"s", "show suggested solution"},
		{"o", "open reference URL in browser"},
		{"r", "reset scratch file to template"},
		{"b  esc", "back to browser"},
		{"", ""},
		{"Anywhere", ""},
		{"?", "this help"},
		{"q  ctrl+c", "quit"},
	}

	var b strings.Builder
	b.WriteString(styleHeader.Render(styleTitle.Render("puzzles — keys")))
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

// renderAnswerBox formats a user's answer or the canonical answer for
// the result screen. Quiz answers are prose (often a full sentence)
// and must wrap to terminal width; predict/code/fix answers stay as
// chroma-highlighted code blocks since they're either values or
// source.
func renderAnswerBox(text string, p *puzzle.Puzzle, termWidth int) string {
	if p.Kind == puzzle.KindQuiz {
		// border eats ~4 cols, leading "  " indent eats 2 — leave room
		wrapped := wordWrap(text, termWidth-6)
		return styleBorder.Render(renderInline(wrapped, styleDescription))
	}
	return renderCodeBlock(text, codeLang(p))
}

// renderProseWithCode formats text that mixes prose paragraphs with
// markdown-style code blocks (paragraphs whose every non-empty line
// starts with 4+ spaces of indentation). Code blocks are dedented
// and syntax-highlighted as `lang`; prose paragraphs are word-wrapped
// to fit the terminal and rendered with the given base style (with
// inline `code` spans). Each prose line is prefixed by linePrefix
// (typically a couple of spaces to align with the surrounding indent).
func renderProseWithCode(text, lang, linePrefix string, width int, base lipgloss.Style) string {
	paragraphs := splitOnBlankLines(strings.TrimRight(text, "\n"))
	out := make([]string, 0, len(paragraphs))
	for _, p := range paragraphs {
		if isCodeBlock(p) {
			out = append(out, renderCodeBlock(dedent(p), lang))
			continue
		}
		wrapped := wordWrap(p, width-len(linePrefix))
		lines := strings.Split(wrapped, "\n  ")
		for i, line := range lines {
			lines[i] = linePrefix + line
		}
		out = append(out, renderInline(strings.Join(lines, "\n"), base))
	}
	return strings.Join(out, "\n")
}

// splitOnBlankLines groups consecutive non-blank lines into paragraphs.
func splitOnBlankLines(text string) []string {
	var paragraphs []string
	var current []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			if len(current) > 0 {
				paragraphs = append(paragraphs, strings.Join(current, "\n"))
				current = nil
			}
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		paragraphs = append(paragraphs, strings.Join(current, "\n"))
	}
	return paragraphs
}

// isCodeBlock reports whether every non-empty line in p starts with
// 4 or more leading spaces — the markdown indented-code-block convention.
func isCodeBlock(p string) bool {
	for _, line := range strings.Split(p, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "    ") {
			return false
		}
	}
	return true
}

// dedent strips the longest common leading-whitespace prefix from
// every non-empty line in p.
func dedent(p string) string {
	lines := strings.Split(p, "\n")
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := 0
		for n < len(line) && line[n] == ' ' {
			n++
		}
		if minIndent == -1 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent <= 0 {
		return p
	}
	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}
	return strings.Join(lines, "\n")
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
