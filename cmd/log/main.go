package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── styles ───────────────────────────────────────────────────────────────────

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).PaddingLeft(1)
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).PaddingLeft(1)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	filterIncludeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	filterExcludeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	podSelectedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	podUnselectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	podCursorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))

	overlayBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("205")).
				Padding(0, 1)

	podColors = []lipgloss.Color{"36", "33", "32", "35", "34", "31", "37"}
)

// ─── highlight rules ──────────────────────────────────────────────────────────

type highlightRule struct {
	keyword string // lowercase
	style   lipgloss.Style
}

// default rules applied in priority order (first match wins)
var defaultHighlights = []highlightRule{
	{keyword: "fatal", style: lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)},
	{keyword: "panic", style: lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)},
	{keyword: "error", style: lipgloss.NewStyle().Foreground(lipgloss.Color("202"))},  // orange-red
	{keyword: "warn",  style: lipgloss.NewStyle().Foreground(lipgloss.Color("214"))},  // orange
	{keyword: "debug", style: lipgloss.NewStyle().Foreground(lipgloss.Color("244"))},  // gray
	{keyword: "trace", style: lipgloss.NewStyle().Foreground(lipgloss.Color("240"))},  // darker gray
}

var userHighlightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true) // yellow

// ─── k8s data ─────────────────────────────────────────────────────────────────

type deployment struct {
	name      string
	namespace string
	ready     string
	available string
	age       string
}

