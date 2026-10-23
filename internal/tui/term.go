// Package tui implements `quarshell term` — a sandbox browser with live logs.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	maxLogLines       = 400
	maxLogLineBytes   = 16 * 1024
	logBatchLines     = 64
	logIngressLines   = 128
	logRenderInterval = 50 * time.Millisecond
	refreshEvery      = 2 * time.Second
)

// Action is a post-TUI command.
type Action struct {
	Kind string // connect | exec | start | stop | delete | logs | ""
	Name string
}

type logErrMsg struct {
	name string
	err  error
}

type logDoneMsg struct{ name string }
type logBatchMsg struct {
	name  string
	lines []string
}

type logRing struct {
	lines []string
	start int
	count int
}

func (b *logRing) Add(line string) {
	if len(b.lines) == 0 {
		b.lines = make([]string, maxLogLines)
	}
	if b.count < len(b.lines) {
		index := (b.start + b.count) % len(b.lines)
		b.lines[index] = line
		b.count++
		return
	}
	b.lines[b.start] = line
	b.start = (b.start + 1) % len(b.lines)
}

func (b *logRing) Snapshot() []string {
	result := make([]string, b.count)
	for i := range result {
		result[i] = b.lines[(b.start+i)%len(b.lines)]
	}
	return result
}

type model struct {
	control         Control
	accent          tcell.Color
	ui              *tview.Application
	screen          tcell.Screen
	root            *tview.Flex
	table           *tview.Table
	details         *tview.TextView
	logs            *tview.TextView
	status          *tview.TextView
	items           []Sandbox
	hasLoaded       bool
	cursor          int
	err             string
	action          Action
	viewLogs        bool
	viewLogDetail   bool
	viewDetails     bool
	filterInput     bool
	filter          string
	confirmDelete   bool
	logName         string
	logBuffer       logRing
	logErr          string
	logsPaused      bool
	logSource       string
	logSources      []string
	logCursor       int
	logFollowTail   bool
	logSelectAnchor *int
	logDetailText   string
	logDetailLine   int
	logDetailSize   int
	logCancel       context.CancelFunc
	active          atomic.Bool
	refreshing      atomic.Bool
	done            chan struct{}
}

// Run starts the interactive terminal UI and returns the chosen action (if any).
func Run(control Control) (Action, error) {
	return RunWithTheme(control, "auto")
}

// RunWithTheme starts the TUI using the requested auto, dark, or light theme.
func RunWithTheme(control Control, theme string) (Action, error) {
	accent, err := themeAccent(theme)
	if err != nil {
		return Action{}, err
	}
	m := newThemedModel(control, accent)
	m.active.Store(true)
	go m.refreshLoop()
	if err := m.ui.Run(); err != nil {
		m.active.Store(false)
		close(m.done)
		m.stopLogs()
		return Action{}, err
	}
	m.active.Store(false)
	close(m.done)
	m.stopLogs()
	return m.action, nil
}

func newModel(control Control) *model {
	accent, _ := themeAccent("auto")
	return newThemedModel(control, accent)
}

func newThemedModel(control Control, accent tcell.Color) *model {
	m := &model{
		control: control,
		accent:  accent,
		ui:      tview.NewApplication(),
		table:   tview.NewTable().SetSelectable(true, false).SetFixed(1, 0),
		details: tview.NewTextView().SetDynamicColors(false).SetScrollable(true),
		logs:    tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWordWrap(false),
		status:  tview.NewTextView().SetDynamicColors(false),
		done:    make(chan struct{}),
	}
	m.logSources = append([]string(nil), control.LogSources()...)
	if len(m.logSources) == 0 {
		m.logSources = []string{"all"}
	}
	m.table.SetBorder(true).SetTitle("Sandboxes")
	m.logs.SetBorder(true).SetTitle("Sandbox logs")
	m.status.SetText("Loading sandboxes…")
	for i, label := range []string{"NAME", "STATUS", "IMAGE"} {
		m.table.SetCell(0, i, tview.NewTableCell(label).SetSelectable(false).SetTextColor(accent).SetAttributes(tcell.AttrBold))
	}
	m.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(m.table, 0, 1, true).
		AddItem(m.status, 1, 0, false)
	m.ui.SetRoot(m.root, true).EnableMouse(true)
	m.ui.SetAfterDrawFunc(func(screen tcell.Screen) { m.screen = screen })
	m.ui.SetInputCapture(m.captureKey)
	m.renderList()
	return m
}

