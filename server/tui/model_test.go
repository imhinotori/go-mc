package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// applyMsg drives Update once and type-asserts the result back to the concrete Model
// (Update returns tea.Model; tests are white-box package tui).
func applyMsg(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	m2, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want tui.Model", next)
	}
	return m2
}

func readyModel(t *testing.T, dispatch commandFn) Model {
	t.Helper()
	m := New(dispatch)
	// A WindowSizeMsg makes the viewport ready and sizes both panes.
	return applyMsg(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
}

func TestModelAppendsLog(t *testing.T) {
	m := readyModel(t, nil)
	m = applyMsg(t, m, logLineMsg("hello"))

	if got := strings.Join(m.lines, "\n"); !strings.Contains(got, "hello") {
		t.Fatalf("m.lines does not contain %q: %q", "hello", got)
	}
	if !strings.Contains(m.vp.View(), "hello") {
		t.Fatalf("viewport content does not contain %q: %q", "hello", m.vp.View())
	}
}

func TestEnterDispatches(t *testing.T) {
	var got []string
	dispatch := func(line string) { got = append(got, line) }

	m := readyModel(t, dispatch)
	m.input.SetValue("say hi")
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(got) != 1 {
		t.Fatalf("dispatch called %d times, want 1 (%v)", len(got), got)
	}
	if got[0] != "say hi" {
		t.Fatalf("dispatch line = %q, want %q", got[0], "say hi")
	}
	if m.input.Value() != "" {
		t.Fatalf("input not cleared after enter: %q", m.input.Value())
	}
}

func TestEnterEmptyNoDispatch(t *testing.T) {
	called := 0
	dispatch := func(string) { called++ }

	m := readyModel(t, dispatch)
	// input is empty
	m = applyMsg(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	if called != 0 {
		t.Fatalf("dispatch called %d times on empty input, want 0", called)
	}
}

func TestResize(t *testing.T) {
	m := readyModel(t, nil) // 80x24 already applied
	if !m.ready {
		t.Fatal("model not ready after WindowSizeMsg")
	}
	// One line is reserved for the input row.
	if got := m.vp.Height(); got != 24-1 {
		t.Fatalf("viewport height = %d, want %d", got, 24-1)
	}
	if got := m.vp.Width(); got != 80 {
		t.Fatalf("viewport width = %d, want %d", got, 80)
	}

	// A subsequent resize re-sizes (not re-creates) the viewport.
	m = applyMsg(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	if got := m.vp.Height(); got != 40-1 {
		t.Fatalf("viewport height after resize = %d, want %d", got, 40-1)
	}
	if got := m.vp.Width(); got != 100 {
		t.Fatalf("viewport width after resize = %d, want %d", got, 100)
	}
}