type k8sDeploymentList struct {
	Items []struct {
		Metadata struct {
			Name              string    `json:"name"`
			Namespace         string    `json:"namespace"`
			CreationTimestamp time.Time `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			Replicas int `json:"replicas"`
		} `json:"spec"`
		Status struct {
			ReadyReplicas     int `json:"readyReplicas"`
			AvailableReplicas int `json:"availableReplicas"`
		} `json:"status"`
	} `json:"items"`
}

func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 2*time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < 2*time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// ─── log entry ────────────────────────────────────────────────────────────────

type logEntry struct {
	pod    string
	prefix string // colored pod name + " │ "
	raw    string // plain log text
}

// ─── app state ────────────────────────────────────────────────────────────────

type appState int

const (
	stateTable      appState = iota
	stateTableSearch
	stateLogs
	stateLogFilter
	statePodPick
	stateHighlightInput
)

type filterMode int

const (
	filterNone    filterMode = iota
	filterInclude
	filterExclude
)

const maxLogEntries = 10_000

// ─── messages ─────────────────────────────────────────────────────────────────

type podsReadyMsg struct{ pods []string }
type logEntryMsg struct{ entry logEntry }
type errMsg struct{ err error }
type exportDoneMsg struct{ filename string }

// ─── model ────────────────────────────────────────────────────────────────────

type model struct {
	namespace   string
	state       appState
	table       table.Model
	allDeps     []deployment
	tableSearch textinput.Model

	// log view
	selected   deployment
	pods       []string
	activePods map[string]bool
	logEntries []logEntry
	logCh      chan logEntry
	cancel     context.CancelFunc
	viewport   viewport.Model

	// log filter
	filterMd    filterMode
	filterText  string
	filterInput textinput.Model

	// highlights
	userHighlights []string // lowercase keywords
	hlInput        textinput.Model

	// pod picker
	podCursor int

	width         int
	height        int
	errText       string
	exportNotice  string
}

func buildTableRows(deps []deployment) []table.Row {
	rows := make([]table.Row, len(deps))
	for i, d := range deps {
		rows[i] = table.Row{d.name, d.namespace, d.ready, d.available, d.age}
	}
	return rows
}

func newModel(namespace string, deps []deployment, initialDeploy *deployment) model {
	cols := []table.Column{
		{Title: "NAME", Width: 32},
		{Title: "NAMESPACE", Width: 14},
		{Title: "READY", Width: 8},
		{Title: "AVAILABLE", Width: 10},
		{Title: "AGE", Width: 6},
	}

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true).
		Foreground(lipgloss.Color("252"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true)

	t := table.New(
		table.WithColumns(cols),
		table.WithRows(buildTableRows(deps)),
		table.WithFocused(true),
		table.WithStyles(s),
	)

	ts := textinput.New()
	ts.Prompt = dimStyle.Render("/") + " "
	ts.Placeholder = "search deployments..."
	ts.CharLimit = 64

	fi := textinput.New()
	fi.CharLimit = 128

	hi := textinput.New()
	hi.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Render("highlight> ")
	hi.Placeholder = "keyword to highlight..."
	hi.CharLimit = 64

	m := model{
		namespace:   namespace,
		state:       stateTable,
		table:       t,
		allDeps:     deps,
		tableSearch: ts,
		filterInput: fi,
		hlInput:     hi,
		activePods:  map[string]bool{},
	}

	if initialDeploy != nil {
		m.state = stateLogs
		m.selected = *initialDeploy
	}

	return m
}

func (m model) Init() tea.Cmd {
	if m.state == stateLogs {
		return fetchPods(m.selected.namespace, m.selected.name)
	}
	return nil
}

// ─── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		nameWidth := m.width - 38
		if nameWidth < 20 {
			nameWidth = 20
		}
		m.table.SetColumns([]table.Column{
			{Title: "NAME", Width: nameWidth},
			{Title: "NAMESPACE", Width: 14},
			{Title: "READY", Width: 8},
			{Title: "AVAILABLE", Width: 10},
			{Title: "AGE", Width: 6},
		})
		m.table.SetHeight(m.height - 3)
		m.viewport = viewport.New(m.width, m.height-6)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case podsReadyMsg:
		m.pods = msg.pods
		m.activePods = make(map[string]bool)
		if len(m.pods) == 0 {
			m.errText = "no pods found for: " + m.selected.name
			return m, nil
		}
		return m.startStreaming()

	case logEntryMsg:
		m.logEntries = append(m.logEntries, msg.entry)
		if len(m.logEntries) > maxLogEntries {
			m.logEntries = m.logEntries[len(m.logEntries)-maxLogEntries:]
		}
		m.refreshViewport()
		return m, waitForLog(m.logCh)

	case exportDoneMsg:
		m.exportNotice = "saved → " + msg.filename

	case errMsg:
		m.errText = msg.err.Error()
	}

	var cmd tea.Cmd
	switch m.state {
	case stateLogFilter:
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.filterText = m.filterInput.Value()
		m.refreshViewport()
	case stateTableSearch:
		m.tableSearch, cmd = m.tableSearch.Update(msg)
		m.applyTableSearch()
	case stateHighlightInput:
		m.hlInput, cmd = m.hlInput.Update(msg)
	case stateTable:
		m.table, cmd = m.table.Update(msg)
	}
	return m, cmd
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

	// ── deployment table ──────────────────────────────────────────────────────
	case stateTable:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "/":
			m.state = stateTableSearch
			m.tableSearch.SetValue("")
			m.tableSearch.Focus()
			return m, textinput.Blink
		case "enter":
			row := m.table.SelectedRow()
			if row == nil {
				break
			}
			for _, d := range m.allDeps {
				if d.name == row[0] && d.namespace == row[1] {
					return m.selectDeployment(d)
				}
			}
		}
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd

	// ── deployment table search ───────────────────────────────────────────────
	case stateTableSearch:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.tableSearch.Blur()
			m.tableSearch.SetValue("")
			m.applyTableSearch()
			m.state = stateTable
			return m, nil
		case "enter":
			row := m.table.SelectedRow()
			if row != nil {
				for _, d := range m.allDeps {
					if d.name == row[0] && d.namespace == row[1] {
						m.tableSearch.Blur()
						m.tableSearch.SetValue("")
						m.applyTableSearch()
						return m.selectDeployment(d)
					}
				}
			}
			m.tableSearch.Blur()
			m.state = stateTable
			return m, nil
		}
		var cmd tea.Cmd
		m.tableSearch, cmd = m.tableSearch.Update(msg)
		m.applyTableSearch()
		var tCmd tea.Cmd
		m.table, tCmd = m.table.Update(msg)
		return m, tea.Batch(cmd, tCmd)

	// ── log view ──────────────────────────────────────────────────────────────
	case stateLogs:
		m.exportNotice = ""
		switch msg.String() {
		case "ctrl+c", "q":
			m.stopLogs()
			return m, tea.Quit
		case "esc", "b":
			m.stopLogs()
			m.state = stateTable
			m.logEntries = nil
			m.pods = nil
			m.activePods = map[string]bool{}
			m.filterText = ""
			m.filterMd = filterNone
			m.errText = ""
			return m, nil
		case "/":
			m.state = stateLogFilter
			m.filterMd = filterInclude
			m.filterInput.SetValue(m.filterText)
			m.filterInput.Focus()
			m.filterInput.Prompt = filterIncludeStyle.Render("include> ")
			return m, textinput.Blink
		case "!":
			m.state = stateLogFilter
			m.filterMd = filterExclude
			m.filterInput.SetValue(m.filterText)
			m.filterInput.Focus()
			m.filterInput.Prompt = filterExcludeStyle.Render("exclude> ")
			return m, textinput.Blink
		case "0":
			m.filterMd = filterNone
			m.filterText = ""
			m.filterInput.SetValue("")
			m.refreshViewport()
			return m, nil
		case "p":
			m.state = statePodPick
			m.podCursor = 0
			return m, nil
		case "h":
			m.state = stateHighlightInput
			m.hlInput.SetValue("")
			m.hlInput.Focus()
			return m, textinput.Blink
		case "e":
			return m, exportLogs(m.logEntries, m.selected, m.filterMd, m.filterText, m.activePods)
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	// ── log filter input ──────────────────────────────────────────────────────
	case stateLogFilter:
		switch msg.String() {
		case "enter":
			m.filterText = m.filterInput.Value()
			m.filterInput.Blur()
			m.state = stateLogs
			m.refreshViewport()
			return m, nil
		case "esc":
			m.filterInput.Blur()
			m.filterInput.SetValue(m.filterText)
			m.state = stateLogs
			return m, nil
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		m.filterText = m.filterInput.Value()
		m.refreshViewport()
		return m, cmd

	// ── highlight input ───────────────────────────────────────────────────────
	case stateHighlightInput:
		switch msg.String() {
		case "enter":
			kw := strings.ToLower(strings.TrimSpace(m.hlInput.Value()))
			if kw != "" {
				m.userHighlights = toggleHighlight(m.userHighlights, kw)
				m.refreshViewport()
			}
			m.hlInput.Blur()
			m.state = stateLogs
			return m, nil
		case "esc":
			m.hlInput.Blur()
			m.state = stateLogs
			return m, nil
		}
		var cmd tea.Cmd
		m.hlInput, cmd = m.hlInput.Update(msg)
		return m, cmd

	// ── pod picker overlay ────────────────────────────────────────────────────
	case statePodPick:
		switch msg.String() {
		case "esc", "enter", "p":
			m.state = stateLogs
			m.refreshViewport()
			return m, nil
		case "up", "k":
			if m.podCursor > 0 {
				m.podCursor--
			}
		case "down", "j":
			if m.podCursor < len(m.pods)-1 {
				m.podCursor++
			}
		case " ":
			pod := m.pods[m.podCursor]
			if len(m.activePods) == 0 {
				for _, p := range m.pods {
					m.activePods[p] = true
				}
				m.activePods[pod] = false
			} else {
				m.activePods[pod] = !m.activePods[pod]
				if m.allPodsSelected() {
					m.activePods = map[string]bool{}
				}
			}
			m.refreshViewport()
		case "a":
			m.activePods = map[string]bool{}
			m.refreshViewport()
		}
	}
	return m, nil
}

func toggleHighlight(list []string, kw string) []string {
	for i, v := range list {
		if v == kw {
			return append(list[:i], list[i+1:]...)
		}
	}
	return append(list, kw)
}

func (m model) allPodsSelected() bool {
	for _, p := range m.pods {
		if !m.activePods[p] {
			return false
		}
	}
	return true
}

// ─── highlight rendering ──────────────────────────────────────────────────────

func (m model) renderEntry(e logEntry) string {
	lower := strings.ToLower(e.raw)

	// user highlights take priority
	for _, kw := range m.userHighlights {
		if strings.Contains(lower, kw) {
			return e.prefix + userHighlightStyle.Render(e.raw)
		}
	}

	// default rules
	for _, rule := range defaultHighlights {
		if strings.Contains(lower, rule.keyword) {
			return e.prefix + rule.style.Render(e.raw)
		}
	}

	return e.prefix + e.raw
}

// ─── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.state {
	case stateTable, stateTableSearch:
		ns := m.namespace
		if ns == "" {
			ns = "all namespaces"
		}
		title := titleStyle.Render(fmt.Sprintf("Deployments  [%s]", ns))
		searchBar := m.tableSearchBar()
		help := helpStyle.Render("↑/↓ navigate · / search · enter select · q quit")
		return title + "\n" + searchBar + "\n" + m.table.View() + "\n" + help

	case stateLogs, stateLogFilter, statePodPick, stateHighlightInput:
		return m.logView()
	}
	return ""
}

func (m model) tableSearchBar() string {
	if m.state == stateTableSearch {
		return " " + m.tableSearch.View()
	}
	if q := m.tableSearch.Value(); q != "" {
		return dimStyle.Render(" filter: ") + q
	}
	return dimStyle.Render(" / to search")
}

func (m model) logView() string {
	if m.errText != "" {
		return titleStyle.Render("Error") + "\n\n  " + m.errText + "\n\n" + helpStyle.Render("b/esc back · q quit")
	}

	title := titleStyle.Render(fmt.Sprintf(
		"Logs: %s/%s  [%d/%d pod(s)]",
		m.selected.namespace, m.selected.name,
		m.visiblePodCount(), len(m.pods),
	))

	statusBar := m.logStatusBar()

	var help string
	switch m.state {
	case stateLogFilter:
		help = helpStyle.Render("enter confirm · esc cancel")
	case statePodPick:
		help = helpStyle.Render("↑/↓ move · space toggle · a all · enter/esc close")
	case stateHighlightInput:
		help = helpStyle.Render("enter add/remove · esc cancel")
	default:
		if m.exportNotice != "" {
			help = helpStyle.Render(m.exportNotice)
		} else {
			help = helpStyle.Render("/ include  ! exclude  0 clear  p pods  h highlight  e export  ↑/↓ scroll  b back  q quit")
		}
	}

	body := m.viewport.View()
	if m.state == statePodPick {
		body = m.renderPodOverlay(body)
	}

	return title + "\n" + statusBar + "\n" + body + "\n" + help
}

func (m model) logStatusBar() string {
	var parts []string

	switch m.state {
	case stateLogFilter:
		return " " + m.filterInput.View()
	case stateHighlightInput:
		return " " + m.hlInput.View()
	}

	if m.filterMd == filterInclude && m.filterText != "" {
		parts = append(parts, filterIncludeStyle.Render("include:")+dimStyle.Render(m.filterText))
	}
	if m.filterMd == filterExclude && m.filterText != "" {
		parts = append(parts, filterExcludeStyle.Render("exclude:")+dimStyle.Render(m.filterText))
	}
	for _, kw := range m.userHighlights {
		parts = append(parts, userHighlightStyle.Render("hl:")+dimStyle.Render(kw))
	}

	if len(parts) == 0 {
		return dimStyle.Render(" no filter · no highlight")
	}
	return " " + strings.Join(parts, "  ")
}

func (m model) visiblePodCount() int {
	if len(m.activePods) == 0 {
		return len(m.pods)
	}
	n := 0
	for _, v := range m.activePods {
		if v {
			n++
		}
	}
	return n
}

func (m model) renderPodOverlay(behind string) string {
	lines := strings.Split(behind, "\n")
	var rows []string
	for i, pod := range m.pods {
		active := len(m.activePods) == 0 || m.activePods[pod]
		check := "[ ]"
		st := podUnselectedStyle
		if active {
			check = "[x]"
			st = podSelectedStyle
		}
		row := fmt.Sprintf("  %s %s", check, st.Render(pod))
		if i == m.podCursor {
			row = podCursorStyle.Render(fmt.Sprintf("  %s %s", check, pod))
		}
		rows = append(rows, row)
	}
	rows = append(rows, "", helpStyle.Render("  space toggle · a all · enter close"))

	overlay := overlayBorderStyle.Render(
		titleStyle.Render("Select Pods") + "\n" + strings.Join(rows, "\n"),
	)

	overlayLines := strings.Split(overlay, "\n")
	startRow, startCol := 1, 2
	for i, ol := range overlayLines {
		row := startRow + i
		if row >= len(lines) {
			break
		}
		runes := []rune(lines[row])
		for len(runes) < startCol+len([]rune(ol)) {
			runes = append(runes, ' ')
		}
		copy(runes[startCol:], []rune(ol))
		lines[row] = string(runes)
	}
	return strings.Join(lines, "\n")
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func (m *model) applyTableSearch() {
	q := strings.ToLower(m.tableSearch.Value())
	if q == "" {
		m.table.SetRows(buildTableRows(m.allDeps))
		return
	}
	var filtered []deployment
	for _, d := range m.allDeps {
		if strings.Contains(strings.ToLower(d.name), q) ||
			strings.Contains(strings.ToLower(d.namespace), q) {
			filtered = append(filtered, d)
		}
	}
	m.table.SetRows(buildTableRows(filtered))
}

func (m *model) refreshViewport() {
	var sb strings.Builder
	for _, e := range m.logEntries {
		if len(m.activePods) > 0 && !m.activePods[e.pod] {
			continue
		}
		switch m.filterMd {
		case filterInclude:
			if m.filterText != "" && !strings.Contains(e.raw, m.filterText) {
				continue
			}
		case filterExclude:
			if m.filterText != "" && strings.Contains(e.raw, m.filterText) {
				continue
			}
		}
		sb.WriteString(m.renderEntry(e))
		sb.WriteByte('\n')
	}
	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

func (m *model) stopLogs() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.logCh != nil {
		go func(ch chan logEntry) { for range ch {} }(m.logCh)
		m.logCh = nil
	}
}

func (m model) selectDeployment(d deployment) (model, tea.Cmd) {
	m.stopLogs()
	m.selected = d
	m.state = stateLogs
	m.logEntries = nil
	m.errText = ""
	m.filterMd = filterNone
	m.filterText = ""
	m.activePods = map[string]bool{}
	m.viewport = viewport.New(m.width, m.height-6)
	return m, fetchPods(d.namespace, d.name)
}

func (m model) startStreaming() (model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan logEntry, 512)
	m.cancel = cancel
	m.logCh = ch
	for idx, pod := range m.pods {
		color := podColors[idx%len(podColors)]
		prefix := lipgloss.NewStyle().Foreground(color).Render(pod) + " │ "
		go streamPodLogs(ctx, m.selected.namespace, pod, prefix, ch)
	}
	return m, waitForLog(ch)
}

// ─── tea commands ─────────────────────────────────────────────────────────────

func fetchPods(ns, deployment string) tea.Cmd {
	return func() tea.Msg {
		selector, err := deploymentSelector(ns, deployment)
		if err != nil {
			return errMsg{err}
		}
		args := []string{"-n", ns, "get", "pods",
			"--selector", selector,
			"--field-selector", "status.phase=Running",
			"-o", "jsonpath={.items[*].metadata.name}",
		}
		out, err := exec.Command("kubectl", args...).Output()
		if err != nil || len(strings.TrimSpace(string(out))) == 0 {
			args2 := []string{"-n", ns, "get", "pods",
				"--selector", selector,
				"-o", "jsonpath={.items[*].metadata.name}",
			}
			out, err = exec.Command("kubectl", args2...).Output()
		}
		if err != nil {
			return errMsg{err}
		}
		return podsReadyMsg{pods: strings.Fields(string(out))}
	}
}

func deploymentSelector(ns, deployment string) (string, error) {
	out, err := exec.Command("kubectl", "-n", ns, "get", "deployment", deployment,
		"-o", "json").Output()
	if err != nil {
		return "", err
	}
	var d struct {
		Spec struct {
			Selector struct {
				MatchLabels map[string]string `json:"matchLabels"`
			} `json:"selector"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return "", err
	}
	if len(d.Spec.Selector.MatchLabels) == 0 {
		return "app=" + deployment, nil
	}
	parts := make([]string, 0, len(d.Spec.Selector.MatchLabels))
	for k, v := range d.Spec.Selector.MatchLabels {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ","), nil
}

func streamPodLogs(ctx context.Context, ns, pod, prefix string, ch chan<- logEntry) {
	args := []string{"-n", ns, "logs", "-f", "--tail=200", pod}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		raw := scanner.Text()
		select {
		case <-ctx.Done():
			cmd.Process.Kill()
			return
		case ch <- logEntry{pod: pod, prefix: prefix, raw: raw}:
		}
	}
	cmd.Wait()
}