func themeAccent(mode string) (tcell.Color, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "dark":
		return tcell.ColorAqua, nil
	case "light":
		return tcell.ColorDarkBlue, nil
	case "", "auto":
		parts := strings.Split(os.Getenv("COLORFGBG"), ";")
		if len(parts) > 0 {
			background, err := strconv.Atoi(parts[len(parts)-1])
			if err == nil && background >= 8 {
				return tcell.ColorDarkBlue, nil
			}
		}
		return tcell.ColorAqua, nil
	default:
		return tcell.ColorDefault, fmt.Errorf("unknown theme mode %q (expected auto, dark, or light)", mode)
	}
}

func (m *model) captureKey(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		m.stopLogs()
		m.ui.Stop()
		return nil
	}
	if m.confirmDelete {
		if event.Key() == tcell.KeyEscape {
			m.confirmDelete = false
			m.renderList()
			return nil
		}
		return event
	}
	if m.filterInput {
		switch event.Key() {
		case tcell.KeyEscape:
			m.filterInput = false
			m.filter = ""
			m.cursor = 0
			m.renderList()
			return nil
		case tcell.KeyEnter:
			m.filterInput = false
			m.renderList()
			return nil
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if m.filter != "" {
				_, size := utf8.DecodeLastRuneInString(m.filter)
				m.filter = m.filter[:len(m.filter)-size]
				m.cursor = 0
				m.renderList()
			}
			return nil
		case tcell.KeyRune:
			m.filter += string(event.Rune())
			m.cursor = 0
			m.renderList()
			return nil
		default:
			return nil
		}
	}
	if m.viewLogs {
		if m.viewLogDetail {
			if event.Key() == tcell.KeyEscape {
				m.viewLogDetail = false
				m.renderLogs()
				return nil
			}
			if event.Key() == tcell.KeyRune && event.Rune() == 'q' {
				m.stopLogs()
				m.ui.Stop()
				return nil
			}
			return event
		}
		switch event.Key() {
		case tcell.KeyEscape:
			m.stopLogs()
			m.viewLogs = false
			m.renderList()
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q':
				m.stopLogs()
				m.ui.Stop()
				return nil
			case 'r':
				m.restartLogs()
				return nil
			case 'p':
				m.logsPaused = !m.logsPaused
				m.renderLogs()
				return nil
			case 's':
				m.logSource = nextLogSource(m.logSource, m.logSources)
				m.startLogs(m.logName)
				return nil
			case 'j':
				m.moveLogCursor(1)
				return nil
			case 'k':
				m.moveLogCursor(-1)
				return nil
			case 'g':
				m.moveLogCursorTo(0)
				return nil
			case 'G':
				m.moveLogCursorTo(len(m.logBuffer.Snapshot()) - 1)
				return nil
			case 'v':
				m.toggleLogSelection()
				return nil
			case 'y':
				m.copySelectedLogLine()
				return nil
			case 'Y':
				m.copyVisibleLogLines()
				return nil
			}
		case tcell.KeyUp:
			m.moveLogCursor(-1)
			return nil
		case tcell.KeyDown:
			m.moveLogCursor(1)
			return nil
		case tcell.KeyHome:
			m.moveLogCursorTo(0)
			return nil
		case tcell.KeyEnd:
			m.moveLogCursorTo(len(m.logBuffer.Snapshot()) - 1)
			return nil
		case tcell.KeyPgUp, tcell.KeyCtrlB:
			_, _, _, height := m.logs.GetRect()
			m.moveLogCursor(-max(height-2, 1))
			return nil
		case tcell.KeyPgDn, tcell.KeyCtrlF:
			_, _, _, height := m.logs.GetRect()
			m.moveLogCursor(max(height-2, 1))
			return nil
		case tcell.KeyEnter:
			lines := m.logBuffer.Snapshot()
			if len(lines) == 0 {
				return nil
			}
			if m.logCursor >= len(lines) {
				m.logCursor = len(lines) - 1
			}
			m.logFollowTail = false
			m.logDetailText = lines[m.logCursor]
			m.logDetailLine = m.logCursor + 1
			m.logDetailSize = len(lines)
			m.viewLogDetail = true
			m.renderLogDetail()
			return nil
		}
		return event
	}
	if m.viewDetails {
		if event.Key() == tcell.KeyEscape || (event.Key() == tcell.KeyRune && event.Rune() == 'q') {
			m.viewDetails = false
			m.renderList()
			return nil
		}
		return event
	}

	switch event.Key() {
	case tcell.KeyRune:
		switch event.Rune() {
		case 'q':
			m.ui.Stop()
			return nil
		case 'j', 'k', 'l', 'a', 'L', 'e', 'c', 'r', 'd', '/', 's', 'S', 'x':
			return m.handleRune(event.Rune())
		}
	case tcell.KeyEnter:
		m.choose("connect")
		return nil
	case tcell.KeyUp:
		m.moveCursor(-1)
		return nil
	case tcell.KeyDown:
		m.moveCursor(1)
		return nil
	}
	return event
}

