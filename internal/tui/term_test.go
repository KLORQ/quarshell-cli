package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

type testControl struct{ items []Sandbox }

func (c testControl) ListSandboxes(context.Context) ([]Sandbox, error) { return c.items, nil }
func (testControl) LogSources() []string                               { return []string{"all", "sandbox", "proxy"} }
func (testControl) Logs(_ context.Context, _ LogOptions, out io.Writer) error {
	_, err := io.WriteString(out, "safe-log-line\n")
	return err
}

func TestSanitizeTerminalText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain unicode", in: "sandbox-пример 🐳", want: "sandbox-пример 🐳"},
		{name: "CSI color", in: "\x1b[31mDENIED\x1b[0m request", want: "DENIED request"},
		{name: "OSC title", in: "before\x1b]0;owned title\x07after", want: "beforeafter"},
		{name: "OSC hyperlink", in: "\x1b]8;;https://example.test\x1b\\click\x1b]8;;\x1b\\", want: "click"},
		{name: "DCS payload", in: "before\x1bP1;payload\x1b\\after", want: "beforeafter"},
		{name: "carriage return and bell", in: "one\rtwo\a", want: "onetwo"},
		{name: "tab retained", in: "column1\tcolumn2", want: "column1\tcolumn2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeTerminalText(tt.in); got != tt.want {
				t.Fatalf("sanitizeTerminalText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTruncateTerminalLinePreservesUTF8AndByteLimit(t *testing.T) {
	got := truncateTerminalLine("абвг", 5)
	if len(got) > 5 {
		t.Fatalf("truncated line is %d bytes, want at most 5", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncated line is invalid UTF-8: %q", got)
	}
	if got != "аб" {
		t.Fatalf("truncated line = %q, want %q", got, "аб")
	}
}

func TestLogSelectionTracksRingEvictionAndCopiesRange(t *testing.T) {
	m := newModel(testControl{})
	m.logName = "session-1"
	m.logFollowTail = false
	for i := 0; i < maxLogLines; i++ {
		m.logBuffer.Add(fmt.Sprintf("line-%d", i))
	}
	anchor := 2
	m.logSelectAnchor = &anchor
	m.logCursor = 4
	m.appendLogLine("session-1", "new-line")
	if m.logCursor != 3 || *m.logSelectAnchor != 1 {
		t.Fatalf("selection after ring eviction = (%d, %d), want (3, 1)", m.logCursor, *m.logSelectAnchor)
	}

	copyScreen := tcell.NewSimulationScreen("UTF-8")
	m.screen = copyScreen
	m.logBuffer = logRing{}
	m.logBuffer.Add("first")
	m.logBuffer.Add("second")
	m.logBuffer.Add("third")
	m.logCursor = 1
	anchor = 0
	m.logSelectAnchor = &anchor
	m.copySelectedLogLine()
	if got := string(copyScreen.GetClipboardData()); got != "first\nsecond" {
		t.Fatalf("copied range = %q, want first and second lines", got)
	}
}

func TestLogSourceCyclingAndThemeSelection(t *testing.T) {
	if got := nextLogSource("", []string{"all", "gateway", "sandbox"}); got != "gateway" {
		t.Fatalf("next source = %q, want gateway", got)
	}
	if got := nextLogSource("proxy", []string{"all", "sandbox", "proxy"}); got != "all" {
		t.Fatalf("next source after proxy = %q, want all", got)
	}
	if got := logSourceLabel(""); got != "all" {
		t.Fatalf("empty source label = %q, want all", got)
	}

	t.Setenv("COLORFGBG", "15;0")
	if got, err := themeAccent("auto"); err != nil || got != tcell.ColorAqua {
		t.Fatalf("dark terminal auto theme = (%v, %v), want aqua", got, err)
	}
	t.Setenv("COLORFGBG", "0;15")
	if got, err := themeAccent("auto"); err != nil || got != tcell.ColorDarkBlue {
		t.Fatalf("light terminal auto theme = (%v, %v), want dark blue", got, err)
	}
	if _, err := themeAccent("sepia"); err == nil {
		t.Fatal("unknown theme unexpectedly accepted")
	}
}

func TestLiveLogKeyBindingsPauseSelectCopyAndOpenSnapshot(t *testing.T) {
	m := newModel(testControl{})
	m.viewLogs = true
	m.logName = "session-1"
	m.logFollowTail = false
	m.logBuffer.Add("first")
	m.logBuffer.Add("second")
	m.logBuffer.Add("third")
	screen := tcell.NewSimulationScreen("UTF-8")
	m.screen = screen
	m.ui.SetScreen(screen)
	m.renderLogs()

	m.captureKey(tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone))
	if !m.logsPaused {
		t.Fatal("p did not pause live log display")
	}
	m.captureKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	if m.logCursor != 1 || m.logFollowTail {
		t.Fatalf("j selection cursor=%d follow=%v, want cursor 1 with follow disabled", m.logCursor, m.logFollowTail)
	}
	m.captureKey(tcell.NewEventKey(tcell.KeyRune, 'v', tcell.ModNone))
	m.captureKey(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone))
	if m.logSelectAnchor == nil || *m.logSelectAnchor != 1 || m.logCursor != 2 {
		t.Fatalf("visual range anchor/cursor = %v/%d, want 1/2", m.logSelectAnchor, m.logCursor)
	}
	m.captureKey(tcell.NewEventKey(tcell.KeyRune, 'y', tcell.ModNone))
	if got := string(screen.GetClipboardData()); got != "second\nthird" {
		t.Fatalf("y copied %q, want selected range", got)
	}
	m.captureKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	if !m.viewLogDetail || !strings.Contains(m.details.GetText(true), "third") {
		t.Fatalf("Enter did not open selected-line snapshot: detail=%v text=%q", m.viewLogDetail, m.details.GetText(true))
	}
	m.captureKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if m.viewLogDetail {
		t.Fatal("Escape did not return from the log snapshot")
	}
}

