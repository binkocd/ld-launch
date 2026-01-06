package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// -----------------------------------------------------------------------
// 1. STYLES & CONFIG
// -----------------------------------------------------------------------

var (
	docStyle     = lipgloss.NewStyle().Margin(1, 2)
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	labelStyle   = lipgloss.NewStyle().Width(14).Bold(true)
	confirmStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true).MarginTop(1)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // For timestamps
)

type sessionState int

const (
	stateList sessionState = iota
	stateForm
	stateConfirmDelete
)

type Host struct {
	Name          string `yaml:"name"`
	Desc          string `yaml:"desc"`
	Endpoint      string `yaml:"endpoint"`
	LastConnected string `yaml:"last_connected"` // New field for YAML
	IsSystem      bool   `yaml:"-"`
}

type Config struct {
	Hosts []Host `yaml:"hosts"`
}

type item struct {
	title, desc, endpoint string
	status                string
	lastConnected         string // New field for the TUI display
	isSystem              bool
}

func (i item) Title() string { return i.title }
func (i item) Description() string {
	// Showing Status + Last Connected in the description
	last := i.lastConnected
	if last == "" {
		last = "Never"
	}
	return fmt.Sprintf("[%s] %s • Used: %s", i.status, i.desc, last)
}
func (i item) FilterValue() string {
	return strings.ToLower(i.title + " " + i.desc + " " + i.endpoint)
}

type statusMsg struct {
	index  int
	status string
}

// -----------------------------------------------------------------------
// 2. KEYBINDINGS
// -----------------------------------------------------------------------

type listKeyMap struct {
	add    key.Binding
	delete key.Binding
}

func newListKeyMap() listKeyMap {
	return listKeyMap{
		add:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
		delete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	}
}

// -----------------------------------------------------------------------
// 3. THE MODEL
// -----------------------------------------------------------------------

type model struct {
	state      sessionState
	list       list.Model
	keys       listKeyMap
	inputs     []textinput.Model
	focusIndex int
	choice     string
}

func initialModel(hosts []Host) model {
	var items []list.Item
	for _, h := range hosts {
		prefix := ""
		if h.IsSystem {
			prefix = "(sys) "
		}
		items = append(items, item{
			title:         prefix + h.Name,
			desc:          h.Desc,
			endpoint:      h.Endpoint,
			lastConnected: h.LastConnected,
			status:        "Checking...",
			isSystem:      h.IsSystem,
		})
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Docker Host Manager"
	customKeys := newListKeyMap()
	l.AdditionalFullHelpKeys = func() []key.Binding { return []key.Binding{customKeys.add, customKeys.delete} }

	inputs := make([]textinput.Model, 3)
	inputs[0] = textinput.New()
	inputs[0].Placeholder = "Home Server"
	inputs[1] = textinput.New()
	inputs[1].Placeholder = "Raspberry Pi Cluster"
	inputs[2] = textinput.New()
	inputs[2].Placeholder = "ssh://user@192.168.1.50"

	return model{state: stateList, list: l, keys: customKeys, inputs: inputs}
}

// -----------------------------------------------------------------------
// 4. UPDATE (LOGIC)
// -----------------------------------------------------------------------

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for i, itm := range m.list.Items() {
		cmds = append(cmds, checkStatus(i, itm.(item).endpoint))
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		h, v := docStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
	}

	switch m.state {
	case stateConfirmDelete:
		return updateConfirmDelete(msg, m)
	case stateForm:
		return updateForm(msg, m)
	default:
		return updateList(msg, m)
	}
}

func updateList(msg tea.Msg, m model) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case statusMsg:
		items := m.list.Items()
		if msg.index < len(items) {
			itm := items[msg.index].(item)
			itm.status = msg.status
			items[msg.index] = itm
			return m, m.list.SetItems(items)
		}
	case tea.KeyMsg:
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "a":
			m.state = stateForm
			m.focusIndex = 0
			return m, m.inputs[0].Focus()
		case "d":
			m.state = stateConfirmDelete
			return m, nil
		case "enter":
			i, ok := m.list.SelectedItem().(item)
			if ok {
				m.choice = i.endpoint

				// UPDATE TIMESTAMP LOGIC
				idx := m.list.Index()
				items := m.list.Items()
				selected := items[idx].(item)
				// Format: Jan 06 14:38
				selected.lastConnected = time.Now().Format("Jan 02 15:04")
				items[idx] = selected
				m.list.SetItems(items)

				// Save the timestamp to YAML before closing
				m.saveCurrentListToYAML()
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func updateConfirmDelete(msg tea.Msg, m model) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch msg.String() {
		case "y", "Y":
			m.list.RemoveItem(m.list.Index())
			m.saveCurrentListToYAML()
			m.state = stateList
		case "n", "N", "esc":
			m.state = stateList
		}
	}
	return m, nil
}

