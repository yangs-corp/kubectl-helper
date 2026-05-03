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
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── styles ───────────────────────────────────────────────────────────────────

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).PaddingLeft(1)
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).PaddingLeft(1)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	warningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	normalStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	altRowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	headerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Bold(true)
)

// ─── fixed column widths ──────────────────────────────────────────────────────

const (
	colTypeWidth      = 9
	colNamespaceWidth = 14
	colObjectWidth    = 34
	colReasonWidth    = 18
	colCountWidth     = 6
	colAgeWidth       = 6
	// fixed total excluding message: 9+14+34+18+6+6 = 87
	// separators between 7 columns: 6 * 2 = 12
	colFixedTotal = colTypeWidth + colNamespaceWidth + colObjectWidth + colReasonWidth + colCountWidth + colAgeWidth + 12
)

// ─── k8s types ────────────────────────────────────────────────────────────────

type k8sEventList struct {
	Items []struct {
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

// ─── event row ────────────────────────────────────────────────────────────────

type eventRow struct {
	typ       string
	namespace string
	object    string
	reason    string
	message   string
	count     int
	age       string
	ts        time.Time
}

// ─── app state ────────────────────────────────────────────────────────────────

type appState int

const (
	stateLoading appState = iota
	stateList
	stateSearch
	stateError
)

// ─── messages ─────────────────────────────────────────────────────────────────

type eventsLoadedMsg struct{ rows []eventRow }
type tickMsg time.Time
type errMsg struct{ err error }

// ─── model ────────────────────────────────────────────────────────────────────

type model struct {
	state       appState
	namespace   string // "" = all namespaces
	allRows     []eventRow
	spinner     spinner.Model
	searchInput textinput.Model
	searchQuery string
	warningOnly bool
	width       int
	height      int
	scrollOffset int
	errText     string
	updatedAt   time.Time
}

func newModel(namespace string) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	si := textinput.New()
	si.Prompt = dimStyle.Render("/") + " "
	si.Placeholder = "filter events..."
	si.CharLimit = 128

	return model{
		state:     stateLoading,
		namespace: namespace,
		spinner:   sp,
		searchInput: si,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		fetchEvents(m.namespace),
		tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

// ─── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		return m, tea.Batch(
			fetchEvents(m.namespace),
			tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)

	case eventsLoadedMsg:
		m.allRows = msg.rows
		m.updatedAt = time.Now()
		m.state = stateList
		m.scrollOffset = 0
		return m, nil

	case errMsg:
		m.state = stateError
		m.errText = msg.err.Error()
		return m, nil
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

	// ── loading ───────────────────────────────────────────────────────────────
	case stateLoading:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}

	// ── event list ────────────────────────────────────────────────────────────
	case stateList:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "/":
			m.state = stateSearch
			m.searchInput.SetValue(m.searchQuery)
			m.searchInput.Focus()
			return m, textinput.Blink
		case "esc":
			m.searchQuery = ""
			m.searchInput.SetValue("")
			m.scrollOffset = 0
			return m, nil
		case "w":
			m.warningOnly = !m.warningOnly
			m.scrollOffset = 0
			return m, nil
		case "r":
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, fetchEvents(m.namespace))
		case "up", "k":
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
		case "down", "j":
			visible := m.visibleRows()
			maxScroll := len(visible) - m.tableHeight()
			if maxScroll > 0 && m.scrollOffset < maxScroll {
				m.scrollOffset++
			}
		case "pgup":
			m.scrollOffset -= m.tableHeight()
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}
		case "pgdown":
			visible := m.visibleRows()
			maxScroll := len(visible) - m.tableHeight()
			m.scrollOffset += m.tableHeight()
			if maxScroll <= 0 {
				m.scrollOffset = 0
			} else if m.scrollOffset > maxScroll {
				m.scrollOffset = maxScroll
			}
		}

	// ── search input ──────────────────────────────────────────────────────────
	case stateSearch:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			m.searchInput.Blur()
			m.searchInput.SetValue("")
			m.searchQuery = ""
			m.state = stateList
			m.scrollOffset = 0
			return m, nil
		case "enter":
			m.searchQuery = m.searchInput.Value()
			m.searchInput.Blur()
			m.state = stateList
			m.scrollOffset = 0
			return m, nil
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(msg)
			m.searchQuery = m.searchInput.Value()
			m.scrollOffset = 0
			return m, cmd
		}

	// ── error ─────────────────────────────────────────────────────────────────
	case stateError:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "r":
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, fetchEvents(m.namespace))
		}
	}

	return m, nil
}

