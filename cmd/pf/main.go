package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ─── styles ───────────────────────────────────────────────────────────────────

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205")).PaddingLeft(1)
	helpStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).PaddingLeft(1)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	runningStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Bold(true)
	stoppedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)

	overlayStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("205")).
			Padding(1, 3)
)

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

// ─── k8s types ────────────────────────────────────────────────────────────────

type k8sServiceList struct {
	Items []struct {
		Metadata struct {
			Name              string    `json:"name"`
			Namespace         string    `json:"namespace"`
			CreationTimestamp time.Time `json:"creationTimestamp"`
		} `json:"metadata"`
		Spec struct {
			ClusterIP string `json:"clusterIP"`
			Ports     []struct {
				Port     int    `json:"port"`
				Protocol string `json:"protocol"`
				Name     string `json:"name"`
			} `json:"ports"`
		} `json:"spec"`
	} `json:"items"`
}

type k8sService struct {
	name      string
	namespace string
	clusterIP string
	ports     []svcPort
	age       string
}

type svcPort struct {
	port     int
	protocol string
}

func (s k8sService) portsString() string {
	parts := make([]string, len(s.ports))
	for i, p := range s.ports {
		parts[i] = fmt.Sprintf("%d/%s", p.port, p.protocol)
	}
	return strings.Join(parts, ", ")
}

func (s k8sService) firstPort() string {
	if len(s.ports) == 0 {
		return ""
	}
	return fmt.Sprintf("%d", s.ports[0].port)
}

// ─── port-forward ─────────────────────────────────────────────────────────────

type portForward struct {
	service   string
	namespace string
	localPort string
	remPort   string
	cmd       *exec.Cmd
	running   bool
}

// ─── app state ────────────────────────────────────────────────────────────────

type appState int

const (
	stateServices      appState = iota
	stateServiceSearch          // search input active
	statePortInput              // port input overlay
	stateForwards               // active forwards table
)

// ─── messages ─────────────────────────────────────────────────────────────────

type servicesMsg struct{ services []k8sService }
type forwardStartedMsg struct{ fwd *portForward }
type fwdRefreshMsg struct{}
type errMsg struct{ err error }

// ─── model ────────────────────────────────────────────────────────────────────

type model struct {
	state       appState
	allServices []k8sService

	// services table + search
	svcTable  table.Model
	svcSearch textinput.Model

	// port input overlay
	portInput   textinput.Model
	selectedSvc k8sService

	// forwards table
	forwards   []*portForward
	fwdTable   table.Model

	width  int
	height int
	err    string
}

// ─── column helpers ───────────────────────────────────────────────────────────

func svcNameWidth(w int) int {
	// fixed: NAMESPACE(14)+CLUSTER-IP(16)+PORTS(24)+AGE(6) = 60
	nw := w - 60
	if nw < 20 {
		nw = 20
	}
	return nw
}

func svcCols(nameWidth int) []table.Column {
	return []table.Column{
		{Title: "NAME", Width: nameWidth},
		{Title: "NAMESPACE", Width: 14},
		{Title: "CLUSTER-IP", Width: 16},
		{Title: "PORTS", Width: 24},
		{Title: "AGE", Width: 6},
	}
}

func fwdCols() []table.Column {
	return []table.Column{
		{Title: "SERVICE", Width: 28},
		{Title: "NAMESPACE", Width: 14},
		{Title: "PORTS", Width: 16},
		{Title: "STATUS", Width: 10},
	}
}

// ─── constructor ──────────────────────────────────────────────────────────────

func newModel() model {
	ts := textinput.New()
	ts.Prompt = dimStyle.Render("/") + " "
	ts.Placeholder = "search services..."
	ts.CharLimit = 64

	pi := textinput.New()
	pi.Prompt = "local:remote> "
	pi.CharLimit = 32

	st := table.New(
		table.WithColumns(svcCols(24)),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
	)

	ft := table.New(
		table.WithColumns(fwdCols()),
		table.WithRows(nil),
		table.WithFocused(true),
		table.WithStyles(tableStyles()),
	)

	return model{
		state:     stateServices,
		svcTable:  st,
		svcSearch: ts,
		portInput: pi,
		fwdTable:  ft,
	}
}

// ─── init ─────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return fetchServices()
}

