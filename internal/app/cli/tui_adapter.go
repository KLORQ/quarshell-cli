package cli

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/KLORQ/quarshell-cli/internal/service"
	tuipkg "github.com/KLORQ/quarshell-cli/internal/tui"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
)

// tuiServiceControl adapts local CLI services to the renderer's narrow port.
// Keeping this adapter in CLI composition prevents the TUI package from
// depending on service internals and leaves room for a public SDK adapter.
type tuiServiceControl struct{ app *service.App }

func (c tuiServiceControl) ListSandboxes(ctx context.Context) ([]tuipkg.Sandbox, error) {
	items, err := c.app.ListOpenShellSandboxes(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]tuipkg.Sandbox, 0, len(items))
	for _, item := range items {
		result = append(result, tuipkg.Sandbox{Name: item.Name, Status: item.Status, Image: item.Image})
	}
	return result, nil
}

func (c tuiServiceControl) Logs(ctx context.Context, opts tuipkg.LogOptions, out io.Writer) error {
	return c.app.LogsToOpts(ctx, service.LogsOpts{Names: opts.Names, All: opts.All, Follow: opts.Follow, Source: logSourceForService(opts.Source)}, out)
}

func (tuiServiceControl) LogSources() []string { return []string{"all", "sandbox", "proxy"} }

func logSourceForService(source string) string {
	if source == "" {
		return "all"
	}
	return source
}

var _ tuipkg.Control = tuiServiceControl{}

type tuiGatewayAPI interface {
	ListControlSandboxes(context.Context, string, bool) ([]klorq.Sandbox, error)
	GetControlSandboxLogs(context.Context, string, string, uint32) ([]klorq.LogLine, error)
	WatchControlSandboxLogs(context.Context, string, string, uint32, io.Writer) error
	WatchControlSandboxLogsFiltered(context.Context, string, string, uint32, string, string, string, io.Writer) error
	WatchControlSandboxSet(context.Context, []klorq.Sandbox, uint32, string, string, string, io.Writer) error
}

type tuiGatewayControl struct {
	openClient func() (tuiGatewayAPI, func() error, error)
	workspace  string
}

func (c tuiGatewayControl) ListSandboxes(ctx context.Context) ([]tuipkg.Sandbox, error) {
	client, closeClient, err := c.openClient()
	if err != nil {
		return nil, err
	}
	defer closeClient()
	items, err := client.ListControlSandboxes(ctx, c.workspace, false)
	if err != nil {
		return nil, err
	}
	result := make([]tuipkg.Sandbox, 0, len(items))
	for _, item := range items {
		result = append(result, tuipkg.Sandbox{Name: item.Name, Status: item.Status, Image: item.Image})
	}
	return result, nil
}

func (c tuiGatewayControl) Logs(ctx context.Context, opts tuipkg.LogOptions, out io.Writer) error {
	if out == nil {
		return fmt.Errorf("TUI gateway logs: writer is required")
	}
	client, closeClient, err := c.openClient()
	if err != nil {
		return err
	}
	defer closeClient()
	if opts.Follow {
		sandboxes, err := c.selectedSandboxes(ctx, client, opts)
		if err != nil {
			return err
		}
		if len(sandboxes) == 1 {
			return client.WatchControlSandboxLogsFiltered(ctx, sandboxes[0].Workspace, sandboxes[0].Name, 200, "", gatewayTUILogSource(opts.Source), "", out)
		}
		return client.WatchControlSandboxSet(ctx, sandboxes, 200, "", gatewayTUILogSource(opts.Source), "", out)
	}
	sandboxes, err := c.selectedSandboxes(ctx, client, opts)
	if err != nil {
		return err
	}
	for _, sandbox := range sandboxes {
		lines, err := client.GetControlSandboxLogs(ctx, sandbox.Workspace, sandbox.Name, 200)
		if err != nil {
			return err
		}
		for _, line := range lines {
			if _, err := fmt.Fprintln(out, line.Text); err != nil {
				return err
			}
		}
	}
	return nil
}

func (tuiGatewayControl) LogSources() []string { return []string{"all", "gateway", "sandbox"} }

func gatewayTUILogSource(source string) string {
	if source == "all" {
		return ""
	}
	return source
}

func (c tuiGatewayControl) selectedSandboxes(ctx context.Context, client tuiGatewayAPI, opts tuipkg.LogOptions) ([]klorq.Sandbox, error) {
	workspace, allWorkspaces := c.workspace, false
	if opts.All {
		workspace, allWorkspaces = "", true
	}
	items, err := client.ListControlSandboxes(ctx, workspace, allWorkspaces)
	if err != nil {
		return nil, err
	}
	if opts.All || len(opts.Names) == 0 {
		return items, nil
	}
	selected := make([]klorq.Sandbox, 0, len(opts.Names))
	for _, item := range items {
		if slices.Contains(opts.Names, item.Name) {
			selected = append(selected, item)
		}
	}
	return selected, nil
}

var _ tuipkg.Control = tuiGatewayControl{}

func newTUIControl(app *service.App) (tuipkg.Control, func() error, error) {
	url, workspace, _, configured, err := app.GatewayTUIConfig()
	if err != nil {
		return nil, nil, err
	}
	if !configured {
		return tuiServiceControl{app: app}, func() error { return nil }, nil
	}
	openClient := func() (tuiGatewayAPI, func() error, error) {
		currentURL, currentWorkspace, token, current, err := app.GatewayTUIConfig()
		if err != nil {
			return nil, nil, err
		}
		if !current || currentURL != url || currentWorkspace != workspace {
			return nil, nil, fmt.Errorf("selected gateway session changed while TUI was running")
		}
		client := klorq.NewWithToken(currentURL, token)
		client.SetInsecureSkipVerify(app.GatewayInsecure)
		return client, client.Close, nil
	}
	return tuiGatewayControl{openClient: openClient, workspace: workspace}, func() error { return nil }, nil
}
