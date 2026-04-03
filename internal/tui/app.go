package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klbrg/gopuzzle/internal/progress"
	"github.com/klbrg/gopuzzle/internal/puzzle"
	"github.com/klbrg/gopuzzle/internal/runner"
)

type state int

const (
	stateBrowse state = iota
	statePuzzleInfo
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

// browseRow is one row in the browse list: a source header, section header, or puzzle entry.
type browseRow struct {
	isHeader  bool
	isSource  bool   // top-level source header (e.g. "gobyexample")
	headerTxt string // text for any header row
	puzzle    *puzzle.Puzzle
}

type Model struct {
	state         state
	spinner       spinner.Model
	progress      *progress.Progress
	puzzles       []*puzzle.Puzzle
	browseRows    []browseRow
	browseIdx     int            // cursor index into browseRows
	browseOffset  int            // scroll offset (top visible row index)
	collapsed     map[string]bool // collapsed source headers
	current       *puzzle.Puzzle
	tempFile      string
	result        *runner.Result
	userSolution  string
	hintShown     bool
	solutionShown bool
	width         int
	height        int
}

func New(p *progress.Progress, puzzles []*puzzle.Puzzle) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colorBlue)

	m := Model{
		state:     stateBrowse,
		spinner:   sp,
		progress:  p,
		puzzles:   puzzles,
		collapsed: make(map[string]bool),
		tempFile:  filepath.Join(os.TempDir(), "gopuzzle_solution.go"),
	}
	m.browseRows = buildBrowseRows(puzzles)
	// Place cursor on first unsolved puzzle.
	for i, row := range m.browseRows {
		if !row.isHeader && !p.Solved[row.puzzle.ID] {
			m.browseIdx = i
			break
		}
	}
	return m
}

func buildBrowseRows(puzzles []*puzzle.Puzzle) []browseRow {
	sorted := make([]*puzzle.Puzzle, len(puzzles))
	copy(sorted, puzzles)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Source != sorted[j].Source {
			return sorted[i].Source < sorted[j].Source
		}
		if sorted[i].Section != sorted[j].Section {
			return sorted[i].Section < sorted[j].Section
		}
		return sorted[i].ID < sorted[j].ID
	})

	var rows []browseRow
	lastSource, lastSection := "", ""
	for _, p := range sorted {
		if p.Source != lastSource {
			rows = append(rows, browseRow{isHeader: true, isSource: true, headerTxt: p.Source})
			lastSource = p.Source
			lastSection = ""
		}
		if p.Section != lastSection {
			rows = append(rows, browseRow{isHeader: true, headerTxt: p.Section})
			lastSection = p.Section
		}
		rows = append(rows, browseRow{puzzle: p})
	}
	return rows
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m *Model) writeTempFile(includeHint bool) error {
	// Build description comment block.
	desc := strings.TrimSpace(m.current.Description)
	header := "// " + m.current.Title + "\n"
	for _, line := range strings.Split(desc, "\n") {
		header += "// " + line + "\n"
	}
	if m.current.Reference != "" {
		header += "// ref: " + m.current.Reference + "\n"
	}
	header += "\n"

	content := header + m.current.Template
	if includeHint && m.current.Hint != "" {
		content += "\n// HINT: " + m.current.Hint + "\n"
	}
	return os.WriteFile(m.tempFile, []byte(content), 0644)
}

