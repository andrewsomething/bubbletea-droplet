package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/digitalocean/godo"
	"github.com/digitalocean/godo/util"
)

var (
	placeholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#99A1B3"))
	focusedStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#0080FF"))
	blurredStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#5B6987"))
	cursorStyle      = focusedStyle.Copy()
	noStyle          = lipgloss.NewStyle()
	helpStyle        = blurredStyle.Copy()

	focusedButton = focusedStyle.Copy().Render("[ Create ]")
	blurredButton = fmt.Sprintf("[ %s ]", blurredStyle.Render("Create"))
)

type model struct {
	focusIndex int
	inputs     []textinput.Model
	cursorMode textinput.CursorMode
	spinner    spinner.Model
	creating   bool
	finalMsg   string
	droplet    *godo.DropletCreateRequest
}

type dropletMsg string

func initialModel() model {
	m := model{
		inputs:  make([]textinput.Model, 4),
		spinner: spinner.NewModel(),
	}

	m.spinner.Style = focusedStyle
	m.spinner.Spinner = spinner.Points

	var t textinput.Model
	for i := range m.inputs {
		t = textinput.NewModel()
		t.CursorStyle = cursorStyle
		t.CharLimit = 32

		switch i {
		case 0:
			t.Focus()
			t.Prompt = "Name: "
			t.Placeholder = "web-001"
			t.PlaceholderStyle = placeholderStyle
			t.PromptStyle = focusedStyle
			t.TextStyle = focusedStyle
			t.CharLimit = 64
		case 1:
			t.Prompt = "Region: "
			t.Placeholder = "nyc3"
			t.PlaceholderStyle = placeholderStyle
		case 2:
			t.Prompt = "Size: "
			t.Placeholder = "s-1vcpu-1gb"
			t.PlaceholderStyle = placeholderStyle
		case 3:
			t.Prompt = "Image: "
			t.Placeholder = "ubuntu-20-04-x64"
			t.PlaceholderStyle = placeholderStyle
		}

		m.inputs[i] = t
	}

	return m
}
func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit

		// Set focus to next input
		case "tab", "shift+tab", "enter", "up", "down":
			s := msg.String()

			if s == "enter" && m.focusIndex == len(m.inputs) {
				m.droplet = setDropletCreate(m.inputs)

				m.creating = true
				cmds := make([]tea.Cmd, 2)
				cmds[0] = dropletCreate(m.droplet)
				cmds[1] = spinner.Tick

				return m, tea.Batch(cmds...)
			}

			// Cycle indexes
			if s == "up" || s == "shift+tab" {
				m.focusIndex--
			} else {
				m.focusIndex++
			}

			if m.focusIndex > len(m.inputs) {
				m.focusIndex = 0
			} else if m.focusIndex < 0 {
				m.focusIndex = len(m.inputs)
			}

			cmds := make([]tea.Cmd, len(m.inputs))
			for i := 0; i <= len(m.inputs)-1; i++ {
				if i == m.focusIndex {
					// Set focused state
					cmds[i] = m.inputs[i].Focus()
					m.inputs[i].PromptStyle = focusedStyle
					m.inputs[i].TextStyle = focusedStyle
					continue
				}
				// Remove focused state
				m.inputs[i].Blur()
				m.inputs[i].PromptStyle = noStyle
				m.inputs[i].TextStyle = noStyle
			}

			return m, tea.Batch(cmds...)
		}

	case dropletMsg:
		m.finalMsg = string(msg)
		return m, tea.Quit
	}

	cmds := make([]tea.Cmd, 2)
	cmds[0] = m.updateInputs(msg)

	var spinnerCmd tea.Cmd
	m.spinner, spinnerCmd = m.spinner.Update(msg)
	cmds[1] = spinnerCmd

	return m, tea.Batch(cmds...)
}

func (m *model) updateInputs(msg tea.Msg) tea.Cmd {
	var cmds = make([]tea.Cmd, len(m.inputs))

	// Only text inputs with Focus() set will respond, so it's safe to simply
	// update all of them here without any further logic.
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	return tea.Batch(cmds...)
}

