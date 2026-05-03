package main

import (
	"bufio"
	"encoding/json"
	"fmt"
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

	statusReady    = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	statusNotReady = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	statusCordoned = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)

	cpuHighStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	cpuMidStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	cpuLowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	memHighStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	memMidStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	memLowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))

	confirmStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(1, 3)

	progressOkStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))
	progressErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// ─── k8s types ────────────────────────────────────────────────────────────────

type node struct {
	name          string
	status        string
	roles         string
	age           string
	version       string
	ip            string
	unschedulable bool
}

type nodeMetric struct {
	cpu    string // e.g. "150m"
	cpuPct string // e.g. "7%"
	mem    string // e.g. "1200Mi"
	memPct string // e.g. "62%"
}

type podRow struct {
	namespace string
	name      string
	status    string
	restarts  string
	age       string
}

type k8sNodeList struct {
	Items []struct {
		Metadata struct {
			Name              string            `json:"name"`
			CreationTimestamp time.Time         `json:"creationTimestamp"`
			Labels            map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Unschedulable bool `json:"unschedulable"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			Addresses []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"addresses"`
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
			} `json:"nodeInfo"`
		} `json:"status"`
	} `json:"items"`
}

type k8sPodList struct {
	Items []struct {
		Metadata struct {
			Name              string    `json:"name"`
			Namespace         string    `json:"namespace"`
			CreationTimestamp time.Time `json:"creationTimestamp"`
		} `json:"metadata"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				RestartCount int `json:"restartCount"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func parseNodes(data []byte) ([]node, error) {
	var list k8sNodeList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	nodes := make([]node, len(list.Items))
	for i, item := range list.Items {
		status := "Unknown"
		for _, c := range item.Status.Conditions {
			if c.Type == "Ready" {
				if item.Spec.Unschedulable {
					status = "SchedulingDisabled"
				} else if c.Status == "True" {
					status = "Ready"
				} else {
					status = "NotReady"
				}
				break
			}
		}
		var roles []string
		for k := range item.Metadata.Labels {
			if strings.HasPrefix(k, "node-role.kubernetes.io/") {
				if r := strings.TrimPrefix(k, "node-role.kubernetes.io/"); r != "" {
					roles = append(roles, r)
				}
			}
		}
		if len(roles) == 0 {
			roles = []string{"<none>"}
		}
		var ip string
		for _, addr := range item.Status.Addresses {
			if addr.Type == "InternalIP" {
				ip = addr.Address
				break
			}
		}
		nodes[i] = node{
			name:          item.Metadata.Name,
			status:        status,
			roles:         strings.Join(roles, ","),
			age:           humanAge(item.Metadata.CreationTimestamp),
			version:       item.Status.NodeInfo.KubeletVersion,
			ip:            ip,
			unschedulable: item.Spec.Unschedulable,
		}
	}
	return nodes, nil
}

func parseTopNodes(out []byte) map[string]nodeMetric {
	m := make(map[string]nodeMetric)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			m[fields[0]] = nodeMetric{
				cpu:    fields[1],
				cpuPct: fields[2],
				mem:    fields[3],
				memPct: fields[4],
			}
		}
	}
	return m
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

func colorStatus(s string) string {
	switch s {
	case "Ready":
		return statusReady.Render(s)
	case "NotReady":
		return statusNotReady.Render(s)
	case "SchedulingDisabled":
		return statusCordoned.Render(s)
	default:
		return dimStyle.Render(s)
	}
}

// pct is like "7%" or "82%" — returns colored string
func colorPct(pct string, hi, mid lipgloss.Style) string {
	if pct == "" || pct == "<unknown>" {
		return dimStyle.Render("-")
	}
	val := strings.TrimSuffix(pct, "%")
	var n int
	fmt.Sscanf(val, "%d", &n)
	switch {
	case n >= 80:
		return hi.Render(pct)
	case n >= 50:
		return mid.Render(pct)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(pct)
	}
}

// ─── app state ────────────────────────────────────────────────────────────────

type appState int

const (
	stateList         appState = iota
	stateListSearch
	stateDetail
	stateConfirm
	stateProgress
)

type actionKind int

const (
	actionDrain    actionKind = iota
	actionCordon
	actionUncordon
)

func (a actionKind) label() string {
	return [...]string{"drain", "cordon", "uncordon"}[a]
}

// ─── messages ─────────────────────────────────────────────────────────────────

type nodesLoadedMsg struct{ nodes []node }
type topNodesMsg struct{ metrics map[string]nodeMetric }
type tickMsg time.Time
type podsLoadedMsg struct{ rows []podRow }
type progressLineMsg struct {
	line  string
	isErr bool
}
type actionDoneMsg struct{ err error }
type sshDoneMsg struct{ err error }
type errMsg struct{ err error }

// ─── model ────────────────────────────────────────────────────────────────────

type model struct {
	state    appState
	allNodes []node
	metrics  map[string]nodeMetric

	// node list
	nodeTable  table.Model
	nodeSearch textinput.Model

	// node detail (pods)
	selected node
	podTable table.Model
	podRows  []podRow

	// confirm + progress
	action        actionKind
	progressLines []string
	progressDone  bool
	progressErr   error
	progressCh    chan progressLineMsg
	progressVP    viewport.Model

	width  int
	height int
	err    string
}

// ─── table helpers ────────────────────────────────────────────────────────────

func tableStyles() table.Styles {
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
	return s
}

func nodeCols(nameWidth int) []table.Column {
	return []table.Column{
		{Title: "NAME", Width: nameWidth},
		{Title: "STATUS", Width: 18},
		{Title: "ROLES", Width: 14},
		{Title: "IP", Width: 16},
		{Title: "CPU", Width: 8},
		{Title: "CPU%", Width: 6},
		{Title: "MEM", Width: 9},
		{Title: "MEM%", Width: 6},
		{Title: "AGE", Width: 6},
	}
}

func podCols(nameWidth int) []table.Column {
	return []table.Column{
		{Title: "NAMESPACE", Width: 16},
		{Title: "NAME", Width: nameWidth},
		{Title: "STATUS", Width: 12},
		{Title: "RESTARTS", Width: 9},
		{Title: "AGE", Width: 6},
	}
}

func nodeNameWidth(w int) int {
	// fixed: STATUS(18)+ROLES(14)+IP(16)+CPU(8)+CPU%(6)+MEM(9)+MEM%(6)+AGE(6) = 83
	nw := w - 83
	if nw < 20 {
		nw = 20
	}
	return nw
}

func podNameWidth(w int) int {
	// fixed: NAMESPACE(16)+STATUS(12)+RESTARTS(9)+AGE(6) = 43
	nw := w - 43
	if nw < 20 {
		nw = 20
	}
	return nw
}

func (m model) buildNodeRows() []table.Row {
	rows := make([]table.Row, len(m.allNodes))
	for i, n := range m.allNodes {
		cpu, cpuPct, mem, memPct := "-", "-", "-", "-"
		if met, ok := m.metrics[n.name]; ok {
			cpu = met.cpu
			cpuPct = met.cpuPct
			mem = met.mem
			memPct = met.memPct
		}
		rows[i] = table.Row{n.name, n.status, n.roles, n.ip, cpu, cpuPct, mem, memPct, n.age}
	}
	return rows
}

func buildPodRows(pods []podRow) []table.Row {
	rows := make([]table.Row, len(pods))
	for i, p := range pods {
		rows[i] = table.Row{p.namespace, p.name, p.status, p.restarts, p.age}
	}
	return rows
}

// ─── constructor ──────────────────────────────────────────────────────────────

func newModel(nodes []node, initialNode *node) model {
	ts := textinput.New()
	ts.Prompt = dimStyle.Render("/") + " "
	ts.Placeholder = "search nodes..."
	ts.CharLimit = 64

	nt := table.New(
		table.WithColumns(nodeCols(24)),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
	)

	pt := table.New(
		table.WithColumns(podCols(40)),
		table.WithRows(nil),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
	)

	m := model{
		state:      stateList,
		allNodes:   nodes,
		metrics:    map[string]nodeMetric{},
		nodeTable:  nt,
		nodeSearch: ts,
		podTable:   pt,
	}
	m.nodeTable.SetRows(m.buildNodeRows())

	if initialNode != nil {
		m.selected = *initialNode
		m.state = stateDetail
	}

	return m
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		fetchTopNodes(),
		tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	}
	if m.state == stateDetail {
		cmds = append(cmds, fetchPods(m.selected.name))
	}
	return tea.Batch(cmds...)
}

