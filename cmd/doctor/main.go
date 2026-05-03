package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── styles ───────────────────────────────────────────────────────────────────

var (
	criticalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	warningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	boldStyle     = lipgloss.NewStyle().Bold(true)
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).PaddingLeft(1)
	llmStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("117"))

	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			PaddingLeft(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236")).
			PaddingLeft(1).
			PaddingRight(1)

	colHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Bold(true)

	rowAltStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

// ─── severity ─────────────────────────────────────────────────────────────────

type severity int

const (
	severityOK severity = iota
	severityWarning
	severityCritical
)

func (s severity) badge() string {
	switch s {
	case severityCritical:
		return criticalStyle.Render("● CRITICAL")
	case severityWarning:
		return warningStyle.Render("● WARNING")
	default:
		return okStyle.Render("● OK")
	}
}

func maxSeverity(a, b severity) severity {
	if a > b {
		return a
	}
	return b
}

// ─── k8s types ────────────────────────────────────────────────────────────────

type k8sNodeList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Unschedulable bool `json:"unschedulable"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"conditions"`
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
				Name         string `json:"name"`
				RestartCount int    `json:"restartCount"`
				State        struct {
					Waiting struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"waiting"`
					Terminated struct {
						Reason string `json:"reason"`
					} `json:"terminated"`
				} `json:"state"`
				LastState struct {
					Terminated struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"terminated"`
				} `json:"lastState"`
			} `json:"containerStatuses"`
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type k8sEventList struct {
	Items []struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		InvolvedObject struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"involvedObject"`
		Reason        string    `json:"reason"`
		Message       string    `json:"message"`
		Count         int       `json:"count"`
		Type          string    `json:"type"`
		LastTimestamp time.Time `json:"lastTimestamp"`
	} `json:"items"`
}

// ─── report types ─────────────────────────────────────────────────────────────

type nodeProblem struct {
	name       string
	conditions []string
	severity   severity
}

type podProblem struct {
	namespace string
	name      string
	reason    string
	restarts  int
	age       string
	severity  severity
	message   string
}

type eventItem struct {
	namespace string
	object    string
	reason    string
	message   string
	count     int
	age       string
	severity  severity
}

type clusterReport struct {
	nodeProblems []nodeProblem
	podProblems  []podProblem
	events       []eventItem
	overall      severity
	updatedAt    time.Time
}

// ─── app / llm state ──────────────────────────────────────────────────────────

type appState int

const (
	stateLoading appState = iota
	stateReport
	stateError
)

type llmState int

const (
	llmIdle llmState = iota
	llmAnalyzing
	llmDone
	llmFailed
)

// ─── messages ─────────────────────────────────────────────────────────────────

type reportMsg struct{ report clusterReport }
type tickMsg time.Time
type errMsg struct{ err error }
type llmMsg struct{ text string }
type llmErrMsg struct{ err error }

// ─── model ────────────────────────────────────────────────────────────────────

type model struct {
	state      appState
	report     clusterReport
	viewport   viewport.Model
	spinner    spinner.Model
	width      int
	height     int
	err        string
	llmMode    string // "claude" | "codex" | ""
	llmState   llmState
	llmText    string
	llmErr     string
	pendingLLM bool
}

func newModel(llmMode string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return model{
		state:      stateLoading,
		spinner:    sp,
		llmMode:    llmMode,
		pendingLLM: llmMode != "",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchReport(),
		tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

// ─── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.viewport = viewport.New(m.width, m.height-3)
		if m.state == stateReport {
			m.viewport.SetContent(m.renderReport())
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			m.state = stateLoading
			m.llmState = llmIdle
			m.llmText = ""
			m.llmErr = ""
			m.pendingLLM = m.llmMode != ""
			return m, tea.Batch(m.spinner.Tick, fetchReport())
		}
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		return m, tea.Batch(
			fetchReport(),
			tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)

	case reportMsg:
		m.report = msg.report
		m.state = stateReport
		m.viewport = viewport.New(m.width, m.height-3)
		var cmd tea.Cmd
		if m.pendingLLM {
			m.llmState = llmAnalyzing
			m.pendingLLM = false
			cmd = fetchLLMAnalysis(m.report, m.llmMode)
		}
		m.viewport.SetContent(m.renderReport())
		return m, cmd

	case llmMsg:
		m.llmState = llmDone
		m.llmText = msg.text
		m.viewport.SetContent(m.renderReport())
		m.viewport.GotoBottom()

	case llmErrMsg:
		m.llmState = llmFailed
		m.llmErr = msg.err.Error()
		m.viewport.SetContent(m.renderReport())

	case errMsg:
		m.state = stateError
		m.err = msg.err.Error()
	}

	return m, nil
}

// ─── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.state {
	case stateLoading:
		return m.spinner.View() + " Diagnosing cluster...\n"
	case stateError:
		return criticalStyle.Render("Error: ") + m.err + "\n\n" + helpStyle.Render("q quit")
	case stateReport:
		header := m.renderHeader()
		help := helpStyle.Render("↑/↓/PgUp/PgDn scroll · r refresh · q quit")
		return header + "\n" + m.viewport.View() + "\n" + help
	}
	return ""
}

func (m model) renderHeader() string {
	overall := m.report.overall
	ts := dimStyle.Render("updated " + m.report.updatedAt.Format("15:04:05"))
	nc := len(m.report.nodeProblems)
	pc := len(m.report.podProblems)
	ec := len(m.report.events)
	counts := dimStyle.Render(fmt.Sprintf("nodes:%d  pods:%d  events:%d", nc, pc, ec))

	llmStatus := ""
	if m.llmMode != "" {
		switch m.llmState {
		case llmAnalyzing:
			llmStatus = "  " + m.spinner.View() + dimStyle.Render(" AI analyzing...")
		case llmDone:
			llmStatus = "  " + llmStyle.Render("AI ✓")
		case llmFailed:
			llmStatus = "  " + criticalStyle.Render("AI ✗")
		}
	}

	return headerStyle.Render(
		boldStyle.Render("Cluster Doctor") + "  " + overall.badge() + "  " + counts + "  " + ts + llmStatus,
	)
}

func (m model) renderReport() string {
	var sb strings.Builder
	sb.WriteString(m.renderNodes())
	sb.WriteString("\n")
	sb.WriteString(m.renderPods())
	sb.WriteString("\n")
	sb.WriteString(m.renderEvents())
	if m.llmMode != "" {
		sb.WriteString("\n")
		sb.WriteString(m.renderLLMSection())
	}
	return sb.String()
}

func (m model) renderLLMSection() string {
	var sb strings.Builder
	label := strings.ToUpper(m.llmMode)
	sb.WriteString(sectionStyle.Render(fmt.Sprintf("AI Analysis (%s)", label)) + "\n")
	switch m.llmState {
	case llmAnalyzing:
		sb.WriteString(dimStyle.Render("  Analyzing...") + "\n")
	case llmDone:
		for _, line := range strings.Split(m.llmText, "\n") {
			sb.WriteString("  " + llmStyle.Render(line) + "\n")
		}
	case llmFailed:
		sb.WriteString(criticalStyle.Render("  Error: ") + m.llmErr + "\n")
	}
	return sb.String()
}

func (m model) renderNodes() string {
	var sb strings.Builder
	count := len(m.report.nodeProblems)
	label := "OK"
	if count > 0 {
		label = fmt.Sprintf("%d issue(s)", count)
	}
	sb.WriteString(sectionStyle.Render(fmt.Sprintf("Nodes  [%s]", label)) + "\n")

	if count == 0 {
		sb.WriteString(dimStyle.Render("  All nodes healthy\n"))
		return sb.String()
	}

	cols := []int{m.width - 24, 22}
	sb.WriteString(renderColHeader(cols, "NAME", "CONDITIONS") + "\n")
	for i, n := range m.report.nodeProblems {
		conds := strings.Join(n.conditions, ", ")
		st := warningStyle
		if n.severity == severityCritical {
			st = criticalStyle
		}
		row := fmt.Sprintf("  %-*s  %s", cols[0]-2, truncate(n.name, cols[0]-4), st.Render(truncate(conds, cols[1])))
		if i%2 == 1 {
			row = rowAltStyle.Render(row)
		}
		sb.WriteString(row + "\n")
	}
	return sb.String()
}

func (m model) renderPods() string {
	var sb strings.Builder
	count := len(m.report.podProblems)
	label := "OK"
	if count > 0 {
		label = fmt.Sprintf("%d issue(s)", count)
	}
	sb.WriteString(sectionStyle.Render(fmt.Sprintf("Problem Pods  [%s]", label)) + "\n")

	if count == 0 {
		sb.WriteString(dimStyle.Render("  All pods healthy\n"))
		return sb.String()
	}

	nsW, nameW, reasonW, restW, ageW := 14, 36, 20, 8, 6
	sb.WriteString(renderColHeader([]int{nsW, nameW, reasonW, restW, ageW},
		"NAMESPACE", "NAME", "REASON", "RESTARTS", "AGE") + "\n")

	for i, p := range m.report.podProblems {
		st := warningStyle
		if p.severity == severityCritical {
			st = criticalStyle
		}
		row := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s",
			nsW-2, truncate(p.namespace, nsW-2),
			nameW, truncate(p.name, nameW),
			reasonW, st.Render(truncate(p.reason, reasonW)),
			restW, fmt.Sprintf("%d", p.restarts),
			ageW, p.age,
		)
		if i%2 == 1 {
			row = rowAltStyle.Render(row)
		}
		sb.WriteString(row + "\n")
		if p.message != "" {
			sb.WriteString(dimStyle.Render("    └ "+truncate(p.message, m.width-6)) + "\n")
		}
	}
	return sb.String()
}

func (m model) renderEvents() string {
	var sb strings.Builder
	count := len(m.report.events)
	label := "none"
	if count > 0 {
		label = fmt.Sprintf("%d", count)
	}
	sb.WriteString(sectionStyle.Render(fmt.Sprintf("Warning Events (last 1h)  [%s]", label)) + "\n")

	if count == 0 {
		sb.WriteString(dimStyle.Render("  No warning events\n"))
		return sb.String()
	}

	nsW, objW, reasonW, ageW := 14, 36, 18, 6
	msgW := m.width - nsW - objW - reasonW - ageW - 12
	if msgW < 20 {
		msgW = 20
	}
	sb.WriteString(renderColHeader([]int{nsW, objW, reasonW, msgW, ageW},
		"NAMESPACE", "OBJECT", "REASON", "MESSAGE", "AGE") + "\n")

	for i, e := range m.report.events {
		countStr := ""
		if e.count > 1 {
			countStr = fmt.Sprintf("x%d ", e.count)
		}
		row := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %-*s",
			nsW-2, truncate(e.namespace, nsW-2),
			objW, truncate(e.object, objW),
			reasonW, warningStyle.Render(truncate(e.reason, reasonW)),
			msgW, truncate(countStr+e.message, msgW),
			ageW, e.age,
		)
		if i%2 == 1 {
			row = rowAltStyle.Render(row)
		}
		sb.WriteString(row + "\n")
	}
	return sb.String()
}

func renderColHeader(widths []int, titles ...string) string {
	var sb strings.Builder
	sb.WriteString("  ")
	for i, t := range titles {
		if i < len(widths) {
			sb.WriteString(colHeaderStyle.Render(fmt.Sprintf("%-*s", widths[i], t)))
			if i < len(titles)-1 {
				sb.WriteString("  ")
			}
		}
	}
	return sb.String()
}

// ─── LLM ─────────────────────────────────────────────────────────────────────

func fetchLLMAnalysis(report clusterReport, mode string) tea.Cmd {
	return func() tea.Msg {
		text, err := runLLM(report, mode)
		if err != nil {
			return llmErrMsg{err}
		}
		return llmMsg{text}
	}
}

func runLLM(report clusterReport, mode string) (string, error) {
	prompt := buildPrompt(report)
	var cmd *exec.Cmd
	switch mode {
	case "claude":
		cmd = exec.Command("claude", "-p", "--bare", prompt)
	case "codex":
		cmd = exec.Command("codex", "exec", prompt)
	default:
		return "", fmt.Errorf("unknown LLM mode: %s", mode)
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func buildPrompt(r clusterReport) string {
	var sb strings.Builder
	sb.WriteString("You are a Kubernetes SRE. Analyze the following cluster diagnostic report and provide:\n")
	sb.WriteString("1. A brief summary of the most critical issues\n")
	sb.WriteString("2. Likely root causes\n")
	sb.WriteString("3. Recommended remediation steps\n\n")
	sb.WriteString("--- CLUSTER DIAGNOSTIC REPORT ---\n")
	sb.WriteString(fmt.Sprintf("Timestamp: %s\n", r.updatedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("Overall severity: %s\n\n", severityText(r.overall)))

	sb.WriteString("== NODES ==\n")
	if len(r.nodeProblems) == 0 {
		sb.WriteString("All nodes healthy\n")
	} else {
		for _, n := range r.nodeProblems {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", n.name, strings.Join(n.conditions, ", ")))
		}
	}

	sb.WriteString("\n== PROBLEM PODS ==\n")
	if len(r.podProblems) == 0 {
		sb.WriteString("All pods healthy\n")
	} else {
		for _, p := range r.podProblems {
			line := fmt.Sprintf("  %s/%s — %s (restarts: %d, age: %s)", p.namespace, p.name, p.reason, p.restarts, p.age)
			if p.message != "" {
				line += "\n    " + p.message
			}
			sb.WriteString(line + "\n")
		}
	}

	sb.WriteString("\n== WARNING EVENTS (last 1h) ==\n")
	if len(r.events) == 0 {
		sb.WriteString("No warning events\n")
	} else {
		for _, e := range r.events {
			sb.WriteString(fmt.Sprintf("  [%s] %s — %s: %s (count: %d)\n",
				e.namespace, e.object, e.reason, e.message, e.count))
		}
	}
	return sb.String()
}

func severityText(s severity) string {
	switch s {
	case severityCritical:
		return "CRITICAL"
	case severityWarning:
		return "WARNING"
	default:
		return "OK"
	}
}

// ─── data fetching ────────────────────────────────────────────────────────────

func fetchReport() tea.Cmd {
	return func() tea.Msg {
		report, err := buildReport()
		if err != nil {
			return errMsg{err}
		}
		return reportMsg{report: report}
	}
}

func buildReport() (clusterReport, error) {
	type result struct {
		nodes  []byte
		pods   []byte
		events []byte
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		var r result
		var err error
		r.nodes, err = exec.Command("kubectl", "get", "nodes", "-o", "json").Output()
		if err != nil {
			ch <- result{err: fmt.Errorf("get nodes: %w", err)}
			return
		}
		r.pods, err = exec.Command("kubectl", "get", "pods", "--all-namespaces", "-o", "json").Output()
		if err != nil {
			ch <- result{err: fmt.Errorf("get pods: %w", err)}
			return
		}
		r.events, err = exec.Command("kubectl", "get", "events",
			"--all-namespaces",
			"--field-selector", "type=Warning",
			"-o", "json",
		).Output()
		if err != nil {
			ch <- result{err: fmt.Errorf("get events: %w", err)}
			return
		}
		ch <- r
	}()
	r := <-ch
	if r.err != nil {
		return clusterReport{}, r.err
	}

	var nodeList k8sNodeList
	if err := json.Unmarshal(r.nodes, &nodeList); err != nil {
		return clusterReport{}, err
	}
	var podList k8sPodList
	if err := json.Unmarshal(r.pods, &podList); err != nil {
		return clusterReport{}, err
	}
	var eventList k8sEventList
	if err := json.Unmarshal(r.events, &eventList); err != nil {
		return clusterReport{}, err
	}

	report := clusterReport{updatedAt: time.Now()}
	overall := severityOK

	// ── nodes ──────────────────────────────────────────────────────────────────
	for _, item := range nodeList.Items {
		var conds []string
		sev := severityOK
		if item.Spec.Unschedulable {
			conds = append(conds, "SchedulingDisabled")
			sev = maxSeverity(sev, severityWarning)
		}
		for _, c := range item.Status.Conditions {
			if c.Type == "Ready" && c.Status != "True" {
				conds = append(conds, "NotReady")
				sev = maxSeverity(sev, severityCritical)
			}
			if c.Type != "Ready" && c.Status == "True" {
				conds = append(conds, c.Type)
				sev = maxSeverity(sev, severityWarning)
			}
		}
		if len(conds) > 0 {
			report.nodeProblems = append(report.nodeProblems, nodeProblem{
				name:       item.Metadata.Name,
				conditions: conds,
				severity:   sev,
			})
			overall = maxSeverity(overall, sev)
		}
	}

	// ── pods ───────────────────────────────────────────────────────────────────
	now := time.Now()
	for _, item := range podList.Items {
		age := humanAge(item.Metadata.CreationTimestamp)

		for _, cs := range item.Status.ContainerStatuses {
			reason := cs.State.Waiting.Reason
			msg := cs.State.Waiting.Message
			sev := severityWarning

			if cs.LastState.Terminated.Reason == "OOMKilled" && reason == "" {
				reason = "OOMKilled"
				msg = cs.LastState.Terminated.Message
				sev = severityCritical
			}

			switch reason {
			case "CrashLoopBackOff":
				sev = severityCritical
			case "OOMKilled":
				sev = severityCritical
			case "ImagePullBackOff", "ErrImagePull":
				sev = severityWarning
			case "":
				if cs.State.Terminated.Reason == "OOMKilled" {
					reason = "OOMKilled"
					sev = severityCritical
				} else {
					continue
				}
			}

			report.podProblems = append(report.podProblems, podProblem{
				namespace: item.Metadata.Namespace,
				name:      item.Metadata.Name,
				reason:    reason,
				restarts:  cs.RestartCount,
				age:       age,
				severity:  sev,
				message:   truncate(msg, 120),
			})
			overall = maxSeverity(overall, sev)
		}

		switch item.Status.Phase {
		case "Failed":
			report.podProblems = append(report.podProblems, podProblem{
				namespace: item.Metadata.Namespace,
				name:      item.Metadata.Name,
				reason:    "Failed",
				age:       age,
				severity:  severityCritical,
			})
			overall = maxSeverity(overall, severityCritical)
		case "Pending":
			if now.Sub(item.Metadata.CreationTimestamp) > 5*time.Minute {
				msg := ""
				for _, c := range item.Status.Conditions {
					if c.Type == "PodScheduled" && c.Status == "False" {
						msg = c.Message
					}
				}
				report.podProblems = append(report.podProblems, podProblem{
					namespace: item.Metadata.Namespace,
					name:      item.Metadata.Name,
					reason:    "Pending",
					age:       age,
					severity:  severityWarning,
					message:   truncate(msg, 120),
				})
				overall = maxSeverity(overall, severityWarning)
			}
		}
	}

	report.podProblems = dedupPodProblems(report.podProblems)

	sort.Slice(report.podProblems, func(i, j int) bool {
		if report.podProblems[i].severity != report.podProblems[j].severity {
			return report.podProblems[i].severity > report.podProblems[j].severity
		}
		return report.podProblems[i].namespace+report.podProblems[i].name <
			report.podProblems[j].namespace+report.podProblems[j].name
	})

	// ── events ─────────────────────────────────────────────────────────────────
	cutoff := now.Add(-1 * time.Hour)
	seen := map[string]bool{}
	for _, item := range eventList.Items {
		if item.LastTimestamp.Before(cutoff) {
			continue
		}
		key := item.InvolvedObject.Namespace + "/" + item.InvolvedObject.Kind + "/" +
			item.InvolvedObject.Name + "/" + item.Reason
		if seen[key] {
			continue
		}
		seen[key] = true
		report.events = append(report.events, eventItem{
			namespace: item.InvolvedObject.Namespace,
			object:    item.InvolvedObject.Kind + "/" + item.InvolvedObject.Name,
			reason:    item.Reason,
			message:   item.Message,
			count:     item.Count,
			age:       humanAge(item.LastTimestamp),
			severity:  severityWarning,
		})
		overall = maxSeverity(overall, severityWarning)
	}

	report.overall = overall
	return report, nil
}

func dedupPodProblems(problems []podProblem) []podProblem {
	seen := map[string]bool{}
	var out []podProblem
	for _, p := range problems {
		key := p.namespace + "/" + p.name + "/" + p.reason
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func humanAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
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

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	llmMode := ""
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--claude":
			llmMode = "claude"
		case "--codex":
			llmMode = "codex"
		}
	}

	p := tea.NewProgram(newModel(llmMode), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
