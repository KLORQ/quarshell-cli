package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/KLORQ/quarshell-cli/internal/storage/templates"
	slogx "github.com/KLORQ/quarshell-core/slogx"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
	"gopkg.in/yaml.v3"
)

// SandboxCreateOpts are CLI flags for sandbox create.
type SandboxCreateOpts struct {
	Name       string
	Image      string
	Policy     string
	Labels     map[string]string
	GatewayURL string
	From       string // BYOC / community image alias
	GPU        bool
	GPUCount   int
	Argv       []string // after `--`: canonical main command
	// Providers are profile ids or existing instance names (OpenShell --provider).
	// On create: discover host env, ensure gateway instance, compose policy before start, attach.
	Providers []string
	// AutoProviders enables OpenShell-style inference from trailing argv.
	AutoProviders bool
	// NoAutoProviders forces ModeOff even if AutoProviders is set.
	NoAutoProviders bool
	// Env is non-secret create-time environment (KEY=VALUE). Credential-looking keys warn.
	Env map[string]string
	// NoCredentialWarnings suppresses --env credential-looking key warnings.
	NoCredentialWarnings bool

	Detach                  bool
	Editor                  string // vscode | cursor
	Forwards                []int
	ExposePort              int
	ExposeAuthorizationMode string
	Upload                  string
	NoGitIgnore             bool
	CPU                     float64
	Memory                  string
	Template                string
	RestartPolicy           string // never | on-failure | always
	GatewayWorkspace        string

	DriverConfigJSON string // --driver-config-json
	ApprovalMode     string // manual|auto
	NoKeep           bool   // delete sandbox after main command exits
	ForceTTY         bool   // --tty force a PTY for the canonical main process

}

// SandboxCreate creates and starts a sandbox.

// SandboxCreate creates and starts a sandbox.
func (a *App) SandboxCreate(opt SandboxCreateOpts) error {
	if opt.Template != "" {
		if err := validateTemplateCreateConflict(opt); err != nil {
			return err
		}
		template, err := templates.Get(opt.Template)
		if err == nil {
			mergeTemplateIntoCreate(&opt, template)
			opt.Template = ""
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sandbox create template %q: %w", opt.Template, err)
		}
	}
	return a.sandboxCreateOpenShell(opt)
}

func sandboxCreateUploadDestination(stagedPath, originalPath string) (string, error) {
	info, err := os.Lstat(stagedPath)
	if err != nil {
		return "", fmt.Errorf("inspect staged upload %q: %w", originalPath, err)
	}
	if info.IsDir() {
		return "/workspace", nil
	}
	return path.Join("/workspace", filepath.Base(originalPath)), nil
}

// SandboxList prints sandboxes.

// SandboxList prints sandboxes.
type SandboxListOpts struct {
	PageSize      int32
	PageToken     string
	IDs           bool
	Names         bool
	Selector      string
	AllWorkspaces bool
	Output        string
}

// SandboxList prints gateway-registered sandboxes using OpenShell list semantics.

// SandboxList prints gateway-registered sandboxes using OpenShell list semantics.
func (a *App) SandboxList(opt SandboxListOpts) error {
	if _, err := parseSandboxLabelSelector(opt.Selector); err != nil {
		return err
	}
	if opt.Output != "" && opt.Output != "table" && opt.Output != "json" && opt.Output != "yaml" {
		return fmt.Errorf("sandbox list: invalid output %q (expected table, json, or yaml)", opt.Output)
	}
	client, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	list, nextPageToken, err := client.ListSandboxesPage(ctx, opt.PageSize, opt.PageToken, opt.Selector, opt.AllWorkspaces)
	if err != nil {
		return fmt.Errorf("sandbox list: %w", err)
	}
	if opt.IDs || opt.Names {
		for _, sb := range list {
			if opt.IDs {
				fmt.Println(sb.ID)
			} else if opt.AllWorkspaces {
				workspace := sb.Workspace
				if workspace == "" {
					workspace = "default"
				}
				fmt.Printf("%s/%s\n", workspace, sb.Name)
			} else {
				fmt.Println(sb.Name)
			}
		}
		if nextPageToken != "" {
			fmt.Fprintf(os.Stderr, "Next page token: %s\n", nextPageToken)
		}
		return nil
	}
	switch opt.Output {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Sandboxes     []klorq.Sandbox `json:"sandboxes"`
			NextPageToken string          `json:"next_page_token,omitempty"`
		}{Sandboxes: list, NextPageToken: nextPageToken})
	case "yaml":
		return yaml.NewEncoder(os.Stdout).Encode(struct {
			Sandboxes     []klorq.Sandbox `yaml:"sandboxes"`
			NextPageToken string          `yaml:"next_page_token,omitempty"`
		}{Sandboxes: list, NextPageToken: nextPageToken})
	case "", "table":
	default:
	}
	if len(list) == 0 {
		fmt.Println("sandbox list: (none)")
		return nil
	}
	fmt.Printf("%-16s %-12s %-20s %s\n", "NAME", "ID", "STATUS", "IMAGE")
	for _, s := range list {
		fmt.Printf("%-16s %-12s %-20s %s\n", s.Name, shortID(s.ID), s.Status, s.Image)
	}
	if nextPageToken != "" {
		fmt.Fprintf(os.Stderr, "Next page token: %s\n", nextPageToken)
	}
	return nil
}