// ─── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.nodeTable.SetColumns(nodeCols(nodeNameWidth(m.width)))
		m.nodeTable.SetHeight(m.height - 3)
		m.podTable.SetColumns(podCols(podNameWidth(m.width)))
		m.podTable.SetHeight(m.height - 4)
		m.progressVP = viewport.New(m.width, m.height-4)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tickMsg:
		return m, tea.Batch(
			fetchTopNodes(),
			tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)

	case topNodesMsg:
		m.metrics = msg.metrics
		m.nodeTable.SetRows(m.buildNodeRows())

	case nodesLoadedMsg:
		m.allNodes = msg.nodes
		m.nodeTable.SetRows(m.buildNodeRows())
		if m.state == stateProgress && m.progressDone {
			for _, n := range m.allNodes {
				if n.name == m.selected.name {
					m.selected = n
					break
				}
			}
		}

	case podsLoadedMsg:
		m.podRows = msg.rows
		m.podTable.SetRows(buildPodRows(m.podRows))

	case progressLineMsg:
		m.progressLines = append(m.progressLines, msg.line)
		m.progressVP.SetContent(strings.Join(m.progressLines, "\n"))
		m.progressVP.GotoBottom()
		return m, waitProgress(m.progressCh)

	case actionDoneMsg:
		m.progressDone = true
		m.progressErr = msg.err
		return m, reloadNodes()

	case sshDoneMsg:
		// SSH session ended — return to TUI

	case errMsg:
		m.err = msg.err.Error()
	}

	var cmd tea.Cmd
	switch m.state {
	case stateList:
		m.nodeTable, cmd = m.nodeTable.Update(msg)
	case stateListSearch:
		m.nodeSearch, cmd = m.nodeSearch.Update(msg)
		m.applyNodeSearch()
		var tc tea.Cmd
		m.nodeTable, tc = m.nodeTable.Update(msg)
		return m, tea.Batch(cmd, tc)
	case stateDetail:
		m.podTable, cmd = m.podTable.Update(msg)
	case stateProgress:
		m.progressVP, cmd = m.progressVP.Update(msg)
	}
	return m, cmd
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

	// ── node list ─────────────────────────────────────────────────────────────
	case stateList:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "/":
			m.state = stateListSearch
			m.nodeSearch.SetValue("")
			m.nodeSearch.Focus()
			return m, textinput.Blink
		case "enter", "s":
			if n, ok := m.nodeFromRow(); ok && n.ip != "" {
				return m, sshCmd(n.ip)
			}
		case "i":
			if n, ok := m.nodeFromRow(); ok {
				m.selected = n
				m.state = stateDetail
				m.podRows = nil
				m.podTable.SetRows(nil)
				m.podTable.SetHeight(m.height - 4)
				return m, fetchPods(n.name)
			}
		case "d":
			return m.promptAction(actionDrain)
		case "c":
			return m.promptAction(actionCordon)
		case "u":
			return m.promptAction(actionUncordon)
		case "r":
			return m, reloadNodes()
		}
		var cmd tea.Cmd
		m.nodeTable, cmd = m.nodeTable.Update(msg)
		return m, cmd

	// ── node list search ──────────────────────────────────────────────────────
	case stateListSearch:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.nodeSearch.Blur()
			m.nodeSearch.SetValue("")
			m.applyNodeSearch()
			m.state = stateList
			return m, nil
		case "enter":
			if n, ok := m.nodeFromRow(); ok && n.ip != "" {
				m.nodeSearch.Blur()
				m.nodeSearch.SetValue("")
				m.applyNodeSearch()
				return m, sshCmd(n.ip)
			}
			m.nodeSearch.Blur()
			m.state = stateList
			return m, nil
		}
		var cmd tea.Cmd
		m.nodeSearch, cmd = m.nodeSearch.Update(msg)
		m.applyNodeSearch()
		var tc tea.Cmd
		m.nodeTable, tc = m.nodeTable.Update(msg)
		return m, tea.Batch(cmd, tc)

	// ── node detail ───────────────────────────────────────────────────────────
	case stateDetail:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "esc", "b":
			m.state = stateList
			return m, nil
		case "d":
			return m.promptAction(actionDrain)
		case "c":
			return m.promptAction(actionCordon)
		case "u":
			return m.promptAction(actionUncordon)
		case "s":
			if m.selected.ip != "" {
				return m, sshCmd(m.selected.ip)
			}
		case "r":
			return m, fetchPods(m.selected.name)
		}
		var cmd tea.Cmd
		m.podTable, cmd = m.podTable.Update(msg)
		return m, cmd

	// ── confirm ───────────────────────────────────────────────────────────────
	case stateConfirm:
		switch msg.String() {
		case "y", "Y":
			return m.startAction()
		case "n", "N", "esc", "q":
			if m.podRows != nil {
				m.state = stateDetail
			} else {
				m.state = stateList
			}
		}

	// ── progress ──────────────────────────────────────────────────────────────
	case stateProgress:
		switch msg.String() {
		case "ctrl+c", "q", "esc", "b", "enter":
			if m.progressDone {
				if m.podRows != nil {
					m.state = stateDetail
					return m, fetchPods(m.selected.name)
				}
				m.state = stateList
				m.progressLines = nil
				m.progressDone = false
				m.progressErr = nil
			}
		}
		var cmd tea.Cmd
		m.progressVP, cmd = m.progressVP.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) nodeFromRow() (node, bool) {
	row := m.nodeTable.SelectedRow()
	if row == nil {
		return node{}, false
	}
	for _, n := range m.allNodes {
		if n.name == row[0] {
			return n, true
		}
	}
	return node{}, false
}

