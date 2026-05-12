package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestWindowResizeKeepsRenderedLogs(t *testing.T) {
	m := newModel("", nil, nil)
	m.state = stateLogs
	m.width = 80
	m.height = 20
	m.selected = deployment{name: "api", namespace: "default"}
	m.pods = []string{"api-abc"}
	m.viewport = viewport.New(80, 14)
	m.logEntries = []logEntry{
		{pod: "api-abc", prefix: "api-abc │ ", raw: "first line"},
		{pod: "api-abc", prefix: "api-abc │ ", raw: "second line"},
	}
	m.refreshViewport()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	resized := updated.(model)

	view := resized.viewport.View()
	if !strings.Contains(view, "first line") || !strings.Contains(view, "second line") {
		t.Fatalf("expected resized viewport to keep log content, got %q", view)
	}
}

func TestRenderEntryWrapsLongLogBody(t *testing.T) {
	m := model{
		width:    24,
		viewport: viewport.New(24, 8),
	}
	prefix := "pod │ "
	entry := logEntry{
		pod:    "pod",
		prefix: prefix,
		raw:    "alpha beta gamma delta epsilon",
	}

	rendered := m.renderEntry(entry)
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected long log body to wrap, got %q", rendered)
	}
	if !strings.HasPrefix(lines[0], prefix) {
		t.Fatalf("expected first wrapped line to include prefix, got %q", lines[0])
	}
	if got, want := len(lines[1])-len(strings.TrimLeft(lines[1], " ")), lipgloss.Width(prefix); got != want {
		t.Fatalf("expected continuation indent width %d, got %d in %q", want, got, lines[1])
	}
}

func TestRefreshViewportAppliesBooleanIncludeFilter(t *testing.T) {
	m := model{
		state:      stateLogs,
		width:      80,
		height:     20,
		viewport:   viewport.New(80, 14),
		filterMd:   filterInclude,
		filterText: "(error OR warn) AND api",
		logEntries: []logEntry{
			{pod: "pod", prefix: "pod │ ", raw: "error from api"},
			{pod: "pod", prefix: "pod │ ", raw: "warn from worker"},
			{pod: "pod", prefix: "pod │ ", raw: "info from api"},
		},
	}

	m.refreshViewport()

	view := m.viewport.View()
	if !strings.Contains(view, "error from api") {
		t.Fatalf("expected matching log in viewport, got %q", view)
	}
	if strings.Contains(view, "warn from worker") || strings.Contains(view, "info from api") {
		t.Fatalf("expected non-matching logs to be filtered, got %q", view)
	}
}

func TestRefreshViewportInvalidBooleanFilterKeepsLogsVisible(t *testing.T) {
	m := model{
		state:      stateLogs,
		width:      80,
		height:     20,
		viewport:   viewport.New(80, 14),
		filterMd:   filterInclude,
		filterText: "error AND",
		logEntries: []logEntry{
			{pod: "pod", prefix: "pod │ ", raw: "info from api"},
		},
	}

	m.refreshViewport()

	if m.filterErr == "" {
		t.Fatal("expected invalid filter to be recorded")
	}
	if view := m.viewport.View(); !strings.Contains(view, "info from api") {
		t.Fatalf("expected invalid filter to leave logs visible, got %q", view)
	}
}

func TestRefreshViewportKeepsScrollPositionWhenNotAtBottom(t *testing.T) {
	m := model{
		state:    stateLogs,
		width:    80,
		height:   10,
		viewport: viewport.New(80, 4),
	}
	for i := 0; i < 20; i++ {
		m.logEntries = append(m.logEntries, logEntry{
			pod:    "pod",
			prefix: "pod │ ",
			raw:    "line",
		})
	}
	m.refreshViewport()
	m.viewport.LineUp(2)
	before := m.viewport.YOffset

	m.logEntries = append(m.logEntries, logEntry{pod: "pod", prefix: "pod │ ", raw: "new line"})
	m.refreshViewport()

	if m.viewport.YOffset != before {
		t.Fatalf("expected refresh to keep scroll offset %d, got %d", before, m.viewport.YOffset)
	}
}

func TestLogViewPageUpScrollsFiveLines(t *testing.T) {
	m := model{
		state:    stateLogs,
		width:    80,
		height:   10,
		viewport: viewport.New(80, 4),
	}
	for i := 0; i < 20; i++ {
		m.logEntries = append(m.logEntries, logEntry{
			pod:    "pod",
			prefix: "pod │ ",
			raw:    "line",
		})
	}
	m.refreshViewport()
	before := m.viewport.YOffset

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	after := updated.(model).viewport.YOffset

	if got := before - after; got != logPageScroll {
		t.Fatalf("expected page up to scroll %d lines, got %d", logPageScroll, got)
	}
}