func (m Model) openVi() tea.Cmd {
	cmd := exec.Command(editorBin(), m.tempFile)
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
		exec.Command("open", url).Start()
	case "linux":
		exec.Command("xdg-open", url).Start()
	case "windows":
		exec.Command("cmd", "/c", "start", url).Start()
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
		m.state = stateRunning
		solution, readErr := os.ReadFile(m.tempFile)
		cur := m.current
		testCode := cur.TestCode
		return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
			if readErr != nil {
				return runResultMsg{err: readErr}
			}
			res, err := runner.Run(string(solution), testCode)
			if err == nil && res.Passed {
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
		if m.state == stateRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

	case stateBrowse:
		switch msg.String() {
		case "up", "k":
			for i := m.browseIdx - 1; i >= 0; i-- {
				if m.isRowVisible(i) && (m.browseRows[i].isSource || !m.browseRows[i].isHeader) {
					m.browseIdx = i
					break
				}
			}
			m.clampScroll()
		case "down", "j":
			for i := m.browseIdx + 1; i < len(m.browseRows); i++ {
				if m.isRowVisible(i) && (m.browseRows[i].isSource || !m.browseRows[i].isHeader) {
					m.browseIdx = i
					break
				}
			}
			m.clampScroll()
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
					m.state = statePuzzleInfo
				}
			}
		case "q", "ctrl+q":
			return m, tea.Quit
		}

	case statePuzzleInfo:
		switch msg.String() {
		case "enter", " ":
			if err := m.writeTempFile(m.hintShown); err != nil {
				return m, nil
			}
			return m, m.openVi()
		case "h":
			if !m.hintShown {
				m.hintShown = true
			}
			return m, nil
		case "s":
			m.solutionShown = true
			return m, nil
		case "o":
			if m.current.Reference != "" {
				openURL(m.current.Reference)
			}
			return m, nil
		case "b", "esc":
			m.state = stateBrowse
		case "q", "ctrl+q":
			return m, tea.Quit
		}

	case stateResult:
		switch msg.String() {
		case "enter", " ":
			if m.result != nil && m.result.Passed {
				m.state = stateBrowse
				return m, nil
			}
			return m, m.openVi()
		case "h":
			if !m.hintShown {
				m.hintShown = true
				_ = m.writeTempFile(true)
			}
			return m, nil
		case "s":
			m.solutionShown = true
			return m, nil
		case "o":
			if m.current.Reference != "" {
				openURL(m.current.Reference)
			}
			return m, nil
		case "b", "esc":
			m.state = stateBrowse
		case "q", "ctrl+q":
			return m, tea.Quit
		}

	case stateDone:
		if msg.String() == "q" || msg.String() == "ctrl+q" {
			return m, tea.Quit
		}
	}

	return m, nil
}

// visibleIndex returns how many visible rows come before row idx.
func (m Model) visibleIndex(idx int) int {
	n := 0
	for i := 0; i < idx; i++ {
		if m.isRowVisible(i) {
			n++
		}
	}
	return n
}

// clampScroll adjusts browseOffset so the cursor stays visible.
func (m *Model) clampScroll() {
	visible := m.height - 6
	if visible < 1 {
		visible = 10
	}
	cursorPos := m.visibleIndex(m.browseIdx)
	if cursorPos < m.browseOffset {
		m.browseOffset = cursorPos
	}
	if cursorPos >= m.browseOffset+visible {
		m.browseOffset = cursorPos - visible + 1
	}
}

func (m Model) View() string {
	switch m.state {
	case stateBrowse:
		return m.viewBrowse()
	case statePuzzleInfo:
		return m.viewPuzzleInfo()
	case stateRunning:
		return fmt.Sprintf("\n\n  %s Compiling and running tests...\n", m.spinner.View())
	case stateResult:
		return m.viewResult()
	case stateDone:
		return stylePass.Render("\n\n  All puzzles completed! Great work.\n\n  Press q to quit.\n")
	}
	return ""
}

// sourceForRow walks backwards to find which source a row belongs to.
func (m Model) sourceForRow(idx int) string {
	for i := idx; i >= 0; i-- {
		if m.browseRows[i].isSource {
			return m.browseRows[i].headerTxt
		}
	}
	return ""
}

// isRowVisible returns false if the row's source is collapsed.
func (m Model) isRowVisible(idx int) bool {
	row := m.browseRows[idx]
	if row.isSource {
		return true // source headers are always visible
	}
	return !m.collapsed[m.sourceForRow(idx)]
}

func (m Model) viewBrowse() string {
	solved := m.progress.TotalSolved
	total := 0
	for _, r := range m.browseRows {
		if !r.isHeader {
			total++
		}
	}

	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s",
		styleTitle.Render("gopuzzle"),
		styleScore.Render(fmt.Sprintf("%d / %d solved", solved, total)),
	))

	visible := m.height - 6
	if visible < 1 {
		visible = 10
	}

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
		if row.isSource {
			arrow := "▼"
			if m.collapsed[row.headerTxt] {
				arrow = "▶"
			}
			cursor := "  "
			style := styleTitle
			if i == m.browseIdx {
				cursor = styleKeyName.Render("▶ ")
				style = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
			}
			lines = append(lines, fmt.Sprintf("  %s%s %s", cursor, arrow, style.Render(row.headerTxt)))
			shown++
			continue
		}
		if row.isHeader {
			lines = append(lines, "      "+styleConcept.Render(row.headerTxt))
			shown++
			continue
		}

		p := row.puzzle
		check := "  "
		if m.progress.Solved[p.ID] {
			check = stylePass.Render("✓ ")
		}

		cursor := "  "
		titleStyle := styleDescription
		if i == m.browseIdx {
			cursor = styleKeyName.Render("▶ ")
			titleStyle = lipgloss.NewStyle().Bold(true).Foreground(colorWhite)
		}

		line := fmt.Sprintf("      %s%s%s  %s  %s",
			cursor,
			check,
			styleOutput.Render(p.ID),
			titleStyle.Render(p.Title),
			styleDifficulty.Render(difficultyStars(p.Difficulty)),
		)
		lines = append(lines, line)
		shown++
	}

	keys := styleKeybind.Render(fmt.Sprintf(
		"%s/%s navigate  %s start  %s quit",
		styleKeyName.Render("↑"),
		styleKeyName.Render("↓"),
		styleKeyName.Render("enter"),
		styleKeyName.Render("q"),
	))

	return header + "\n" + strings.Join(lines, "\n") + "\n\n  " + keys
}