func (m model) promptAction(a actionKind) (model, tea.Cmd) {
	if n, ok := m.nodeFromRow(); ok {
		m.selected = n
		m.action = a
		m.state = stateConfirm
	}
	return m, nil
}

func (m model) startAction() (model, tea.Cmd) {
	m.state = stateProgress
	m.progressLines = nil
	m.progressDone = false
	m.progressErr = nil
	m.progressVP = viewport.New(m.width, m.height-4)
	ch := make(chan progressLineMsg, 256)
	m.progressCh = ch
	return m, tea.Batch(runAction(m.action, m.selected.name, ch), waitProgress(ch))
}

// ─── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.err != "" {
		return titleStyle.Render("Error") + "\n\n  " + m.err + "\n\n" + helpStyle.Render("q quit")
	}
	switch m.state {
	case stateList, stateListSearch:
		return m.listView()
	case stateDetail:
		return m.detailView()
	case stateConfirm:
		return m.confirmView()
	case stateProgress:
		return m.progressView()
	}
	return ""
}

func (m model) listView() string {
	title := titleStyle.Render(fmt.Sprintf("Nodes  [%d]", len(m.allNodes)))
	var searchBar string
	if m.state == stateListSearch {
		searchBar = " " + m.nodeSearch.View()
	} else if q := m.nodeSearch.Value(); q != "" {
		searchBar = dimStyle.Render(" filter: ") + q
	} else {
		searchBar = dimStyle.Render(" / to search")
	}
	help := helpStyle.Render("↑/↓ navigate · enter/s ssh · i detail · / search · d drain · c cordon · u uncordon · r refresh · q quit")
	return title + "\n" + searchBar + "\n" + m.nodeTable.View() + "\n" + help
}