func exportLogs(entries []logEntry, deploy deployment, fm filterMode, filterText string, activePods map[string]bool) tea.Cmd {
	return func() tea.Msg {
		filename := fmt.Sprintf("kubectl-log-%s-%s.log", deploy.name, time.Now().Format("20060102-150405"))
		f, err := os.Create(filename)
		if err != nil {
			return errMsg{err}
		}
		defer f.Close()
		count := 0
		for _, e := range entries {
			if len(activePods) > 0 && !activePods[e.pod] {
				continue
			}
			switch fm {
			case filterInclude:
				if filterText != "" && !strings.Contains(e.raw, filterText) {
					continue
				}
			case filterExclude:
				if filterText != "" && strings.Contains(e.raw, filterText) {
					continue
				}
			}
			fmt.Fprintf(f, "[%s] %s\n", e.pod, e.raw)
			count++
		}
		return exportDoneMsg{filename: fmt.Sprintf("%s (%d lines)", filename, count)}
	}
}

func waitForLog(ch chan logEntry) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return logEntryMsg{entry: e}
	}
}

// ─── data fetching ────────────────────────────────────────────────────────────

func getDeployments(ns string) ([]deployment, error) {
	var args []string
	if ns != "" {
		args = []string{"-n", ns, "get", "deployments", "-o", "json"}
	} else {
		args = []string{"get", "deployments", "--all-namespaces", "-o", "json"}
	}
	out, err := exec.Command("kubectl", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get deployments: %w", err)
	}
	var list k8sDeploymentList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse deployments: %w", err)
	}
	deps := make([]deployment, len(list.Items))
	for i, item := range list.Items {
		deps[i] = deployment{
			name:      item.Metadata.Name,
			namespace: item.Metadata.Namespace,
			ready:     fmt.Sprintf("%d/%d", item.Status.ReadyReplicas, item.Spec.Replicas),
			available: fmt.Sprintf("%d", item.Status.AvailableReplicas),
			age:       humanAge(item.Metadata.CreationTimestamp),
		}
	}
	return deps, nil
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	namespace, deployName := parseArgs(os.Args[1:])

	deps, err := getDeployments(namespace)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(deps) == 0 {
		fmt.Fprintln(os.Stderr, "no deployments found")
		os.Exit(1)
	}

	var initialDeploy *deployment
	if deployName != "" {
		for i, d := range deps {
			if d.name == deployName {
				initialDeploy = &deps[i]
				break
			}
		}
		if initialDeploy == nil {
			fmt.Fprintf(os.Stderr, "deployment %q not found\n", deployName)
			os.Exit(1)
		}
	}

	p := tea.NewProgram(newModel(namespace, deps, initialDeploy), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseArgs(args []string) (namespace, deployName string) {
	skip := false
	for i, arg := range args {
		if skip {
			skip = false
			continue
		}
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Print(`Usage: kubectl log [OPTIONS] [DEPLOYMENT]

Interactive log viewer for Kubernetes deployments.

Options:
  -n, --namespace <namespace>   Filter by namespace (default: all)
  -h, --help                    Show this help

Keys (deployment list):
  ↑/↓ navigate · / search · enter select · q quit

Keys (log view):
  ↑/↓/PgUp/PgDn scroll · / include · ! exclude · 0 clear
  p pod selector · h highlight · e export to file · b back · q quit
`)
			os.Exit(0)
		case (arg == "-n" || arg == "--namespace") && i+1 < len(args):
			namespace = args[i+1]
			skip = true
		case strings.HasPrefix(arg, "--namespace="):
			namespace = strings.TrimPrefix(arg, "--namespace=")
		case strings.HasPrefix(arg, "-n="):
			namespace = strings.TrimPrefix(arg, "-n=")
		case !strings.HasPrefix(arg, "-"):
			deployName = arg
		}
	}
	return
}
