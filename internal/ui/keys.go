package ui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
)

type keyMap struct {
	Up           key.Binding
	Down         key.Binding
	Previous     key.Binding
	Next         key.Binding
	All          key.Binding
	CPU          key.Binding
	Memory       key.Binding
	Engines      key.Binding
	Processes    key.Binding
	Control      key.Binding
	Info         key.Binding
	Help         key.Binding
	Back         key.Binding
	Details      key.Binding
	SortCPU      key.Binding
	SortMemory   key.Binding
	Paths        key.Binding
	Performance  key.Binding
	Powersave    key.Binding
	SwapTune     key.Binding
	SwapSizeTune key.Binding
	Refresh      key.Binding
	Quit         key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		Previous: key.NewBinding(
			key.WithKeys("left"),
			key.WithHelp("←", "previous page"),
		),
		Next: key.NewBinding(
			key.WithKeys("right", "tab"),
			key.WithHelp("→/tab", "next page"),
		),
		All: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "all"),
		),
		CPU: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "cpu"),
		),
		Memory: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "memory"),
		),
		Engines: key.NewBinding(
			key.WithKeys("4"),
			key.WithHelp("4", "engines"),
		),
		Processes: key.NewBinding(
			key.WithKeys("5"),
			key.WithHelp("5", "processes"),
		),
		Control: key.NewBinding(
			key.WithKeys("6"),
			key.WithHelp("6", "CTRL"),
		),
		Info: key.NewBinding(
			key.WithKeys("7", "i"),
			key.WithHelp("7/i", "info"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "close"),
		),
		Details: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "details"),
		),
		SortCPU: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "sort CPU"),
		),
		SortMemory: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "sort memory"),
		),
		Paths: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "show/hide paths"),
		),
		Performance: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "performance"),
		),
		Powersave: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "powersave"),
		),
		SwapTune: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "swappiness"),
		),
		SwapSizeTune: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "Swap capacity"),
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
	return []key.Binding{k.Help, k.Refresh, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.All, k.CPU, k.Memory, k.Engines, k.Processes, k.Control, k.Info},
		{k.Previous, k.Next, k.Up, k.Down, k.SortCPU, k.SortMemory, k.Paths, k.Details},
		{k.Refresh, k.Help, k.Back, k.Quit},
	}
}

var _ help.KeyMap = keyMap{}