// ─── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.state {
	case stateLoading:
		return m.spinner.View() + " Loading events...\n"
	case stateError:
		return titleStyle.Render("Error") + "\n\n  " + m.errText + "\n\n" + helpStyle.Render("r retry · q quit")
	case stateList, stateSearch:
		return m.listView()
	}
	return ""
}

func (m model) listView() string {
	ns := m.namespace
	if ns == "" {
		ns = "all namespaces"
	}

	visible := m.visibleRows()
	countInfo := fmt.Sprintf("%d", len(visible))
	if m.warningOnly || m.searchQuery != "" {
		countInfo = fmt.Sprintf("%d/%d", len(visible), len(m.allRows))
	}

	title := titleStyle.Render(fmt.Sprintf("Events  [%s]  %s", ns, countInfo))
	ts := dimStyle.Render("updated " + m.updatedAt.Format("15:04:05"))

	var filterBar string
	switch m.state {
	case stateSearch:
		filterBar = " " + m.searchInput.View()
	default:
		var parts []string
		if m.searchQuery != "" {
			parts = append(parts, dimStyle.Render("filter:")+m.searchQuery)
		}
		if m.warningOnly {
			parts = append(parts, warningStyle.Render("warning-only"))
		}
		if len(parts) > 0 {
			filterBar = " " + strings.Join(parts, "  ")
		} else {
			filterBar = dimStyle.Render(" / to filter")
		}
	}

	header := m.renderHeader()
	body := m.renderRows(visible)

	help := helpStyle.Render("/ filter · w warning · r refresh · esc clear · q quit")

	return title + "  " + ts + "\n" + filterBar + "\n" + header + "\n" + body + "\n" + help
}

func (m model) tableHeight() int {
	// title + filterBar + header + help = 4 lines
	h := m.height - 4
	if h < 1 {
		h = 1
	}
	return h
}

func (m model) msgColWidth() int {
	w := m.width - colFixedTotal
	if w < 20 {
		w = 20
	}
	return w
}

func (m model) renderHeader() string {
	msgW := m.msgColWidth()
	return "  " +
		headerStyle.Render(fmt.Sprintf("%-*s", colTypeWidth, "TYPE")) + "  " +
		headerStyle.Render(fmt.Sprintf("%-*s", colNamespaceWidth, "NAMESPACE")) + "  " +
		headerStyle.Render(fmt.Sprintf("%-*s", colObjectWidth, "OBJECT")) + "  " +
		headerStyle.Render(fmt.Sprintf("%-*s", colReasonWidth, "REASON")) + "  " +
		headerStyle.Render(fmt.Sprintf("%-*s", msgW, "MESSAGE")) + "  " +
		headerStyle.Render(fmt.Sprintf("%-*s", colCountWidth, "COUNT")) + "  " +
		headerStyle.Render(fmt.Sprintf("%-*s", colAgeWidth, "AGE"))
}