// handleRune performs a key action after capturing it at the application level.
func (m *model) handleRune(key rune) *tcell.EventKey {
	if key == 's' || key == 'S' {
		if len(m.visibleItems()) == 0 {
			return nil
		}
		status := strings.ToLower(strings.TrimSpace(m.selected().Status))
		if key == 's' && status == "running" {
			m.choose("stop")
		} else if key == 'S' && (status == "stopped" || status == "exited") {
			m.choose("start")
		}
		return nil
	}
	if key == 'x' {
		if len(m.visibleItems()) > 0 {
			m.confirmSelectedDelete()
		}
		return nil
	}
	if key == 'l' || key == 'a' {
		if len(m.visibleItems()) == 0 {
			return nil
		}
		name := m.selected().Name
		if key == 'a' {
			name = "*"
		}
		m.startLogs(name)
		return nil
	}
	if key == 'L' {
		m.choose("logs")
		return nil
	}
	if key == 'd' {
		if len(m.visibleItems()) > 0 {
			m.viewDetails = true
			m.renderDetails()
		}
		return nil
	}
	if key == '/' {
		m.filterInput = true
		m.filter = ""
		m.cursor = 0
		m.renderList()
		return nil
	}
	if key == 'r' {
		go m.refresh()
		return nil
	}
	if key == 'e' || key == 'c' {
		kind := "exec"
		if key == 'c' {
			kind = "connect"
		}
		m.choose(kind)
		return nil
	}
	if key == 'j' {
		m.moveCursor(1)
		return nil
	}
	if key == 'k' {
		m.moveCursor(-1)
		return nil
	}
	return nil
}

func (m *model) confirmSelectedDelete() {
	item := m.selected()
	if item.Name == "" {
		return
	}
	m.confirmDelete = true
	modal := tview.NewModal().
		SetText("Permanently delete sandbox " + tview.Escape(sanitizeTerminalText(item.Name)) + "?").
		AddButtons([]string{"Cancel", "Delete"}).
		SetDoneFunc(func(buttonIndex int, _ string) {
			m.confirmDelete = false
			if buttonIndex == 1 {
				m.action = Action{Kind: "delete", Name: item.Name}
				m.stopLogs()
				m.ui.Stop()
				return
			}
			m.renderList()
		})
	m.ui.SetRoot(modal, true)
}

func (m *model) moveCursor(delta int) {
	items := m.visibleItems()
	if len(items) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(items) {
		m.cursor = len(items) - 1
	}
	m.table.Select(m.cursor+1, 0)
	m.renderList()
}

func (m *model) selected() Sandbox {
	items := m.visibleItems()
	if len(items) == 0 {
		return Sandbox{}
	}
	row, _ := m.table.GetSelection()
	index := row - 1
	if index < 0 || index >= len(items) {
		index = m.cursor
	}
	return items[index]
}

