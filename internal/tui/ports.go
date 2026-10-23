package tui

import (
	"context"
	"io"
)

// Sandbox is the renderer-facing subset of sandbox state.
type Sandbox struct {
	Name   string
	Status string
	Image  string
}

// LogOptions describes a renderer request without exposing service internals.
type LogOptions struct {
	Names  []string
	All    bool
	Follow bool
	Source string
}

// Control is the boundary between terminal presentation and CLI composition.
type Control interface {
	ListSandboxes(context.Context) ([]Sandbox, error)
	Logs(context.Context, LogOptions, io.Writer) error
	LogSources() []string
}
