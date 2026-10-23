// Package service assembles the host-side CLI and runtime dependencies.
package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// managedSecretKeys tracks keys that must not fall back to host environment
// values after the gateway removes them.
type managedSecretKeys map[string]struct{}

// App holds constructed dependencies for CLI commands.

// App holds constructed dependencies for CLI commands.
type App struct {
	// OpenShell global session (ApplyGlobal).
	OutputFormat        string
	GlobalWorkspace     string
	GatewayURLOverride  string
	GatewayNameOverride string
	GatewayInsecure     bool

	// cmdCtx is the per-invocation context (SIGINT/SIGTERM from main).
	cmdCtx       context.Context
	secretMu     sync.Mutex
	tokenMu      sync.Mutex
	edgeTunnelMu sync.Mutex
	edgeTunnels  map[string]string
	// gatewaySecretKeys remembers keys that were gateway-managed so a later
	// empty snapshot cannot revive an older value from the process environment.
	gatewaySecretKeys map[string]managedSecretKeys
}

// New builds the Gateway-facing CLI application.
func New() *App {
	return &App{}
}

func envTruthy(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func findModuleDir(modulePath string) (string, error) {
	candidates := []string{}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, wd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	want := "module " + modulePath
	short := strings.TrimPrefix(modulePath, "github.com/KLORQ/")
	for _, start := range candidates {
		dir := start
		for range 8 {
			gm := filepath.Join(dir, "go.mod")
			b, err := os.ReadFile(gm)
			if err == nil && strings.Contains(string(b), want) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		try := filepath.Join(start, short)
		if b, err := os.ReadFile(filepath.Join(try, "go.mod")); err == nil &&
			strings.Contains(string(b), want) {
			return try, nil
		}
	}
	return "", fmt.Errorf("cannot find module %s source", modulePath)
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
