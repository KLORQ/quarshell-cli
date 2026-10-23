package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/KLORQ/quarshell-cli/internal/storage/sandboxstate"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
	"github.com/KLORQ/quarshell-slogx"
)

// SandboxStop stops a sandbox without deleting network/volume.
func (a *App) SandboxStop(name string) error {
	var err error
	name, err = a.resolveSandboxTarget(name)
	if err != nil {
		return fmt.Errorf("sandbox stop: %w", err)
	}
	const op = "cli.sandbox.stop"
	log := a.op(op, slog.String("sandbox", name))
	log.Info("stopping sandbox")
	ctx, cancel := a.withTimeout(TimeoutWait)
	defer cancel()
	client, err := a.lifecycleGatewaySandbox(ctx, name)
	if err != nil {
		return fmt.Errorf("sandbox stop: %w", err)
	}
	defer client.Close()
	if _, err := client.StopControlSandbox(ctx, a.workspaceName(), name); err != nil {
		log.Error("gateway failed to stop sandbox", slogx.Err(err))
		return fmt.Errorf("sandbox stop: %w", err)
	}
	a.rememberLastSandbox(name, "")
	log.Info("sandbox stopped by gateway")
	fmt.Printf("sandbox stop: ok %s\n", name)
	return nil
}

// SandboxStart starts a previously stopped sandbox (volume retained).
func (a *App) SandboxStart(name string) error {
	var err error
	name, err = a.resolveSandboxTarget(name)
	if err != nil {
		return fmt.Errorf("sandbox start: %w", err)
	}
	const op = "cli.sandbox.start"
	log := a.op(op, slog.String("sandbox", name))
	log.Info("starting sandbox")
	ctx, cancel := a.withTimeout(TimeoutWait)
	defer cancel()
	client, err := a.lifecycleGatewaySandbox(ctx, name)
	if err != nil {
		return fmt.Errorf("sandbox start: %w", err)
	}
	defer client.Close()
	if _, err := client.StartControlSandbox(ctx, a.workspaceName(), name); err != nil {
		log.Error("gateway failed to start sandbox", slogx.Err(err))
		return fmt.Errorf("sandbox start: %w", err)
	}
	a.rememberLastSandbox(name, "")
	log.Info("sandbox started by gateway")
	fmt.Printf("sandbox start: ok %s\n", name)
	return nil
}