func (m Model) viewPuzzleInfo() string {
	if m.current == nil {
		return ""
	}

	header := styleHeader.Render(fmt.Sprintf(
		"%s  %s / %s  %s",
		styleTitle.Render("gopuzzle"),
		styleConcept.Render(m.current.Source),
		styleConcept.Render(m.current.Section),
		styleDifficulty.Render(difficultyStars(m.current.Difficulty)),
	))

	title := styleTitle.Render(m.current.Title)
	desc := styleDescription.Render(wordWrap(m.current.Description, m.width-4))

	var hintLine string
	if m.hintShown {
		hintLine = "\n" + styleHint.Render("  Hint: "+m.current.Hint)
	}

	keys := styleKeybind.Render(fmt.Sprintf(
		"%s open in %s  %s hint  %s solution  %s ref  %s back  %s quit",
		styleKeyName.Render("enter"),
		editorBin(),
		styleKeyName.Render("h"),
		styleKeyName.Render("s"),
		styleKeyName.Render("o"),
		styleKeyName.Render("b"),
		styleKeyName.Render("q"),
	))

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
			parts = append(parts, "\n"+styleBorder.Render(styleOutput.Render(m.current.Solution)))
		} else {
			parts = append(parts, "\n  "+styleHint.Render("No suggested solution available for this puzzle."))
		}
	}
	parts = append(parts, "", "  "+keys)

	return strings.Join(parts, "\n")
}

func (m Model) viewResult() string {
	if m.result == nil {
		return ""
	}

	var lines []string

	if m.result.Passed {
		lines = append(lines, stylePass.Render("  PASS ✓"), "")
		if m.userSolution != "" {
			lines = append(lines,
				"  "+styleKeybind.Render("Your solution:"),
				styleBorder.Render(styleOutput.Render(strings.TrimSpace(m.userSolution))),
				"",
			)
		}
		if m.current.Solution != "" {
			lines = append(lines,
				"  "+styleKeybind.Render("Suggested solution:"),
				styleBorder.Render(styleOutput.Render(strings.TrimSpace(m.current.Solution))),
				"",
			)
		}
		lines = append(lines,
			"  "+styleExplanation.Render(wordWrap(m.current.Explanation, m.width-4)),
			"",
			"  "+styleKeybind.Render(
				styleKeyName.Render("enter")+" back to browser  "+
					styleKeyName.Render("q")+" quit",
			),
		)
	} else {
		outputBox := styleBorder.Render(styleOutput.Render(truncate(m.result.Output, 30)))
		lines = append(lines,
			styleFail.Render("  FAIL ✗"),
			"",
			outputBox,
		)
		if m.current.Reference != "" {
			lines = append(lines, "  "+styleKeybind.Render("ref: ")+styleKeyName.Render(m.current.Reference))
		}
		if m.hintShown {
			lines = append(lines,
				"",
				styleHint.Render("  Hint: "+m.current.Hint),
			)
		}
		if m.solutionShown {
			if m.current.Solution != "" {
				lines = append(lines, "", styleBorder.Render(styleOutput.Render(m.current.Solution)))
			} else {
				lines = append(lines, "", "  "+styleHint.Render("No suggested solution available for this puzzle."))
			}
		}
		lines = append(lines,
			"",
			"  "+styleKeybind.Render(
				styleKeyName.Render("enter")+" retry in "+editorBin()+
					"  "+styleKeyName.Render("h")+" hint"+
					"  "+styleKeyName.Render("s")+" solution"+
					"  "+styleKeyName.Render("o")+" ref"+
					"  "+styleKeyName.Render("b")+" back"+
					"  "+styleKeyName.Render("q")+" quit",
			),
		)
	}

	return "\n" + strings.Join(lines, "\n")
}

// wordWrap wraps text at the given width, preserving existing newlines.
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

// truncate keeps at most maxLines lines of output.
func truncate(s string, maxLines int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	return strings.Join(lines[:maxLines], "\n") + "\n  ..."
}
