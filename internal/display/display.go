// Package display contains local operator-facing display helpers.
package display

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	"github.com/KLORQ/quarshell-core"
	"github.com/KLORQ/quarshell-core/defaults"
)

// Mode selects how the agent GUI is exposed to the operator.
type Mode string

const (
	ModeNone  Mode = "none"
	ModeNoVNC Mode = "novnc"
)

// Config is host-side display options.
type Config struct {
	Mode     Mode
	Port     int    // host loopback port; 0 uses DefaultPort
	Password string // empty → RandomPassword
	Publish  string // default 127.0.0.1
}

// Stack starts and stops a display backend for one sandbox.
type Stack interface {
	Start(ctx context.Context, cfg Config) (url string, err error)
	Stop(ctx context.Context) error
}

// None is a no-op display stack.
type None struct{}

// Start returns an empty URL.
func (None) Start(_ context.Context, _ Config) (string, error) { return "", nil }

// Stop is a no-op.
func (None) Stop(_ context.Context) error { return nil }

// ErrUnsupported is returned for modes not compiled in.
var ErrUnsupported = fmt.Errorf("display: %w", core.ErrNotImplemented)

// DefaultPort is the container/host noVNC HTTP port.
const DefaultPort = defaults.NoVNCPort

// RandomPassword returns a short URL-safe password.
func RandomPassword() (string, error) {
	var b [9]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	s := base64.RawURLEncoding.EncodeToString(b[:])
	if len(s) > 12 {
		s = s[:12]
	}
	return s, nil
}

// NoVNCURL builds the operator URL (password as query for convenience; also enter in UI).
func NoVNCURL(host string, port int, password string) string {
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = DefaultPort
	}
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   "/vnc.html",
	}
	q := url.Values{}
	q.Set("autoconnect", "true")
	q.Set("resize", "remote")
	if password != "" {
		q.Set("password", password)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// ParseMode maps CLI/policy strings.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "novnc", "vnc", "gui":
		return ModeNoVNC
	default:
		return ModeNone
	}
}

// OpenHostBrowser opens the URL with the platform default browser (best-effort).
func OpenHostBrowser(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("display: browser URL must be absolute http(s)")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "--", rawURL)
	case "linux":
		cmd = exec.Command("xdg-open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return fmt.Errorf("display: open browser unsupported on %s", runtime.GOOS)
	}
	return cmd.Start()
}