// ─── update ───────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.svcTable.SetColumns(svcCols(svcNameWidth(m.width)))
		m.svcTable.SetHeight(m.height - 4) // title + search + fwdbar + help
		m.fwdTable.SetHeight(m.height - 3)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case servicesMsg:
		m.allServices = msg.services
		m.svcTable.SetRows(buildSvcRows(m.allServices))

	case forwardStartedMsg:
		m.forwards = append(m.forwards, msg.fwd)
		m.fwdTable.SetRows(buildFwdRows(m.forwards))
		m.state = stateServices
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return fwdRefreshMsg{} })

	case fwdRefreshMsg:
		m.fwdTable.SetRows(buildFwdRows(m.forwards))
		// keep ticking as long as there are active forwards
		for _, f := range m.forwards {
			if f.running {
				return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return fwdRefreshMsg{} })
			}
		}

	case errMsg:
		m.err = msg.err.Error()
	}

	var cmd tea.Cmd
	switch m.state {
	case stateServices:
		m.svcTable, cmd = m.svcTable.Update(msg)
	case stateServiceSearch:
		m.svcSearch, cmd = m.svcSearch.Update(msg)
		m.applySearch()
		var tc tea.Cmd
		m.svcTable, tc = m.svcTable.Update(msg)
		return m, tea.Batch(cmd, tc)
	case statePortInput:
		m.portInput, cmd = m.portInput.Update(msg)
	case stateForwards:
		m.fwdTable, cmd = m.fwdTable.Update(msg)
	}
	return m, cmd
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {

	// ── services ──────────────────────────────────────────────────────────────
	case stateServices:
		switch msg.String() {
		case "ctrl+c", "q":
			m.killAll()
			return m, tea.Quit
		case "/":
			m.state = stateServiceSearch
			m.svcSearch.SetValue("")
			m.svcSearch.Focus()
			return m, textinput.Blink
		case "enter":
			if svc, ok := m.svcFromRow(); ok {
				m.selectedSvc = svc
				m.portInput.SetValue("")
				m.portInput.Focus()
				m.state = statePortInput
				return m, textinput.Blink
			}
		case "tab":
			m.state = stateForwards
			return m, nil
		}
		var cmd tea.Cmd
		m.svcTable, cmd = m.svcTable.Update(msg)
		return m, cmd

	// ── service search ────────────────────────────────────────────────────────
	case stateServiceSearch:
		switch msg.String() {
		case "ctrl+c":
			m.killAll()
			return m, tea.Quit
		case "esc":
			m.svcSearch.Blur()
			m.svcSearch.SetValue("")
			m.applySearch()
			m.state = stateServices
			return m, nil
		case "enter":
			if svc, ok := m.svcFromRow(); ok {
				m.svcSearch.Blur()
				m.svcSearch.SetValue("")
				m.applySearch()
				m.selectedSvc = svc
				m.portInput.SetValue("")
				m.portInput.Focus()
				m.state = statePortInput
				return m, textinput.Blink
			}
			m.svcSearch.Blur()
			m.state = stateServices
			return m, nil
		case "tab":
			m.svcSearch.Blur()
			m.svcSearch.SetValue("")
			m.applySearch()
			m.state = stateForwards
			return m, nil
		}
		var cmd tea.Cmd
		m.svcSearch, cmd = m.svcSearch.Update(msg)
		m.applySearch()
		var tc tea.Cmd
		m.svcTable, tc = m.svcTable.Update(msg)
		return m, tea.Batch(cmd, tc)

	// ── port input overlay ────────────────────────────────────────────────────
	case statePortInput:
		switch msg.String() {
		case "ctrl+c":
			m.killAll()
			return m, tea.Quit
		case "esc":
			m.portInput.Blur()
			m.state = stateServices
			return m, nil
		case "enter":
			localPort, remotePort, ok := parsePortInput(m.portInput.Value(), m.selectedSvc.firstPort())
			if !ok {
				return m, nil
			}
			m.portInput.Blur()
			return m, startForward(m.selectedSvc, localPort, remotePort)
		}
		var cmd tea.Cmd
		m.portInput, cmd = m.portInput.Update(msg)
		return m, cmd

	// ── forwards ──────────────────────────────────────────────────────────────
	case stateForwards:
		switch msg.String() {
		case "ctrl+c", "q":
			m.killAll()
			return m, tea.Quit
		case "d":
			if idx, ok := m.fwdIndex(); ok {
				fwd := m.forwards[idx]
				if fwd.running && fwd.cmd != nil && fwd.cmd.Process != nil {
					_ = fwd.cmd.Process.Kill()
					fwd.running = false
				}
				m.fwdTable.SetRows(buildFwdRows(m.forwards))
			}
			return m, nil
		case "tab":
			m.state = stateServices
			return m, nil
		}
		var cmd tea.Cmd
		m.fwdTable, cmd = m.fwdTable.Update(msg)
		return m, cmd
	}

	return m, nil
}