func (m model) detailView() string {
	met := m.metrics[m.selected.name]
	cpuInfo := ""
	if met.cpu != "" {
		cpuInfo = "  " + dimStyle.Render("CPU:") + colorPct(met.cpuPct, cpuHighStyle, cpuMidStyle) +
			dimStyle.Render(" ("+met.cpu+")") +
			"  " + dimStyle.Render("MEM:") + colorPct(met.memPct, memHighStyle, memMidStyle) +
			dimStyle.Render(" ("+met.mem+")")
	}
	header := titleStyle.Render("Node: ") +
		lipgloss.NewStyle().Bold(true).Render(m.selected.name) +
		"  " + colorStatus(m.selected.status) +
		"  " + dimStyle.Render(m.selected.ip) +
		cpuInfo +
		dimStyle.Render(fmt.Sprintf("  [%d pods]", len(m.podRows)))
	help := helpStyle.Render("↑/↓ navigate · s ssh · d drain · c cordon · u uncordon · r refresh · b/esc back · q quit")
	return header + "\n" + m.podTable.View() + "\n" + help
}

func (m model) confirmView() string {
	action := strings.ToUpper(m.action.label())
	var warning string
	if m.action == actionDrain {
		warning = "\n\n  " + lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(
			"⚠  Pods will be evicted. DaemonSets and emptyDir data ignored.",
		)
	}
	box := confirmStyle.Render(
		titleStyle.Render(action+" node?") + "\n\n" +
			"  Node:   " + lipgloss.NewStyle().Bold(true).Render(m.selected.name) + "\n" +
			"  Status: " + colorStatus(m.selected.status) +
			warning + "\n\n" +
			lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render("  [y]") +
			" confirm    " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("[n]") +
			" cancel",
	)
	boxLines := strings.Split(box, "\n")
	top := (m.height - len(boxLines)) / 2
	left := (m.width - lipgloss.Width(box)) / 2
	if top < 0 {
		top = 0
	}
	if left < 0 {
		left = 0
	}
	var sb strings.Builder
	sb.WriteString(strings.Repeat("\n", top))
	indent := strings.Repeat(" ", left)
	for _, l := range boxLines {
		sb.WriteString(indent + l + "\n")
	}
	return sb.String()
}