func parseSandboxLabelSelector(raw string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	pairs := 0
	for _, term := range strings.Split(raw, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		pairs++
		if pairs > 64 {
			return nil, fmt.Errorf("sandbox list: label selector exceeds 64 pair limit")
		}
		key, value, ok := strings.Cut(term, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok {
			return nil, fmt.Errorf("sandbox list: invalid selector %q (expected key1=value1,key2=value2)", raw)
		}
		if err := validateSandboxLabelKey(key); err != nil {
			return nil, fmt.Errorf("sandbox list: invalid selector %q: %w", raw, err)
		}
		if err := validateSandboxLabelValue(value); err != nil {
			return nil, fmt.Errorf("sandbox list: invalid selector %q: %w", raw, err)
		}
		// The upstream parser inserts into a map; repeated keys use the last value.
		out[key] = value
	}
	return out, nil
}

func validateSandboxLabelKey(key string) error {
	if key == "" {
		return fmt.Errorf("label key cannot be empty")
	}
	if len(key) > 253 {
		return fmt.Errorf("label key exceeds 253 characters")
	}
	prefix, name, hasPrefix := strings.Cut(key, "/")
	if !hasPrefix {
		name = prefix
		prefix = ""
	}
	if name == "" {
		return fmt.Errorf("label key name segment cannot be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("label key name segment exceeds 63 characters")
	}
	if err := validateLabelSegment(name); err != nil {
		return fmt.Errorf("label key name segment: %w", err)
	}
	if !hasPrefix {
		return nil
	}
	if prefix == "" {
		return fmt.Errorf("label key prefix cannot be empty when '/' is present")
	}
	if len(prefix) > 253 {
		return fmt.Errorf("label key prefix exceeds 253 characters")
	}
	for _, r := range prefix {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return fmt.Errorf("label key prefix must be a DNS subdomain")
		}
	}
	if strings.Contains(prefix, "..") || strings.ContainsAny(prefix[:1]+prefix[len(prefix)-1:], "-.") {
		return fmt.Errorf("label key prefix cannot start or end with '-' or '.', or contain consecutive dots")
	}
	return nil
}

func validateSandboxLabelValue(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 63 {
		return fmt.Errorf("label value exceeds 63 characters")
	}
	return validateLabelSegment(value)
}

func validateLabelSegment(value string) error {
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("contains invalid characters")
		}
	}
	first, _ := utf8.DecodeRuneInString(value)
	last, _ := utf8.DecodeLastRuneInString(value)
	if !unicode.IsLetter(first) && !unicode.IsNumber(first) {
		return fmt.Errorf("must start with alphanumeric character")
	}
	if !unicode.IsLetter(last) && !unicode.IsNumber(last) {
		return fmt.Errorf("must end with alphanumeric character")
	}
	return nil
}

// ListOpenShellSandboxes returns caller-visible sandboxes from the selected
// Gateway for the terminal UI and automation.
func (a *App) ListOpenShellSandboxes(ctx context.Context) ([]klorq.Sandbox, error) {
	client, err := a.gatewayClient()
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.ListControlSandboxes(ctx, a.GlobalWorkspace, false)
}

// SandboxStatus prints one sandbox.

// SandboxStatus prints one sandbox.
func (a *App) SandboxStatus(nameOrID string) error {
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	client, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("sandbox get: %w", err)
	}
	defer client.Close()
	sandbox, err := client.GetControlSandbox(ctx, a.GlobalWorkspace, nameOrID)
	if err != nil {
		return fmt.Errorf("sandbox get: %w", err)
	}
	fmt.Printf("name:           %s\n", sandbox.Name)
	fmt.Printf("id:             %s\n", sandbox.ID)
	fmt.Printf("workspace:      %s\n", sandbox.Workspace)
	fmt.Printf("registry_status: %s\n", sandbox.Status)
	fmt.Printf("runtime_status:  unavailable\n")
	fmt.Printf("image:          %s\n", sandbox.Image)
	return nil
}

// SandboxRemove deletes a sandbox.

// SandboxRemove deletes a sandbox.
func (a *App) SandboxRemove(nameOrID string) error {
	const op = "cli.sandbox.remove"
	log := a.op(op, slog.String("sandbox", nameOrID))
	log.Info("removing sandbox")
	ctx, cancel := a.withTimeout(TimeoutWait)
	defer cancel()
	client, err := a.lifecycleGatewaySandbox(ctx, nameOrID)
	if err != nil {
		return fmt.Errorf("sandbox rm: %w", err)
	}
	defer client.Close()
	if err := client.DeleteControlSandbox(ctx, a.workspaceName(), nameOrID); err != nil {
		log.Error("gateway failed to delete sandbox", slogx.Err(err))
		return fmt.Errorf("sandbox rm: %w", err)
	}
	a.clearLastSandbox(nameOrID)
	log.Info("sandbox removed by gateway")
	fmt.Printf("sandbox rm: ok %s\n", nameOrID)
	return nil
}