// ─── view ─────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if m.err != "" {
		return titleStyle.Render("Error") + "\n\n  " + m.err + "\n\n" + helpStyle.Render("q quit")
	}
	switch m.state {
	case stateServices, stateServiceSearch:
		return m.servicesView()
	case statePortInput:
		return m.portInputView()
	case stateForwards:
		return m.forwardsView()
	}
	return ""
}

func (m model) servicesView() string {
	active := m.runningCount()
	titleText := fmt.Sprintf("Services  [%d]", len(m.allServices))
	if active > 0 {
		titleText += "  " + runningStyle.Render(fmt.Sprintf("● %d forwarding", active))
	}
	title := titleStyle.Render(titleText)

	var searchBar string
	if m.state == stateServiceSearch {
		searchBar = " " + m.svcSearch.View()
	} else if q := m.svcSearch.Value(); q != "" {
		searchBar = dimStyle.Render(" filter: ") + q
	} else {
		searchBar = dimStyle.Render(" / to search")
	}

	help := helpStyle.Render("/ search · enter start forward · tab forwards · q quit")
	return title + "\n" + searchBar + "\n" + m.forwardsBar() + "\n" + m.svcTable.View() + "\n" + help
}

func (m model) forwardsBar() string {
	var parts []string
	for _, f := range m.forwards {
		if f.running {
			parts = append(parts, runningStyle.Render("●")+
				lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(
					fmt.Sprintf(" %s  localhost:%s → %s", f.service, f.localPort, f.remPort),
				))
		}
	}
	if len(parts) == 0 {
		return dimStyle.Render(" no active forwards")
	}
	return " " + strings.Join(parts, dimStyle.Render("  │  "))
}

func (m model) runningCount() int {
	n := 0
	for _, f := range m.forwards {
		if f.running {
			n++
		}
	}
	return n
}