func (m *model) visibleItems() []Sandbox {
	if m.filter == "" {
		return m.items
	}
	query := strings.ToLower(m.filter)
	items := make([]Sandbox, 0, len(m.items))
	for _, item := range m.items {
		if strings.Contains(strings.ToLower(item.Name), query) || strings.Contains(strings.ToLower(item.Status), query) || strings.Contains(strings.ToLower(item.Image), query) {
			items = append(items, item)
		}
	}
	return items
}

func (m *model) choose(kind string) {
	if len(m.visibleItems()) == 0 {
		return
	}
	m.action = Action{Kind: kind, Name: m.selected().Name}
	m.stopLogs()
	m.ui.Stop()
}

func (m *model) refreshLoop() {
	m.refresh()
	ticker := time.NewTicker(refreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.refresh()
		case <-m.done:
			return
		}
	}
}

func (m *model) refresh() {
	if !m.refreshing.CompareAndSwap(false, true) {
		return
	}
	defer m.refreshing.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	items, err := m.control.ListSandboxes(ctx)
	if !m.active.Load() {
		return
	}
	m.ui.QueueUpdateDraw(func() {
		if err != nil {
			m.err = err.Error()
		} else {
			m.err = ""
			m.items = items
			m.hasLoaded = true
			visibleCount := len(m.visibleItems())
			if visibleCount == 0 {
				m.cursor = 0
			} else if m.cursor >= visibleCount {
				m.cursor = visibleCount - 1
			}
		}
		m.renderList()
	})
}

func (m *model) renderList() {
	if m.viewLogs || m.viewDetails || m.confirmDelete {
		return
	}
	m.root.RemoveItem(m.logs)
	m.root.RemoveItem(m.details)
	if !containsPrimitive(m.root, m.table) {
		m.root.AddItem(m.table, 0, 1, true)
	}
	if !containsPrimitive(m.root, m.status) {
		m.root.AddItem(m.status, 1, 0, false)
	}
	m.ui.SetRoot(m.root, true)
	m.table.Clear()
	for i, label := range []string{"NAME", "STATUS", "IMAGE"} {
		m.table.SetCell(0, i, tview.NewTableCell(label).SetSelectable(false).SetTextColor(m.accent).SetAttributes(tcell.AttrBold))
	}
	visible := m.visibleItems()
	for i, item := range visible {
		row := i + 1
		m.table.SetCell(row, 0, tview.NewTableCell(tview.Escape(sanitizeTerminalText(item.Name))))
		m.table.SetCell(row, 1, tview.NewTableCell(tview.Escape(sanitizeTerminalText(item.Status))))
		m.table.SetCell(row, 2, tview.NewTableCell(tview.Escape(sanitizeTerminalText(item.Image))))
	}
	if len(visible) > 0 {
		m.table.Select(m.cursor+1, 0)
	}
	if m.err != "" {
		message := "Error: " + sanitizeTerminalText(m.err)
		if m.hasLoaded {
			message = "Refresh failed; showing last known sandboxes: " + sanitizeTerminalText(m.err)
		}
		m.status.SetText(message + "   ·   r retry · q quit")
	} else {
		label := fmt.Sprintf("%d/%d sandboxes   ·   / search · d details · enter/c connect · e exec · l logs · a all logs · L dump logs · q quit", len(visible), len(m.items))
		if len(visible) > 0 {
			status := strings.ToLower(strings.TrimSpace(m.selected().Status))
			switch status {
			case "running":
				label += " · s stop"
			case "stopped", "exited":
				label += " · S start"
			}
			label += " · x delete"
		}
		if m.filter != "" || m.filterInput {
			label = fmt.Sprintf("search: %s   ·   enter apply · esc clear · backspace edit", sanitizeTerminalText(m.filter))
		}
		m.status.SetText(label)
	}
}

func (m *model) renderDetails() {
	if !m.viewDetails || len(m.visibleItems()) == 0 {
		return
	}
	item := m.selected()
	m.root.RemoveItem(m.table)
	m.root.RemoveItem(m.logs)
	m.root.RemoveItem(m.status)
	m.details.SetText(fmt.Sprintf("Name: %s\nStatus: %s\nImage: %s", tview.Escape(sanitizeTerminalText(item.Name)), tview.Escape(sanitizeTerminalText(item.Status)), tview.Escape(sanitizeTerminalText(item.Image))))
	m.root.AddItem(m.details, 0, 1, true)
	m.status.SetText("esc back · q back")
	m.root.AddItem(m.status, 1, 0, false)
	m.ui.SetRoot(m.root, true)
}