func TestSelectionMovementAndLogBufferBound(t *testing.T) {
	m := newModel(testControl{})
	m.items = []Sandbox{{Name: "one"}, {Name: "two"}}
	m.renderList()
	m.moveCursor(1)
	if got := m.selected().Name; got != "two" {
		t.Fatalf("selected sandbox = %q, want two", got)
	}
	m.moveCursor(10)
	if got := m.selected().Name; got != "two" {
		t.Fatalf("selection should stop at the final item, got %q", got)
	}

	m.logName = "session-1"
	m.appendLogLine("stale-session", "should not appear")
	for i := 0; i < maxLogLines+5; i++ {
		m.appendLogLine("session-1", fmt.Sprintf("line-%d", i))
	}
	lines := m.logBuffer.Snapshot()
	if got := len(lines); got != maxLogLines {
		t.Fatalf("log buffer has %d lines, want %d", got, maxLogLines)
	}
	if got := lines[0]; got != "line-5" {
		t.Fatalf("oldest retained line = %q, want line-5", got)
	}
}

func TestTViewApplicationRendersLogsAndQuits(t *testing.T) {
	m := newModel(testControl{items: []Sandbox{{Name: "alpha", Status: "running", Image: "test"}}})
	m.active.Store(true)
	screen := tcell.NewSimulationScreen("UTF-8")
	m.ui.SetScreen(screen)
	go m.refreshLoop()
	runDone := make(chan error, 1)
	go func() { runDone <- m.ui.Run() }()

	waitModel(t, m, func() bool { return len(m.items) == 1 && m.items[0].Name == "alpha" })
	screen.InjectKey(tcell.KeyRune, 'l', tcell.ModNone)
	waitModel(t, m, func() bool {
		lines := m.logBuffer.Snapshot()
		return m.viewLogs && len(lines) == 1 && lines[0] == "safe-log-line"
	})
	screen.InjectKey(tcell.KeyRune, 'q', tcell.ModNone)
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("TUI Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TUI did not quit after q")
	}
	m.active.Store(false)
	close(m.done)
	m.stopLogs()
}

func waitModel(t *testing.T, m *model, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		matched := false
		m.ui.QueueUpdateDraw(func() { matched = condition() })
		if matched {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("TUI model did not reach the expected state")
}