// lifecycleGatewaySandbox resolves a sandbox through the native OpenShell
// Gateway API. CLI-local runtime fallbacks are retired with the Go port.
func (a *App) lifecycleGatewaySandbox(ctx context.Context, name string) (*klorq.Client, error) {
	u, err := a.currentGatewayURL()
	if err != nil {
		return nil, fmt.Errorf("select an OpenShell Gateway: %w", err)
	}
	client := a.clientFor(u)
	if _, err = client.GetControlSandbox(ctx, a.workspaceName(), name); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func (a *App) workspaceName() string {
	if a.GlobalWorkspace == "" {
		return "default"
	}
	return a.GlobalWorkspace
}

func (a *App) resolveSandboxTarget(name string) (string, error) {
	if name != "" {
		return name, nil
	}
	gateway, err := a.currentGatewayURL()
	if err != nil {
		return "", fmt.Errorf("select an OpenShell Gateway: %w", err)
	}
	workspace := a.GlobalWorkspace
	if workspace == "" {
		workspace = "default"
	}
	name, err = sandboxstate.GetLast(gateway, workspace)
	if err != nil {
		return "", fmt.Errorf("load last sandbox: %w", err)
	}
	if name == "" {
		return "", fmt.Errorf("no sandbox name provided and no last-used sandbox; create or select a sandbox first")
	}
	return name, nil
}

func (a *App) rememberLastSandbox(name, gateway string) {
	if name == "" {
		return
	}
	if gateway == "" {
		url, err := a.currentGatewayURL()
		if err != nil {
			slog.Warn("could not save last-used sandbox without a Gateway", "error", err)
			return
		}
		gateway = url
	}
	gateway = strings.TrimRight(gateway, "/")
	workspace := a.GlobalWorkspace
	if workspace == "" {
		workspace = "default"
	}
	if err := sandboxstate.SetLast(gateway, workspace, name); err != nil {
		slog.Warn("could not save last-used sandbox", "error", err)
	}
}

func (a *App) clearLastSandbox(name string) {
	if name == "" {
		return
	}
	url, err := a.currentGatewayURL()
	if err != nil {
		return
	}
	gateway := strings.TrimRight(url, "/")
	workspace := a.workspaceName()
	last, err := sandboxstate.GetLast(gateway, workspace)
	if err == nil && last == name {
		if err := sandboxstate.SetLast(gateway, workspace, ""); err != nil {
			slog.Warn("could not clear last-used sandbox", "error", err)
		}
	}
}

// Copy transfers files: host→sandbox or sandbox→host.
// Specs look like "localpath" and "name:/path" (sandbox side has a colon).
func (a *App) Copy(src, dst string) error {
	ctx, cancel := a.withTimeout(TimeoutWork)
	defer cancel()
	return a.copy(ctx, src, dst)
}

// SandboxUpload uploads a path with OpenShell-compatible .gitignore
// selection unless noGitIgnore is explicitly set.
func (a *App) SandboxUpload(src, dst string, noGitIgnore bool) error {
	ctx, cancel := a.withTimeout(TimeoutWork)
	defer cancel()
	sandboxName, _, sandboxTarget := splitSandboxPath(dst)
	if !sandboxTarget {
		return fmt.Errorf("sandbox upload: destination must be <sandbox>:/path")
	}
	client, err := a.resolveTransferRoute(ctx, sandboxName)
	if err != nil {
		return fmt.Errorf("sandbox upload: %w", err)
	}
	defer client.Close()
	staged, cleanup, filtered, err := prepareGitAwareUpload(ctx, src, !noGitIgnore)
	if err != nil {
		return err
	}
	defer client.Close()
	defer cleanup()
	if filtered {
		fmt.Fprintf(os.Stderr, "sandbox upload: applying .gitignore rules to %s\n", src)
	} else if !noGitIgnore {
		fmt.Fprintf(os.Stderr, "sandbox upload: Git filtering was not applied to %s (outside a Git work tree or symlink source)\n", src)
	}
	_, remotePath, _ := splitSandboxPath(dst)
	return uploadGatewayPath(ctx, client, a.workspaceName(), sandboxName, staged, remotePath, TimeoutWork)
}

func (a *App) copy(ctx context.Context, src, dst string) error {
	sName, sPath, sOK := splitSandboxPath(src)
	dName, dPath, dOK := splitSandboxPath(dst)
	if sOK == dOK {
		return fmt.Errorf("usage: cautem cp <local> <sandbox>:/path  OR  cautem cp <sandbox>:/path <local>")
	}
	sandboxName := sName
	if dOK {
		sandboxName = dName
	}
	client, err := a.resolveTransferRoute(ctx, sandboxName)
	if err != nil {
		return err
	}
	defer client.Close()
	if !sOK && dOK {
		return uploadGatewayPath(ctx, client, a.workspaceName(), sandboxName, src, dPath, TimeoutWork)
	}
	return downloadGatewayPath(ctx, client, a.workspaceName(), sandboxName, sPath, dst, TimeoutWork)
}

func (a *App) resolveTransferRoute(ctx context.Context, sandboxName string) (*klorq.Client, error) {
	client, err := a.lifecycleGatewaySandbox(ctx, sandboxName)
	if err != nil {
		return nil, fmt.Errorf("sandbox transfer: %w", err)
	}
	return client, nil
}

func splitSandboxPath(s string) (name, path string, ok bool) {
	// Avoid treating Windows drive letters; we only support name:/abs
	i := strings.Index(s, ":/")
	if i <= 0 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
