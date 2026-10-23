package service

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/KLORQ/quarshell-cli/internal/storage/sandboxstate"
)

func TestRemoveSandboxesAttemptsEveryName(t *testing.T) {
	var removed []string
	wantErr := errors.New("container busy")
	err := removeSandboxes([]string{"one", "two", "three"}, func(name string) error {
		removed = append(removed, name)
		if name == "two" {
			return wantErr
		}
		return nil
	})
	if !reflect.DeepEqual(removed, []string{"one", "two", "three"}) {
		t.Fatalf("remove order = %v", removed)
	}
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "failed to remove 1 of 3") || !strings.Contains(err.Error(), "two") {
		t.Fatalf("removeSandboxes() error = %v", err)
	}
}

func TestResolveSandboxTargetUsesGatewayWorkspaceScope(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	app := &App{GatewayURLOverride: "https://gw.example", GlobalWorkspace: "team-a"}
	if err := sandboxstate.SetLast("https://gw.example", "team-a", "remembered"); err != nil {
		t.Fatal(err)
	}
	got, err := app.resolveSandboxTarget("")
	if err != nil || got != "remembered" {
		t.Fatalf("resolveSandboxTarget() = %q, %v; want remembered", got, err)
	}
	if got, err := app.resolveSandboxTarget("explicit"); err != nil || got != "explicit" {
		t.Fatalf("resolve explicit target = %q, %v", got, err)
	}
	app.GlobalWorkspace = "team-b"
	if _, err := app.resolveSandboxTarget(""); err == nil || !strings.Contains(err.Error(), "no last-used sandbox") {
		t.Fatalf("missing scoped target error = %v", err)
	}
}

func TestRemoveSandboxesEmptyIsNoop(t *testing.T) {
	called := false
	if err := removeSandboxes(nil, func(string) error { called = true; return nil }); err != nil {
		t.Fatalf("removeSandboxes(empty) error = %v", err)
	}
	if called {
		t.Fatal("remove callback called for empty input")
	}
}