func (m *model) startLogs(name string) {
	m.stopLogs()
	m.viewLogs = true
	m.logName = name
	m.logBuffer = logRing{}
	m.logErr = ""
	m.logsPaused = false
	m.viewLogDetail = false
	m.logCursor = 0
	m.logFollowTail = true
	m.logSelectAnchor = nil
	m.logDetailText = ""
	m.logDetailLine = 0
	m.logDetailSize = 0
	opt := LogOptions{Follow: true, All: name == "*", Source: m.logSource}
	if name != "*" {
		opt.Names = []string{name}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.logCancel = cancel
	m.renderLogs()
	go m.streamLogs(ctx, name, opt)
}

func (m *model) restartLogs() {
	if m.logName != "" {
		m.startLogs(m.logName)
	}
}

func (m *model) streamLogs(ctx context.Context, name string, opt LogOptions) {
	pr, pw := io.Pipe()
	writeDone := make(chan error, 1)
	go func() {
		err := m.control.Logs(ctx, opt, pw)
		_ = pw.Close()
		writeDone <- err
	}()

	lines := make(chan string, logIngressLines)
	readDone := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := truncateTerminalLine(sanitizeTerminalText(scanner.Text()), maxLogLineBytes)
			select {
			case lines <- line:
			case <-ctx.Done():
				_ = pr.Close()
				readDone <- ctx.Err()
				return
			}
		}
		readDone <- scanner.Err()
	}()

	ticker := time.NewTicker(logRenderInterval)
	defer ticker.Stop()
	batch := make([]string, 0, logBatchLines)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		copyOfBatch := append([]string(nil), batch...)
		batch = batch[:0]
		return m.queueLogUpdate(ctx, logBatchMsg{name: name, lines: copyOfBatch})
	}
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				err := <-readDone
				if flushErr := flush(); flushErr != nil {
					_ = pr.Close()
					return
				}
				_ = pr.Close()
				select {
				case writeErr := <-writeDone:
					if err != nil && ctx.Err() == nil {
						_ = m.queueLogUpdate(ctx, logErrMsg{name: name, err: err})
					} else if writeErr != nil && ctx.Err() == nil {
						_ = m.queueLogUpdate(ctx, logErrMsg{name: name, err: writeErr})
					} else if ctx.Err() == nil {
						_ = m.queueLogUpdate(ctx, logDoneMsg{name: name})
					}
				case <-ctx.Done():
				}
				return
			}
			batch = append(batch, line)
			if len(batch) >= logBatchLines {
				if err := flush(); err != nil {
					_ = pr.Close()
					return
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				_ = pr.Close()
				return
			}
		case <-ctx.Done():
			_ = pr.Close()
			return
		}
	}
}

func (m *model) queueLogUpdate(ctx context.Context, message any) error {
	if !m.active.Load() {
		return context.Canceled
	}
	completed := make(chan struct{})
	go func() {
		m.ui.QueueUpdateDraw(func() {
			defer close(completed)
			switch msg := message.(type) {
			case logBatchMsg:
				for _, line := range msg.lines {
					m.appendLogLine(msg.name, line)
				}
			case logErrMsg:
				if msg.name == m.logName {
					m.logErr = msg.err.Error()
				}
			case logDoneMsg:
				if msg.name == m.logName && m.logErr == "" {
					m.logErr = "(stream ended)"
				}
			}
			_, isBatch := message.(logBatchMsg)
			if !m.logsPaused || !isBatch {
				if m.viewLogDetail {
					m.renderLogDetail()
				} else {
					m.renderLogs()
				}
			}
		})
	}()
	select {
	case <-completed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.done:
		return context.Canceled
	}
}

func truncateTerminalLine(line string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	for len(line) > maxBytes {
		_, size := utf8.DecodeLastRuneInString(line)
		line = line[:len(line)-size]
	}
	return line
}