func (m model) progressView() string {
	title := titleStyle.Render(strings.ToUpper(m.action.label()) + ": " + m.selected.name)
	body := m.progressVP.View()
	var footer string
	if m.progressDone {
		if m.progressErr != nil {
			footer = progressErrStyle.Render(" FAILED: "+m.progressErr.Error()) + "  " + helpStyle.Render("esc/b back")
		} else {
			footer = progressOkStyle.Render(" DONE") + "  " + helpStyle.Render("esc/b back")
		}
	} else {
		footer = dimStyle.Render(" running...")
	}
	return title + "\n" + body + "\n" + footer
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func (m *model) applyNodeSearch() {
	q := strings.ToLower(m.nodeSearch.Value())
	if q == "" {
		m.nodeTable.SetRows(m.buildNodeRows())
		return
	}
	var filtered []node
	for _, n := range m.allNodes {
		if strings.Contains(strings.ToLower(n.name), q) ||
			strings.Contains(strings.ToLower(n.roles), q) ||
			strings.Contains(n.ip, q) {
			filtered = append(filtered, n)
		}
	}
	// build rows for filtered subset with metrics
	rows := make([]table.Row, len(filtered))
	for i, n := range filtered {
		cpu, cpuPct, mem, memPct := "-", "-", "-", "-"
		if met, ok := m.metrics[n.name]; ok {
			cpu, cpuPct, mem, memPct = met.cpu, met.cpuPct, met.mem, met.memPct
		}
		rows[i] = table.Row{n.name, n.status, n.roles, n.ip, cpu, cpuPct, mem, memPct, n.age}
	}
	m.nodeTable.SetRows(rows)
}

// ─── commands ─────────────────────────────────────────────────────────────────

func fetchTopNodes() tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("kubectl", "top", "nodes", "--no-headers").Output()
		if err != nil {
			// metrics server may not be installed — return empty, not an error
			return topNodesMsg{metrics: map[string]nodeMetric{}}
		}
		return topNodesMsg{metrics: parseTopNodes(out)}
	}
}