// SandboxDelete removes one or more sandboxes, or all registered sandboxes in
// the selected workspace. The registry is the source of truth for --all since
// local runtime listings do not carry workspace ownership metadata.
func (a *App) SandboxDelete(names []string, all bool) error {
	if all {
		client, err := a.gatewayClient()
		if err != nil {
			return fmt.Errorf("sandbox delete --all: %w", err)
		}
		ctx, cancel := a.withTimeout(TimeoutAPI)
		registered, err := client.ListControlSandboxes(ctx, a.GlobalWorkspace, false)
		cancel()
		if err != nil {
			return fmt.Errorf("sandbox delete --all: list workspace sandboxes: %w", err)
		}
		workspace := a.GlobalWorkspace
		if workspace == "" {
			workspace = "default"
		}
		names = names[:0]
		for _, sandbox := range registered {
			sandboxWorkspace := sandbox.Workspace
			if sandboxWorkspace == "" {
				sandboxWorkspace = "default"
			}
			if sandboxWorkspace == workspace {
				names = append(names, sandbox.Name)
			}
		}
		if len(names) == 0 {
			fmt.Println("No sandboxes to delete.")
			return nil
		}
	}
	return removeSandboxes(names, a.SandboxRemove)
}

func removeSandboxes(names []string, remove func(string) error) error {
	var failures []error
	for _, name := range names {
		if err := remove(name); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("sandbox delete: failed to remove %d of %d sandboxes: %w", len(failures), len(names), errors.Join(failures...))
	}
	return nil
}

// LogsOpts configures observation log streaming.

// LogsOpts configures observation log streaming.
type LogsOpts struct {
	Names    []string
	Follow   bool
	All      bool
	Lines    uint32
	LinesSet bool
	Since    string
	Source   string
	Level    string
}

// Logs streams sandbox container logs to stdout.

// Logs streams sandbox container logs to stdout.
func (a *App) Logs(name string, follow bool) error {
	return a.LogsOpts(LogsOpts{Names: []string{name}, Follow: follow})
}

// LogsOpts streams observation logs (gateway SSE preferred; docker fallback).

// LogsOpts streams observation logs (gateway SSE preferred; docker fallback).
func (a *App) LogsOpts(opt LogsOpts) error {
	return a.LogsToOpts(a.CommandContext(), opt, os.Stdout)
}

// LogsTo streams sandbox container logs to w (used by `quarshell term` live observation panel).

// LogsTo streams sandbox container logs to w (used by `quarshell term` live observation panel).
func (a *App) LogsTo(ctx context.Context, name string, follow bool, w io.Writer) error {
	return a.LogsToOpts(ctx, LogsOpts{Names: []string{name}, Follow: follow}, w)
}

// LogsToOpts streams one or more sandboxes.

// LogsToOpts streams one or more sandboxes.
func (a *App) LogsToOpts(ctx context.Context, opt LogsOpts, w io.Writer) error {
	if w == nil {
		w = os.Stdout
	}
	if ctx == nil {
		ctx = a.CommandContext()
	}
	if !opt.LinesSet && opt.Lines == 0 {
		opt.Lines = 200
	}
	names := opt.Names
	if len(names) == 0 && !opt.All {
		name, err := a.resolveSandboxTarget("")
		if err != nil {
			return fmt.Errorf("logs: %w", err)
		}
		names = []string{name}
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("logs: %w", err)
	}
	defer c.Close()
	var sandboxes []klorq.Sandbox
	if opt.All {
		sandboxes, err = c.ListControlSandboxes(ctx, "", true)
		if err != nil {
			return fmt.Errorf("logs: list visible sandboxes: %w", err)
		}
	} else {
		workspace := a.GlobalWorkspace
		if workspace == "" {
			workspace = "default"
		}
		sandboxes = make([]klorq.Sandbox, 0, len(names))
		for _, name := range names {
			sandboxes = append(sandboxes, klorq.Sandbox{Name: name, Workspace: workspace})
		}
	}
	if opt.Follow {
		return c.WatchControlSandboxSet(ctx, sandboxes, opt.Lines, opt.Since, gatewayLogSource(opt.Source), opt.Level, w)
	}
	for _, sandbox := range sandboxes {
		lines, err := c.GetControlSandboxLogsFiltered(ctx, sandbox.Workspace, sandbox.Name, opt.Lines, opt.Since, gatewayLogSource(opt.Source), opt.Level)
		if err != nil {
			return fmt.Errorf("logs: workspace %q sandbox %q: %w", sandbox.Workspace, sandbox.Name, err)
		}
		for _, line := range lines {
			prefix := sandbox.Name
			if opt.All {
				prefix = sandbox.Workspace + "/" + sandbox.Name
			}
			if len(sandboxes) == 1 {
				prefix = line.Source
				if prefix == "" {
					prefix = "proxy"
				}
			}
			fmt.Fprintf(w, "[%s] %s\n", prefix, line.Text)
		}
	}
	return nil
}

func gatewayLogSource(source string) string {
	if source == "all" {
		return ""
	}
	return source
}

// GatewayAdd registers a named gateway in config.