func (m *model) appendLogLine(session, line string) {
	if session != m.logName {
		return
	}
	wasFull := len(m.logBuffer.lines) > 0 && m.logBuffer.count == len(m.logBuffer.lines)
	m.logBuffer.Add(line)
	if m.logFollowTail {
		m.logCursor = m.logBuffer.count - 1
	} else if wasFull && m.logCursor > 0 {
		m.logCursor--
	}
	if wasFull && m.logSelectAnchor != nil && *m.logSelectAnchor > 0 {
		*m.logSelectAnchor--
	}
}

func (m *model) moveLogCursor(delta int) {
	lines := m.logBuffer.Snapshot()
	if len(lines) == 0 {
		return
	}
	m.logFollowTail = false
	m.moveLogCursorTo(m.logCursor + delta)
}

func (m *model) moveLogCursorTo(index int) {
	lines := m.logBuffer.Snapshot()
	if len(lines) == 0 {
		return
	}
	if index < 0 {
		index = 0
	}
	if index >= len(lines) {
		index = len(lines) - 1
	}
	m.logFollowTail = false
	m.logCursor = index
	m.renderLogs()
}

func (m *model) renderLogs() {
	if !m.viewLogs {
		return
	}
	m.root.RemoveItem(m.table)
	m.root.RemoveItem(m.details)
	m.root.RemoveItem(m.status)
	m.root.RemoveItem(m.logs)
	m.root.AddItem(m.logs, 0, 1, true)
	m.root.AddItem(m.status, 1, 0, false)
	m.ui.SetRoot(m.root, true)
	m.logs.SetTitle("Sandbox logs: " + sanitizeTerminalText(m.logName))
	lines := m.logBuffer.Snapshot()
	if len(lines) > 0 {
		if m.logCursor >= len(lines) {
			m.logCursor = len(lines) - 1
		}
		if m.logFollowTail {
			m.logCursor = len(lines) - 1
		}
	}
	displayLines := make([]string, len(lines))
	for index, line := range lines {
		prefix := "  "
		color := ""
		if m.logSelectAnchor != nil && index >= min(*m.logSelectAnchor, m.logCursor) && index <= max(*m.logSelectAnchor, m.logCursor) {
			prefix = "* "
			color = "[black:yellow]"
		}
		if index == m.logCursor {
			prefix = "> "
			color = "[black:green]"
		}
		displayLines[index] = color + tview.Escape(prefix+sanitizeTerminalText(line))
		if color != "" {
			displayLines[index] += "[-]"
		}
	}
	body := strings.Join(displayLines, "\n")
	if m.logErr != "" {
		if body != "" {
			body += "\n"
		}
		body += tview.Escape(sanitizeTerminalText(m.logErr))
	}
	if body == "" {
		body = "Waiting for log lines…"
	}
	m.logs.SetText(body)
	if m.logFollowTail {
		m.logs.ScrollToEnd()
	} else {
		_, _, _, height := m.logs.GetRect()
		row := max(m.logCursor-max(height-3, 0), 0)
		m.logs.ScrollTo(row, 0)
	}
	status := "j/k select · v range · enter detail · y copy · Y copy view · g/G top/end · s:" + logSourceLabel(m.logSource) + " · p pause · r reconnect · esc/q"
	if m.logSelectAnchor != nil {
		count := max(m.logCursor, *m.logSelectAnchor) - min(m.logCursor, *m.logSelectAnchor) + 1
		status = fmt.Sprintf("visual selection: %d lines · v cancel · y copy · j/k extend · Y copy view · s:%s · p pause · r reconnect · esc/q", count, logSourceLabel(m.logSource))
	}
	if m.logsPaused {
		if m.logSelectAnchor == nil {
			status = "paused · j/k select · v range · enter detail · y copy · Y copy view · g/G top/end · s:" + logSourceLabel(m.logSource) + " · p resume · esc/q"
		} else {
			status = strings.Replace(status, "p pause", "p resume", 1)
		}
	}
	m.status.SetText(status)
}