func (m model) renderRows(rows []eventRow) string {
	if len(rows) == 0 {
		return dimStyle.Render("  No events found")
	}

	msgW := m.msgColWidth()
	tableH := m.tableHeight()

	start := m.scrollOffset
	if start < 0 {
		start = 0
	}
	end := start + tableH
	if end > len(rows) {
		end = len(rows)
	}

	var sb strings.Builder
	for i, row := range rows[start:end] {
		absIdx := start + i
		line := m.renderRow(row, msgW)
		if absIdx%2 == 1 {
			line = altRowStyle.Render(line)
		}
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

func (m model) renderRow(row eventRow, msgW int) string {
	var typCell string
	if row.typ == "Warning" {
		typCell = warningStyle.Render(fmt.Sprintf("%-*s", colTypeWidth, truncate(row.typ, colTypeWidth)))
	} else {
		typCell = normalStyle.Render(fmt.Sprintf("%-*s", colTypeWidth, truncate(row.typ, colTypeWidth)))
	}

	countStr := "-"
	if row.count > 0 {
		countStr = fmt.Sprintf("%d", row.count)
	}

	return "  " + typCell + "  " +
		fmt.Sprintf("%-*s", colNamespaceWidth, truncate(row.namespace, colNamespaceWidth)) + "  " +
		fmt.Sprintf("%-*s", colObjectWidth, truncate(row.object, colObjectWidth)) + "  " +
		fmt.Sprintf("%-*s", colReasonWidth, truncate(row.reason, colReasonWidth)) + "  " +
		fmt.Sprintf("%-*s", msgW, truncate(row.message, msgW)) + "  " +
		fmt.Sprintf("%-*s", colCountWidth, truncate(countStr, colCountWidth)) + "  " +
		fmt.Sprintf("%-*s", colAgeWidth, truncate(row.age, colAgeWidth))
}

// ─── filtering ────────────────────────────────────────────────────────────────

func (m model) visibleRows() []eventRow {
	var out []eventRow
	q := strings.ToLower(m.searchQuery)
	for _, row := range m.allRows {
		if m.warningOnly && row.typ != "Warning" {
			continue
		}
		if q != "" {
			haystack := strings.ToLower(row.typ + " " + row.namespace + " " + row.object +
				" " + row.reason + " " + row.message)
			if !strings.Contains(haystack, q) {
				continue
			}
		}
		out = append(out, row)
	}
	return out
}

// ─── data fetching ────────────────────────────────────────────────────────────

func fetchEvents(namespace string) tea.Cmd {
	return func() tea.Msg {
		var args []string
		if namespace != "" {
			args = []string{"-n", namespace, "get", "events", "-o", "json"}
		} else {
			args = []string{"get", "events", "--all-namespaces", "-o", "json"}
		}
		out, err := exec.Command("kubectl", args...).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
				return errMsg{fmt.Errorf("kubectl get events: %s", strings.TrimSpace(string(ee.Stderr)))}
			}
			return errMsg{fmt.Errorf("kubectl get events: %w", err)}
		}
		var list k8sEventList
		if err := json.Unmarshal(out, &list); err != nil {
			return errMsg{fmt.Errorf("parse events: %w", err)}
		}

		rows := make([]eventRow, 0, len(list.Items))
		for _, item := range list.Items {
			rows = append(rows, eventRow{
				typ:       item.Type,
				namespace: item.InvolvedObject.Namespace,
				object:    item.InvolvedObject.Kind + "/" + item.InvolvedObject.Name,
				reason:    item.Reason,
				message:   item.Message,
				count:     item.Count,
				age:       humanAge(item.LastTimestamp),
				ts:        item.LastTimestamp,
			})
		}

		// sort newest first
		sort.Slice(rows, func(i, j int) bool {
			return rows[i].ts.After(rows[j].ts)
		})

		return eventsLoadedMsg{rows: rows}
	}
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
	namespace := parseArgs(os.Args[1:])

	p := tea.NewProgram(newModel(namespace), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseArgs(args []string) string {
	for i, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Print(`Usage: kubectl event [OPTIONS]

Interactive Kubernetes event viewer. Auto-refreshes every 10 seconds.

Options:
  -n, --namespace <namespace>   Filter by namespace (default: all)
  -h, --help                    Show this help

Keys:
  ↑/↓ navigate · / filter · w warning-only · Esc clear · r refresh · q quit
`)
			os.Exit(0)
		case (arg == "-n" || arg == "--namespace") && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(arg, "--namespace="):
			return strings.TrimPrefix(arg, "--namespace=")
		case strings.HasPrefix(arg, "-n="):
			return strings.TrimPrefix(arg, "-n=")
		}
	}
	return ""
}
