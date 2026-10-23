// Package ui provides host-side CLI chrome (styles + terminal capability tiers).
package ui

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// Mode is the host terminal capability tier.
type Mode int

const (
	// Plain — no color / dumb terminal / non-TTY.
	Plain Mode = iota
	// Reduced — TTY but limited (no truecolor / screen / dumb-ish).
	Reduced
	// Full — interactive TTY suitable for login shell + color chrome.
	Full
)

var (
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

var colorMode = "auto"

// SetColorMode configures whether CLI chrome uses ANSI styling.
func SetColorMode(mode string) {
	colorMode = strings.ToLower(strings.TrimSpace(mode))
	profile := termenv.Ascii
	switch colorMode {
	case "always":
		profile = termenv.ANSI
	case "auto":
		if os.Getenv("NO_COLOR") == "" && os.Getenv("FORCE_COLOR") != "" {
			profile = termenv.ANSI
		} else if os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stderr.Fd())) {
			termType := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
			if runtime.GOOS == "windows" || (termType != "" && termType != "dumb") {
				profile = termenv.ANSI
			}
		}
	}
	lipgloss.SetColorProfile(profile)
}

// Detect picks Full / Reduced / Plain from the environment.
func Detect() Mode {
	switch colorMode {
	case "never":
		return Plain
	case "always":
		return Full
	}
	if os.Getenv("NO_COLOR") != "" {
		return Plain
	}
	if os.Getenv("FORCE_COLOR") != "" {
		return Full
	}
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return Plain
	}
	t := strings.ToLower(strings.TrimSpace(os.Getenv("TERM")))
	if runtime.GOOS != "windows" {
		if t == "" || t == "dumb" {
			return Plain
		}
	}
	if strings.Contains(t, "color") || strings.HasPrefix(t, "xterm") ||
		strings.HasPrefix(t, "screen") || strings.HasPrefix(t, "tmux") ||
		strings.HasPrefix(t, "vt") || os.Getenv("COLORTERM") != "" {
		return Full
	}
	return Reduced
}

// Ok prints a success line (styled when capable).
func Ok(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch Detect() {
	case Full, Reduced:
		fmt.Fprintln(os.Stderr, okStyle.Render("✓")+" "+msg)
	default:
		fmt.Fprintln(os.Stderr, "ok: "+msg)
	}
}

// Warn prints a warning line.
func Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch Detect() {
	case Full, Reduced:
		fmt.Fprintln(os.Stderr, warnStyle.Render("!")+" "+msg)
	default:
		fmt.Fprintln(os.Stderr, "warn: "+msg)
	}
}

// Err prints an error line (does not exit).
func Err(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch Detect() {
	case Full, Reduced:
		fmt.Fprintln(os.Stderr, errStyle.Render("✗")+" "+msg)
	default:
		fmt.Fprintln(os.Stderr, "error: "+msg)
	}
}

// Dim prints secondary info.
func Dim(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch Detect() {
	case Full, Reduced:
		fmt.Fprintln(os.Stderr, dimStyle.Render(msg))
	default:
		fmt.Fprintln(os.Stderr, msg)
	}
}

// DefaultShellArgv returns guest argv for connect (login shell when Full).
func DefaultShellArgv() []string {
	switch Detect() {
	case Full:
		return []string{"bash", "-il"}
	default:
		return []string{"bash"}
	}
}
