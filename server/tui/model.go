// Package tui is Sulfur's operator console: a bubbletea v2 Model (a scrolling log
// viewport + a command input line) plus the slog.Handler bridge that fans log
// records out to stderr (always) and into the TUI (non-blocking, TTY mode only).
//
// This package is OPERATOR TOOLING, isolated from the gameplay packages — the
// CLAUDE.md 1:1-vanilla-jar mandate does NOT bind here (there is no vanilla
// equivalent). The binding constraints are: CGO_ENABLED=0 stays clean (pure-Go
// static binary) and `go test -race` stays green (the bridge crosses goroutines).
package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
)

// inputHeight is the number of terminal rows reserved for the command input line
// below the log viewport.
const inputHeight = 1

// logLineMsg is the external message the slog bridge Sends into the program. The
// handler (in this same package) constructs it from a formatted log record.
type logLineMsg string

// commandFn routes a typed console line to the server. It is invoked from the
// bubbletea goroutine on Enter; the implementation MUST be non-blocking and hand
// the line to the tick via a message channel (see Plan 02), never execute inline.
type commandFn func(line string)

// Model is the single bubbletea Model holding the log viewport and the command
// input line. All Model state is touched ONLY inside Update (bubbletea runs Update
// single-threaded), so the log bridge never reaches into these fields directly —
// it crosses the goroutine boundary via Program.Send (a logLineMsg).
type Model struct {
	vp       viewport.Model
	input    textinput.Model
	lines    []string
	dispatch commandFn
	ready    bool
}

// New builds an operator-console Model. dispatch routes Enter'd lines to the
// server (may be nil in tests / degraded modes).
func New(dispatch commandFn) Model {
	ti := textinput.New()
	ti.Placeholder = "type a command…"
	ti.Focus()
	return Model{input: ti, dispatch: dispatch}
}

// Init starts the input cursor blink.
func (m Model) Init() tea.Cmd { return textinput.Blink }

// Update handles three external message classes — window resize, key presses
// (Enter dispatch + ctrl+c quit), and logLineMsg (append to the viewport) — then
// forwards every message to the input + viewport sub-models.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if !m.ready {
			m.vp = viewport.New(
				viewport.WithWidth(msg.Width),
				viewport.WithHeight(msg.Height-inputHeight),
			)
			m.ready = true
		} else {
			m.vp.SetWidth(msg.Width)
			m.vp.SetHeight(msg.Height - inputHeight)
		}
		m.input.SetWidth(msg.Width)

	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			line := m.input.Value()
			m.input.SetValue("")
			if line != "" && m.dispatch != nil {
				m.dispatch(line) // → tick message; the reply returns as a logLineMsg
			}
		}

	case logLineMsg:
		m.lines = append(m.lines, string(msg))
		if m.ready {
			m.vp.SetContent(lipgloss.JoinVertical(lipgloss.Left, m.lines...))
			m.vp.GotoBottom()
		}
	}

	var c tea.Cmd
	m.input, c = m.input.Update(msg)
	cmds = append(cmds, c)
	m.vp, c = m.vp.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

// View joins the log viewport above the command input line. The alt-screen flag
// is declarative on tea.View in v2 (NOT a NewProgram option).
func (m Model) View() tea.View {
	body := lipgloss.JoinVertical(lipgloss.Left, m.vp.View(), m.input.View())
	v := tea.NewView(body)
	v.AltScreen = true
	return v
}