func (m model) View() string {
	var b strings.Builder

	if m.finalMsg != "" {
		fmt.Fprint(&b, m.finalMsg)

		return b.String()
	}

	if m.creating {
		fmt.Fprintf(&b, "%s  %s\n\n", m.spinner.View(), placeholderStyle.Render("Creating Droplet..."))

		return b.String()
	}

	for i := range m.inputs {
		b.WriteString(m.inputs[i].View())
		if i < len(m.inputs)-1 {
			b.WriteRune('\n')
		}
	}

	button := &blurredButton
	if m.focusIndex == len(m.inputs) {
		button = &focusedButton
	}
	fmt.Fprintf(&b, "\n\n%s\n\n", *button)

	return b.String()
}

func dropletCreate(createReq *godo.DropletCreateRequest) tea.Cmd {
	return func() tea.Msg {
		token := os.Getenv("DO_TOKEN")
		if token == "" {
			return dropletMsg(dropletErrorMsg(errors.New("set the 'DO_TOKEN' environment variable to a DigitalOcean API token")))
		}

		client := godo.NewFromToken(token)
		ctx := context.Background()

		droplet, resp, err := client.Droplets.Create(ctx, createReq)
		if err != nil {
			return dropletMsg(dropletErrorMsg(err))
		}
		err = util.WaitForActive(ctx, client, resp.Links.Actions[0].HREF)
		if err != nil {
			return dropletMsg(dropletErrorMsg(err))
		}
		droplet, _, err = client.Droplets.Get(ctx, droplet.ID)
		if err != nil {
			return dropletMsg(dropletErrorMsg(err))
		}

		pubIP, err := droplet.PublicIPv4()
		if err != nil {
			return dropletMsg(dropletErrorMsg(err))
		}
		privIP, err := droplet.PrivateIPv4()
		if err != nil {
			return dropletMsg(dropletErrorMsg(err))
		}

		var b strings.Builder
		fmt.Fprintf(&b, "🎉 💧 %s\n\n", focusedStyle.Render("Success!"))
		fmt.Fprintf(&b, "%s %s\n", focusedStyle.Render("Name:"), placeholderStyle.Render(droplet.Name))
		fmt.Fprintf(&b, "%s %s\n", focusedStyle.Render("Price Monthly:"), placeholderStyle.Render(fmt.Sprintf("$%.2f", droplet.Size.PriceMonthly)))
		fmt.Fprintf(&b, "%s %s\n", focusedStyle.Render("Region:"), placeholderStyle.Render(droplet.Region.Name))
		fmt.Fprintf(&b, "%s %s\n", focusedStyle.Render("Size:"), placeholderStyle.Render(droplet.Size.Slug))
		fmt.Fprintf(&b, "%s %s\n", focusedStyle.Render("Public IPv4:"), placeholderStyle.Render(pubIP))
		fmt.Fprintf(&b, "%s %s\n", focusedStyle.Render("Private IPv4:"), placeholderStyle.Render(privIP))
		fmt.Fprint(&b, "\n")

		return dropletMsg(b.String())
	}
}

func dropletErrorMsg(err error) string {
	return fmt.Sprintf("%s\n\n%s\n\n", focusedStyle.Render("😞 Something went wrong:"), placeholderStyle.Render(err.Error()))
}

func setDropletCreate(inputs []textinput.Model) *godo.DropletCreateRequest {
	droplet := &godo.DropletCreateRequest{}

	droplet.Name = inputs[0].Value()
	if droplet.Name == "" {
		droplet.Name = inputs[0].Placeholder
	}
	droplet.Region = inputs[1].Value()
	if droplet.Region == "" {
		droplet.Region = inputs[1].Placeholder
	}
	droplet.Size = inputs[2].Value()
	if droplet.Size == "" {
		droplet.Size = inputs[2].Placeholder
	}
	imageStr := inputs[3].Value()
	if imageStr == "" {
		imageStr = inputs[3].Placeholder
	}
	createImage := godo.DropletCreateImage{Slug: imageStr}
	i, err := strconv.Atoi(imageStr)
	if err == nil {
		createImage = godo.DropletCreateImage{ID: i}
	}
	droplet.Image = createImage

	return droplet
}

func main() {
	if err := tea.NewProgram(initialModel()).Start(); err != nil {
		fmt.Printf("could not start program: %s\n", err)
		os.Exit(1)
	}
}
