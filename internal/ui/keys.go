package ui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
)

type keyMap struct {
	Up         key.Binding
	Down       key.Binding
	Overview   key.Binding
	Engines    key.Binding
	Processes  key.Binding
	Thermal    key.Binding
	System     key.Binding
	Help       key.Binding
	Back       key.Binding
	SortCPU    key.Binding
	SortMemory key.Binding
	Refresh    key.Binding
	Quit       key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "select up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "select down"),
		),
		Overview: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "overview"),
		),
		Engines: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "engines"),
		),
		Processes: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "processes"),
		),
		Thermal: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "thermal"),
		),
		System: key.NewBinding(
			key.WithKeys("5"),
			key.WithHelp("5", "system"),
		),
		Help: key.NewBinding(
			key.WithKeys("6", "?"),
			key.WithHelp("?", "help"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
		SortCPU: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "sort CPU"),
		),
		SortMemory: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "sort memory"),
		),
		Refresh: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "refresh"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
	}
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Overview, k.Engines, k.Processes, k.Thermal, k.System, k.Help, k.Refresh, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Overview, k.Engines, k.Processes, k.Thermal, k.System, k.Help},
		{k.Up, k.Down, k.SortCPU, k.SortMemory},
		{k.Refresh, k.Back, k.Quit},
	}
}

var _ help.KeyMap = keyMap{}