func (m model) portInputView() string {
	svc := m.selectedSvc
	box := overlayStyle.Render(
		titleStyle.Render("Start Port-Forward") + "\n\n" +
			"  Service:    " + lipgloss.NewStyle().Bold(true).Render(svc.name) + "\n" +
			"  Namespace:  " + svc.namespace + "\n" +
			"  Ports:      " + dimStyle.Render(svc.portsString()) + "\n\n" +
			"  " + m.portInput.View() + "\n\n" +
			dimStyle.Render("  e.g. 8080  or  8080:80") + "\n" +
			helpStyle.Render("  enter confirm · esc cancel"),
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

	// render service table behind the overlay
	bg := titleStyle.Render(fmt.Sprintf("Services  [%d]", len(m.allServices))) +
		"\n" + dimStyle.Render(" / to search") +
		"\n" + m.svcTable.View() +
		"\n" + helpStyle.Render("/ search · enter start forward · tab forwards · q quit")

	bgLines := strings.Split(bg, "\n")
	// pad bg to at least height lines
	for len(bgLines) < m.height {
		bgLines = append(bgLines, "")
	}

	// overlay the box onto bg lines
	indent := strings.Repeat(" ", left)
	for i, bl := range boxLines {
		lineIdx := top + i
		if lineIdx < 0 || lineIdx >= len(bgLines) {
			continue
		}
		bgLines[lineIdx] = indent + bl
	}

	return strings.Join(bgLines, "\n")
}

func (m model) forwardsView() string {
	title := titleStyle.Render(fmt.Sprintf("Forwards  [%d]", len(m.forwards)))
	help := helpStyle.Render("d kill · tab services · q quit")
	return title + "\n\n" + m.fwdTable.View() + "\n" + help
}

// ─── row builders ─────────────────────────────────────────────────────────────

func buildSvcRows(services []k8sService) []table.Row {
	rows := make([]table.Row, len(services))
	for i, s := range services {
		rows[i] = table.Row{s.name, s.namespace, s.clusterIP, s.portsString(), s.age}
	}
	return rows
}

func buildFwdRows(forwards []*portForward) []table.Row {
	rows := make([]table.Row, len(forwards))
	for i, f := range forwards {
		ports := f.localPort + ":" + f.remPort
		var status string
		if f.running {
			status = runningStyle.Render("running")
		} else {
			status = stoppedStyle.Render("stopped")
		}
		rows[i] = table.Row{f.service, f.namespace, ports, status}
	}
	return rows
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func (m model) svcFromRow() (k8sService, bool) {
	row := m.svcTable.SelectedRow()
	if row == nil {
		return k8sService{}, false
	}
	for _, s := range m.allServices {
		if s.name == row[0] && s.namespace == row[1] {
			return s, true
		}
	}
	return k8sService{}, false
}

func (m model) fwdIndex() (int, bool) {
	row := m.fwdTable.SelectedRow()
	if row == nil {
		return 0, false
	}
	for i, f := range m.forwards {
		ports := f.localPort + ":" + f.remPort
		if f.service == row[0] && f.namespace == row[1] && ports == row[2] {
			return i, true
		}
	}
	return 0, false
}

func (m *model) applySearch() {
	q := strings.ToLower(m.svcSearch.Value())
	if q == "" {
		m.svcTable.SetRows(buildSvcRows(m.allServices))
		return
	}
	var filtered []k8sService
	for _, s := range m.allServices {
		if strings.Contains(strings.ToLower(s.name), q) ||
			strings.Contains(strings.ToLower(s.namespace), q) {
			filtered = append(filtered, s)
		}
	}
	m.svcTable.SetRows(buildSvcRows(filtered))
}

func (m *model) killAll() {
	for _, f := range m.forwards {
		if f.running && f.cmd != nil && f.cmd.Process != nil {
			_ = f.cmd.Process.Kill()
			f.running = false
		}
	}
}

// parsePortInput parses "8080" or "8080:80".
// Returns (localPort, remotePort, ok).
func parsePortInput(input, firstPort string) (string, string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", false
	}
	if strings.Contains(input, ":") {
		parts := strings.SplitN(input, ":", 2)
		if parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		return parts[0], parts[1], true
	}
	// single port: map to first available remote port
	remPort := firstPort
	if remPort == "" {
		remPort = input
	}
	return input, remPort, true
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

// ─── commands ─────────────────────────────────────────────────────────────────

func fetchServices() tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("kubectl", "get", "svc", "--all-namespaces", "-o", "json").Output()
		if err != nil {
			return errMsg{err}
		}
		var list k8sServiceList
		if err := json.Unmarshal(out, &list); err != nil {
			return errMsg{err}
		}
		services := make([]k8sService, len(list.Items))
		for i, item := range list.Items {
			ports := make([]svcPort, len(item.Spec.Ports))
			for j, p := range item.Spec.Ports {
				proto := p.Protocol
				if proto == "" {
					proto = "TCP"
				}
				ports[j] = svcPort{port: p.Port, protocol: proto}
			}
			services[i] = k8sService{
				name:      item.Metadata.Name,
				namespace: item.Metadata.Namespace,
				clusterIP: item.Spec.ClusterIP,
				ports:     ports,
				age:       humanAge(item.Metadata.CreationTimestamp),
			}
		}
		return servicesMsg{services: services}
	}
}

func startForward(svc k8sService, localPort, remotePort string) tea.Cmd {
	return func() tea.Msg {
		fwd := &portForward{
			service:   svc.name,
			namespace: svc.namespace,
			localPort: localPort,
			remPort:   remotePort,
			running:   true,
		}
		cmd := exec.Command("kubectl", "port-forward",
			"-n", svc.namespace,
			"svc/"+svc.name,
			localPort+":"+remotePort,
		)
		if err := cmd.Start(); err != nil {
			return errMsg{err}
		}
		fwd.cmd = cmd

		// watch for process exit in background; mark stopped when it does
		go func() {
			_ = cmd.Wait()
			fwd.running = false
		}()

		return forwardStartedMsg{fwd: fwd}
	}
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			fmt.Print(`Usage: kubectl pf

Interactive port-forward manager. List services and manage active forwards.

Options:
  -h, --help   Show this help

Keys (services):
  ↑/↓ navigate · / search · enter start forward · Tab active forwards · q quit

Keys (active forwards):
  ↑/↓ navigate · d kill forward · Tab services · q quit
`)
			os.Exit(0)
		}
	}
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
