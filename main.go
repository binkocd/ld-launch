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
// 1. STYLES & MODELS
// -----------------------------------------------------------------------

var (
	docStyle     = lipgloss.NewStyle().Margin(1, 2)
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	labelStyle   = lipgloss.NewStyle().Width(14).Bold(true)
	confirmStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true).MarginTop(1)
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
	LastConnected string `yaml:"last_connected"`
	IsSystem      bool   `yaml:"-"`
}

type Config struct {
	Hosts []Host `yaml:"hosts"`
}

type item struct {
	title, desc, endpoint, lastConnected, status string
	isSystem                                     bool
}

func (i item) Title() string { return i.title }
func (i item) Description() string {
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

type listKeyMap struct {
	add, delete key.Binding
}

func newListKeyMap() listKeyMap {
	return listKeyMap{
		add:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
		delete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	}
}

type model struct {
	state      sessionState
	list       list.Model
	keys       listKeyMap
	inputs     []textinput.Model
	focusIndex int
	choice     string
}

// -----------------------------------------------------------------------
// 2. TUI INITIALIZATION
// -----------------------------------------------------------------------

func initialModel(hosts []Host) model {
	var items []list.Item
	for _, h := range hosts {
		prefix := ""
		if h.IsSystem {
			prefix = "(sys) "
		}
		items = append(items, item{
			title: prefix + h.Name, desc: h.Desc, endpoint: h.Endpoint,
			lastConnected: h.LastConnected, status: "Checking...", isSystem: h.IsSystem,
		})
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Docker Host Manager"

	keys := newListKeyMap()
	l.AdditionalFullHelpKeys = func() []key.Binding { return []key.Binding{keys.add, keys.delete} }

	ins := make([]textinput.Model, 3)
	ins[0] = textinput.New()
	ins[0].Placeholder = "Name"
	ins[1] = textinput.New()
	ins[1].Placeholder = "Description"
	ins[2] = textinput.New()
	ins[2].Placeholder = "ssh://user@ip"

	return model{state: stateList, list: l, keys: keys, inputs: ins}
}

func (m model) Init() tea.Cmd {
	var cmds []tea.Cmd
	for i, itm := range m.list.Items() {
		cmds = append(cmds, checkStatus(i, itm.(item).endpoint))
	}
	return tea.Batch(cmds...)
}

// -----------------------------------------------------------------------
// 3. UPDATE LOGIC
// -----------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.WindowSizeMsg); ok {
		h, v := docStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)
	}

	switch m.state {
	case stateConfirmDelete:
		return m.updateConfirmDelete(msg)
	case stateForm:
		return m.updateForm(msg)
	default:
		return m.updateList(msg)
	}
}

func (m model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			return m, m.inputs[0].Focus()
		case "d":
			if i, ok := m.list.SelectedItem().(item); ok {
				if !i.isSystem {
					m.state = stateConfirmDelete
				}
			}
			return m, nil
		case "q", "ctrl+c":
			m.choice = ""
			return m, tea.Quit
		case "enter":
			if i, ok := m.list.SelectedItem().(item); ok {
				m.choice = i.endpoint
				idx := m.list.Index()
				items := m.list.Items()
				selected := items[idx].(item)
				selected.lastConnected = time.Now().Format("Jan 02 15:04")
				items[idx] = selected
				m.list.SetItems(items)
				m.saveCurrentListToYAML()
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m model) updateConfirmDelete(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch strings.ToLower(msg.String()) {
		case "y":
			m.list.RemoveItem(m.list.Index())
			m.saveCurrentListToYAML()
			m.state = stateList
		case "n", "esc":
			m.state = stateList
		}
	}
	return m, nil
}

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.state = stateList
			return m, nil
		case "tab", "enter":
			if msg.String() == "enter" && m.focusIndex == 2 {
				newIdx := len(m.list.Items())
				endpoint := m.inputs[2].Value()
				m.list.InsertItem(newIdx, item{
					title: m.inputs[0].Value(), desc: m.inputs[1].Value(), endpoint: endpoint, status: "Checking...",
				})
				m.saveCurrentListToYAML()
				for i := range m.inputs {
					m.inputs[i].SetValue("")
					m.inputs[i].Blur()
				}
				m.focusIndex = 0
				m.state = stateList
				return m, checkStatus(newIdx, endpoint)
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
// 4. VIEW & HELPERS
// -----------------------------------------------------------------------

func (m model) View() string {
	switch m.state {
	case stateConfirmDelete:
		selected := m.list.SelectedItem().(item)
		return docStyle.Render(fmt.Sprintf("Delete %s?\n\n%s", titleStyle.Render(selected.title), confirmStyle.Render("[y] Confirm / [n] Cancel")))
	case stateForm:
		var s strings.Builder
		s.WriteString(titleStyle.Render("ADD NEW HOST") + "\n\n")
		labels := []string{"Host Name:", "Description:", "Endpoint:"}
		for i := range m.inputs {
			s.WriteString(fmt.Sprintf("%s %s\n", labelStyle.Render(labels[i]), m.inputs[i].View()))
		}
		return docStyle.Render(s.String() + "\n(tab/enter to cycle • esc to cancel)")
	default:
		return docStyle.Render(m.list.View())
	}
}

func (m model) saveCurrentListToYAML() {
	var custom []Host
	for _, itm := range m.list.Items() {
		h := itm.(item)
		if !h.isSystem {
			custom = append(custom, Host{Name: h.title, Desc: h.desc, Endpoint: h.endpoint, LastConnected: h.lastConnected})
		}
	}
	data, _ := yaml.Marshal(Config{Hosts: custom})
	configDir, _ := os.UserConfigDir()
	_ = os.WriteFile(filepath.Join(configDir, "ld-launch", "config.yml"), data, 0644)
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
			Name: fmt.Sprintf("%v", raw["Name"]), Desc: fmt.Sprintf("%v", raw["Description"]),
			Endpoint: fmt.Sprintf("%v", raw["DockerEndpoint"]), IsSystem: true,
		})
	}
	return hosts
}

func main() {
	configDir, _ := os.UserConfigDir()
	_ = os.MkdirAll(filepath.Join(configDir, "ld-launch"), os.ModePerm)

	for {
		path := filepath.Join(configDir, "ld-launch", "config.yml")
		var cfg Config
		if data, err := os.ReadFile(path); err == nil {
			yaml.Unmarshal(data, &cfg)
		}

		all := append(getSystemContexts(), cfg.Hosts...)
		p := tea.NewProgram(initialModel(all), tea.WithAltScreen())

		finalModel, err := p.Run()
		if err != nil {
			fmt.Printf("Error: %v", err)
			os.Exit(1)
		}

		m := finalModel.(model)
		// If choice is empty, it means the user pressed Q or Ctrl+C
		if m.choice == "" {
			break
		}

		c := exec.Command("lazydocker")
		c.Env = append(os.Environ(), "DOCKER_HOST="+m.choice)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		_ = c.Run()
		
		// After lazydocker exits, the loop restarts back to the menu
	}
}