func updateForm(msg tea.Msg, m model) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateList
			return m, nil
		case "tab", "enter":
			if msg.String() == "enter" && m.focusIndex == 2 {
				newIndex := len(m.list.Items())
				m.list.InsertItem(newIndex, item{
					title:    m.inputs[0].Value(),
					desc:     m.inputs[1].Value(),
					endpoint: m.inputs[2].Value(),
					status:   "Checking...",
				})
				m.saveCurrentListToYAML()
				for i := range m.inputs {
					m.inputs[i].SetValue("")
				}
				m.state = stateList
				// Trigger the status check for the new item immediately
				return m, checkStatus(newIndex, m.inputs[2].Value())
			}
			m.inputs[m.focusIndex].Blur()
			m.focusIndex = (m.focusIndex + 1) % 3
			return m, m.inputs[m.focusIndex].Focus()
		}
	}
	var cmds []tea.Cmd
	for i := range m.inputs {
		var cmd tea.Cmd
		m.inputs[i], cmd = m.inputs[i].Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// -----------------------------------------------------------------------
// 5. VIEW (RENDERING)
// -----------------------------------------------------------------------

func (m model) View() string {
	switch m.state {
	case stateConfirmDelete:
		selected := m.list.SelectedItem().(item)
		return docStyle.Render(fmt.Sprintf(
			"Delete host %s?\n\n%s",
			titleStyle.Render(selected.title),
			confirmStyle.Render("[y] Confirm / [n] Cancel"),
		))
	case stateForm:
		var s strings.Builder
		s.WriteString(titleStyle.Render("ADD NEW DOCKER HOST") + "\n\n")
		labels := []string{"Host Name:", "Description:", "Endpoint:"}
		for i := range m.inputs {
			s.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render(labels[i]), m.inputs[i].View()))
		}
		s.WriteString("\n(tab to cycle • enter to save • esc to cancel)")
		return docStyle.Render(s.String())
	default:
		return docStyle.Render(m.list.View())
	}
}

// -----------------------------------------------------------------------
// 6. BACKEND (SAVE/LOAD/EXEC)
// -----------------------------------------------------------------------

func loadConfig() (Config, error) {
	configDir, _ := os.UserConfigDir()
	path := filepath.Join(configDir, "ld-launch", "config.yml")
	data, err := os.ReadFile(path)
	var cfg Config
	if err != nil {
		return cfg, err
	}
	yaml.Unmarshal(data, &cfg)
	return cfg, nil
}

func (m model) saveCurrentListToYAML() {
	var custom []Host
	for _, itm := range m.list.Items() {
		h := itm.(item)
		if !h.isSystem {
			custom = append(custom, Host{
				Name:          h.title,
				Desc:          h.desc,
				Endpoint:      h.endpoint,
				LastConnected: h.lastConnected, // Save the timestamp!
			})
		}
	}
	data, _ := yaml.Marshal(Config{Hosts: custom})
	configDir, _ := os.UserConfigDir()
	path := filepath.Join(configDir, "ld-launch", "config.yml")
	_ = os.WriteFile(path, data, 0644)
}

func getSystemContexts() []Host {
	cmd := exec.Command("docker", "context", "ls", "--format", "{{json .}}")
	out, _ := cmd.Output()
	var hosts []Host
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		json.Unmarshal([]byte(line), &raw)
		hosts = append(hosts, Host{
			Name:     fmt.Sprintf("%v", raw["Name"]),
			Desc:     fmt.Sprintf("%v", raw["Description"]),
			Endpoint: fmt.Sprintf("%v", raw["DockerEndpoint"]),
			IsSystem: true,
		})
	}
	return hosts
}

func checkStatus(index int, endpoint string) tea.Cmd {
	return func() tea.Msg {
		alive := false
		if strings.HasPrefix(endpoint, "unix://") {
			_, err := os.Stat(strings.TrimPrefix(endpoint, "unix://"))
			alive = (err == nil)
		} else {
			u, _ := url.Parse(endpoint)
			addr := u.Host
			if !strings.Contains(addr, ":") {
				addr += ":22"
			}
			conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
			if err == nil {
				conn.Close()
				alive = true
			}
		}
		status := "○ Offline"
		if alive {
			status = "● Online"
		}
		return statusMsg{index, status}
	}
}

func runLazyDocker(endpoint string) {
	c := exec.Command("lazydocker")
	c.Env = append(os.Environ(), "DOCKER_HOST="+endpoint)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	_ = c.Run()
}

func main() {
	configDir, _ := os.UserConfigDir()
	_ = os.MkdirAll(filepath.Join(configDir, "ld-launch"), os.ModePerm)
	cfg, _ := loadConfig()
	all := append(getSystemContexts(), cfg.Hosts...)
	p := tea.NewProgram(initialModel(all), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		os.Exit(1)
	}
}