func fetchPods(nodeName string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("kubectl", "get", "pods",
			"--all-namespaces",
			"--field-selector", "spec.nodeName="+nodeName,
			"-o", "json",
		).Output()
		if err != nil {
			return errMsg{err}
		}
		var list k8sPodList
		if err := json.Unmarshal(out, &list); err != nil {
			return errMsg{err}
		}
		rows := make([]podRow, len(list.Items))
		for i, item := range list.Items {
			restarts := 0
			for _, cs := range item.Status.ContainerStatuses {
				restarts += cs.RestartCount
			}
			rows[i] = podRow{
				namespace: item.Metadata.Namespace,
				name:      item.Metadata.Name,
				status:    item.Status.Phase,
				restarts:  fmt.Sprintf("%d", restarts),
				age:       humanAge(item.Metadata.CreationTimestamp),
			}
		}
		return podsLoadedMsg{rows: rows}
	}
}

func reloadNodes() tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("kubectl", "get", "nodes", "-o", "json").Output()
		if err != nil {
			return errMsg{err}
		}
		nodes, err := parseNodes(out)
		if err != nil {
			return errMsg{err}
		}
		return nodesLoadedMsg{nodes: nodes}
	}
}

func runAction(action actionKind, nodeName string, ch chan<- progressLineMsg) tea.Cmd {
	return func() tea.Msg {
		var args []string
		switch action {
		case actionDrain:
			args = []string{"drain", nodeName, "--ignore-daemonsets", "--delete-emptydir-data"}
		case actionCordon:
			args = []string{"cordon", nodeName}
		case actionUncordon:
			args = []string{"uncordon", nodeName}
		}
		cmd := exec.Command("kubectl", args...)
		stdout, _ := cmd.StdoutPipe()
		stderr, _ := cmd.StderrPipe()
		if err := cmd.Start(); err != nil {
			close(ch)
			return actionDoneMsg{err: err}
		}
		done := make(chan struct{}, 2)
		go func() {
			sc := bufio.NewScanner(stdout)
			for sc.Scan() {
				ch <- progressLineMsg{line: progressOkStyle.Render(sc.Text())}
			}
			done <- struct{}{}
		}()
		go func() {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				ch <- progressLineMsg{line: progressErrStyle.Render(sc.Text()), isErr: true}
			}
			done <- struct{}{}
		}()
		<-done
		<-done
		err := cmd.Wait()
		close(ch)
		return actionDoneMsg{err: err}
	}
}

func waitProgress(ch chan progressLineMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func sshCmd(ip string) tea.Cmd {
	return tea.ExecProcess(exec.Command("ssh", ip), func(err error) tea.Msg {
		return sshDoneMsg{err: err}
	})
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	nodeName := parseArgs(os.Args[1:])

	out, err := exec.Command("kubectl", "get", "nodes", "-o", "json").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kubectl get nodes:", err)
		os.Exit(1)
	}
	nodes, err := parseNodes(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse nodes:", err)
		os.Exit(1)
	}
	if len(nodes) == 0 {
		fmt.Fprintln(os.Stderr, "no nodes found")
		os.Exit(1)
	}

	var initial *node
	if nodeName != "" {
		for i, n := range nodes {
			if n.name == nodeName {
				initial = &nodes[i]
				break
			}
		}
		if initial == nil {
			fmt.Fprintf(os.Stderr, "node %q not found\n", nodeName)
			os.Exit(1)
		}
	}

	p := tea.NewProgram(newModel(nodes, initial), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseArgs(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return ""
}