func (m *model) copySelectedLogLine() {
	lines := m.logBuffer.Snapshot()
	if len(lines) == 0 || m.logCursor < 0 || m.logCursor >= len(lines) {
		return
	}
	if m.logSelectAnchor != nil {
		start := min(*m.logSelectAnchor, m.logCursor)
		end := max(*m.logSelectAnchor, m.logCursor)
		m.copyLogText(strings.Join(lines[start:end+1], "\n"))
		return
	}
	m.copyLogText(lines[m.logCursor])
}

func (m *model) toggleLogSelection() {
	if m.logSelectAnchor != nil {
		m.logSelectAnchor = nil
	} else {
		lines := m.logBuffer.Snapshot()
		if len(lines) == 0 {
			return
		}
		m.logFollowTail = false
		anchor := m.logCursor
		m.logSelectAnchor = &anchor
	}
	m.renderLogs()
}

func (m *model) copyVisibleLogLines() {
	lines := m.logBuffer.Snapshot()
	if len(lines) == 0 {
		return
	}
	first, _ := m.logs.GetScrollOffset()
	_, _, _, height := m.logs.GetRect()
	visible := max(height-2, 1)
	if first < 0 {
		first = 0
	}
	if first >= len(lines) {
		first = len(lines) - 1
	}
	end := min(first+visible, len(lines))
	m.copyLogText(strings.Join(lines[first:end], "\n"))
}

func (m *model) copyLogText(value string) {
	if m.screen == nil {
		m.status.SetText("clipboard unavailable")
		return
	}
	m.screen.SetClipboard([]byte(value))
	m.status.SetText("clipboard copy requested (terminal support required)")
}

func (m *model) renderLogDetail() {
	if !m.viewLogs || !m.viewLogDetail {
		return
	}
	if m.logDetailSize == 0 {
		m.viewLogDetail = false
		m.renderLogs()
		return
	}
	m.root.RemoveItem(m.table)
	m.root.RemoveItem(m.logs)
	m.root.RemoveItem(m.details)
	m.root.RemoveItem(m.status)
	m.root.AddItem(m.details, 0, 1, true)
	m.root.AddItem(m.status, 1, 0, false)
	m.ui.SetRoot(m.root, true)
	m.details.SetTitle("Log line details")
	m.details.SetText(fmt.Sprintf("Line %d of %d\n\n%s", m.logDetailLine, m.logDetailSize, m.logDetailText))
	m.status.SetText("esc back · q quit")
}

func nextLogSource(source string, sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	current := -1
	for index, candidate := range sources {
		if candidate == source || candidate == "all" && source == "" {
			current = index
			break
		}
	}
	return sources[(current+1)%len(sources)]
}

func logSourceLabel(source string) string {
	if source == "" {
		return "all"
	}
	return source
}

func containsPrimitive(flex *tview.Flex, target tview.Primitive) bool {
	for i := 0; i < flex.GetItemCount(); i++ {
		item := flex.GetItem(i)
		if item == target {
			return true
		}
	}
	return false
}

func (m *model) stopLogs() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	if m.viewLogs {
		m.viewLogs = false
	}
	m.viewLogDetail = false
}

// sanitizeTerminalText drops terminal control sequences from untrusted names and logs.
func sanitizeTerminalText(input string) string {
	const (
		plain = iota
		escape
		csi
		osc
		oscEscape
		stringControl
		stringEscape
	)
	var out strings.Builder
	state := plain
	for _, r := range input {
		switch state {
		case plain:
			switch {
			case r == '\x1b':
				state = escape
			case unicode.IsControl(r) && r != '\t':
				// Drop terminal controls, including carriage return and BEL.
			default:
				out.WriteRune(r)
			}
		case escape:
			switch r {
			case '[':
				state = csi
			case ']':
				state = osc
			case 'P', 'X', '^', '_':
				state = stringControl
			default:
				state = plain
			}
		case csi:
			if r >= '@' && r <= '~' {
				state = plain
			}
		case osc:
			switch r {
			case '\a':
				state = plain
			case '\x1b':
				state = oscEscape
			}
		case oscEscape:
			if r == '\\' {
				state = plain
			} else {
				state = osc
			}
		case stringControl:
			if r == '\x1b' {
				state = stringEscape
			}
		case stringEscape:
			if r == '\\' {
				state = plain
			} else {
				state = stringControl
			}
		}
	}
	return out.String()
}
