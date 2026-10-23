package service

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/outfmt"
	"github.com/KLORQ/quarshell-cli/internal/providerflags"
	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	"github.com/KLORQ/quarshell-runtime/refresh"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
)

// ProviderGet prints provider metadata (no secret values).
func (a *App) ProviderGet(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	rec, err := c.GetProvider(a.apiCtx(), name)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Println(string(b))
	return nil
}

// ProviderDelete removes a provider instance and its encrypted credentials.
func (a *App) ProviderDelete(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	if err := c.DeleteProvider(a.apiCtx(), name); err != nil {
		return err
	}
	fmt.Printf("deleted provider %s\n", name)
	return nil
}

// ProviderProfileDelete removes a custom gateway profile.
func (a *App) ProviderProfileDelete(id string, scopeOptions ...string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	scope, workspace := a.profileScope(scopeOptions...)
	if err := c.DeleteProfileScoped(a.apiCtx(), id, scope, workspace); err != nil {
		return err
	}
	fmt.Printf("deleted profile %s\n", id)
	return nil
}

// ProviderRefresh re-injects credential values from the host env into the gateway store.
func (a *App) ProviderRefresh(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	rec, err := c.GetProvider(a.apiCtx(), name)
	if err != nil {
		return err
	}
	keys := rec.EnvVars
	if len(keys) == 0 {
		return fmt.Errorf("provider %s has no env_vars to refresh", name)
	}
	results, err := (refresh.FromEnv{}).Refresh(a.apiCtx(), keys)
	creds := map[string]string{}
	for _, r := range results {
		creds[r.Key] = r.Value
	}
	if len(creds) == 0 {
		return err
	}
	rec.Credentials = creds
	// Provider GET redacts refresh material. Preserve the gateway's encrypted
	// refresh configuration by omitting it from this credential-only update.
	rec.Refresh = nil
	if putErr := c.PutProvider(a.apiCtx(), rec); putErr != nil {
		return putErr
	}
	fmt.Printf("refreshed provider %s (%d keys from host env)\n", name, len(creds))
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: %v\n", err)
	}
	return nil
}

// ProviderRefreshStatus reports whether host env keys for a provider are set (not values).
func (a *App) ProviderRefreshStatus(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	rec, err := c.GetProvider(a.apiCtx(), name)
	if err != nil {
		return err
	}
	for _, k := range rec.EnvVars {
		_, ok := os.LookupEnv(k)
		state := "missing"
		if ok {
			state = "set"
		}
		fmt.Printf("%s\t%s\n", k, state)
	}
	return nil
}

// GatewayRemove drops a named gateway from local CLI config.
func (a *App) GatewayRemove(name string) error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Gateways == nil {
		return fmt.Errorf("no gateways configured")
	}
	if _, ok := cfg.Gateways[name]; !ok {
		return fmt.Errorf("gateway %q not found", name)
	}
	delete(cfg.Gateways, name)
	if cfg.Current == name {
		cfg.Current = ""
	}
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("removed gateway %s\n", name)
	return nil
}

// GatewayInfo prints the selected gateway's caller-visible control summary.
func (a *App) GatewayInfo() error {
	if err := a.GatewayEnsure(); err != nil {
		return err
	}
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	info, err := a.clientWithToken(u, a.gatewayTokenForURL(u)).ControlOverview(a.apiCtx())
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(info, "", "  ")
	fmt.Println(string(b))
	return nil
}

// GatewayLogin stores a bearer token for the current gateway (local config).
func (a *App) GatewayLogin(token string) error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Current == "" {
		return fmt.Errorf("no gateway selected")
	}
	g := cfg.Gateways[cfg.Current]
	g.Token = strings.TrimSpace(token)
	g.EdgeToken = ""
	g.RefreshToken = ""
	g.TokenExpiresAtMS = 0
	cfg.Gateways[cfg.Current] = g
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("logged in to gateway %s\n", cfg.Current)
	return nil
}

// GatewayLogout clears the stored token.
func (a *App) GatewayLogout() error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	if cfg.Current == "" {
		return fmt.Errorf("no gateway selected")
	}
	g := cfg.Gateways[cfg.Current]
	g.Token = ""
	g.EdgeToken = ""
	g.RefreshToken = ""
	g.TokenExpiresAtMS = 0
	cfg.Gateways[cfg.Current] = g
	if err := gwconfig.Save(cfg); err != nil {
		return err
	}
	fmt.Printf("logged out of gateway %s\n", cfg.Current)
	return nil
}

// WhoamiSnapshot is structured identity for -o json|yaml.
type WhoamiSnapshot struct {
	Gateway   string   `json:"gateway"`
	URL       string   `json:"url,omitempty"`
	Auth      string   `json:"auth"`
	Subject   string   `json:"subject,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	IdP       string   `json:"idp,omitempty"`
	GatewayID string   `json:"gateway_id,omitempty"`
}

// Whoami prints gateway identity + selected gateway name.
func (a *App) Whoami() error {
	cfg, _, err := gwconfig.Load()
	if err != nil {
		return err
	}
	snap := WhoamiSnapshot{
		Gateway: cfg.Current,
		Auth:    "anonymous",
		Subject: "anonymous",
	}
	if cfg.Current != "" {
		g := cfg.Gateways[cfg.Current]
		snap.URL = g.URL
		if strings.TrimSpace(g.Token) != "" {
			snap.Auth = "token"
		}
		cli := a.clientWithToken(g.URL, g.Token)
		ctx, cancel := a.withTimeout(TimeoutAPIShort)
		defer cancel()
		if who, err := cli.Whoami(ctx); err == nil {
			if s, ok := who["subject"].(string); ok && s != "" {
				snap.Subject = s
			}
			if auth, ok := who["auth"].(string); ok && auth != "" {
				snap.Auth = auth
			}
			if id, ok := who["gateway_id"].(string); ok {
				snap.GatewayID = id
			}
			if idp, ok := who["idp"].(string); ok {
				snap.IdP = idp
			}
			if roles, ok := who["roles"].([]any); ok {
				for _, r := range roles {
					if s, ok := r.(string); ok {
						snap.Roles = append(snap.Roles, s)
					}
				}
			}
		} else if info, err := cli.Info(ctx); err == nil {
			if id, ok := info["gateway_id"].(string); ok {
				snap.GatewayID = id
			}
		}
	}
	format := "text"
	if a != nil && a.OutputFormat != "" {
		format = a.OutputFormat
	}
	return outfmt.Emit(os.Stdout, format, func(w io.Writer) error {
		fmt.Fprintf(w, "gateway: %s\n", snap.Gateway)
		if snap.URL != "" {
			fmt.Fprintf(w, "url: %s\n", snap.URL)
		}
		fmt.Fprintf(w, "auth: %s\n", snap.Auth)
		if snap.Subject != "" {
			fmt.Fprintf(w, "subject: %s\n", snap.Subject)
		}
		if snap.GatewayID != "" {
			fmt.Fprintf(w, "gateway_id: %s\n", snap.GatewayID)
		}
		if snap.IdP != "" {
			fmt.Fprintf(w, "idp: %s\n", snap.IdP)
		}
		if len(snap.Roles) > 0 {
			fmt.Fprintf(w, "roles: %s\n", strings.Join(snap.Roles, ","))
		}
		return nil
	}, snap)
}

// localParityDir stores process bookkeeping for native TCP forwards.
func localParityDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "quarshell", "parity")
	return dir, os.MkdirAll(dir, 0o755)
}

func readJSONMap(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func writeJSONMap(path string, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// WorkspaceCreate registers a named workspace on the Gateway.
func (a *App) WorkspaceCreate(name string, labels ...map[string]string) error {
	workspaceLabels := map[string]string{}
	if len(labels) > 0 {
		for key, value := range labels[0] {
			workspaceLabels[key] = value
		}
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("workspace create requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if _, err := c.CreateWorkspaceWithLabels(ctx, name, workspaceLabels); err != nil {
		return err
	}
	fmt.Printf("workspace %s created\n", name)
	return nil
}

type WorkspaceListOptions struct {
	PageSize      int
	PageToken     string
	LabelSelector string
	Output        string
}

type workspaceListOutput struct {
	Workspaces    []workspaceListItem `json:"workspaces" yaml:"workspaces"`
	NextPageToken string              `json:"next_page_token" yaml:"next_page_token"`
}

type workspaceListItem struct {
	ID              string            `json:"id,omitempty" yaml:"id,omitempty"`
	Name            string            `json:"name" yaml:"name"`
	ResourceVersion *uint64           `json:"resource_version,omitempty" yaml:"resource_version,omitempty"`
	Status          string            `json:"status,omitempty" yaml:"status,omitempty"`
	CreatedAt       string            `json:"created_at,omitempty" yaml:"created_at,omitempty"`
	Labels          map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

func (a *App) WorkspaceList(options ...WorkspaceListOptions) error {
	opts := WorkspaceListOptions{PageSize: 100}
	if len(options) > 0 {
		opts = options[0]
		if opts.PageSize <= 0 {
			opts.PageSize = 100
		}
	}
	if opts.Output == "" && a != nil {
		opts.Output = a.OutputFormat
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("workspace list requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	page, err := c.ListWorkspacesPage(ctx, klorq.WorkspaceListOptions{
		PageSize: opts.PageSize, PageToken: opts.PageToken, LabelSelector: opts.LabelSelector,
	})
	if err != nil {
		return err
	}
	items := make([]workspaceListItem, 0, len(page.Workspaces))
	for _, workspace := range page.Workspaces {
		resourceVersion := workspace.ResourceVersion
		items = append(items, workspaceListItem{
			ID: workspace.ID, Name: workspace.Name, ResourceVersion: &resourceVersion,
			Status: workspace.Status, CreatedAt: formatWorkspaceTimestamp(workspace.CreatedAt), Labels: workspace.Labels,
		})
	}
	return emitWorkspaceList(opts.Output, workspaceListOutput{Workspaces: items, NextPageToken: page.NextPageToken})
}

func parseWorkspaceLabelSelector(selector string) (map[string]string, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, nil
	}
	requirements := make(map[string]string)
	pairs := 0
	for _, rawPair := range strings.Split(selector, ",") {
		pair := strings.TrimSpace(rawPair)
		if pair == "" {
			continue
		}
		pairs++
		if pairs > 64 {
			return nil, fmt.Errorf("label selector exceeds 64 pair limit")
		}
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label selector: expected 'key=value', got %q", pair)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if err := validateSandboxLabelKey(key); err != nil {
			return nil, fmt.Errorf("invalid label selector key %q: %w", key, err)
		}
		if err := validateSandboxLabelValue(value); err != nil {
			return nil, fmt.Errorf("invalid label selector value for %q: %w", key, err)
		}
		requirements[key] = value
	}
	return requirements, nil
}

func workspaceLabelsMatch(labels map[string]any, requirements map[string]string) bool {
	for key, want := range requirements {
		got, ok := labels[key].(string)
		if !ok || got != want {
			return false
		}
	}
	return true
}

func formatWorkspaceTimestamp(value string) string {
	if value == "" {
		return ""
	}
	created, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return created.UTC().Format("2006-01-02 15:04:05")
}

func emitWorkspaceList(output string, page workspaceListOutput) error {
	return writeWorkspaceListWithToken(os.Stdout, os.Stderr, output, page)
}

func writeWorkspaceList(w io.Writer, output string, page workspaceListOutput) error {
	return writeWorkspaceListWithToken(w, w, output, page)
}

func writeWorkspaceListWithToken(w, tokenWriter io.Writer, output string, page workspaceListOutput) error {
	if err := outfmt.Emit(w, output, func(w io.Writer) error {
		if len(page.Workspaces) == 0 {
			_, err := fmt.Fprintln(w, "No workspaces found.")
			return err
		}
		nameWidth := len("NAME")
		for _, workspace := range page.Workspaces {
			nameWidth = max(nameWidth, len(workspace.Name))
		}
		if _, err := fmt.Fprintf(w, "%-*s  %-12s  %-20s  %s\n", nameWidth, "NAME", "STATUS", "CREATED", "LABELS"); err != nil {
			return err
		}
		for _, workspace := range page.Workspaces {
			labels := make([]string, 0, len(workspace.Labels))
			for key, value := range workspace.Labels {
				labels = append(labels, key+"="+value)
			}
			sort.Strings(labels)
			if _, err := fmt.Fprintf(w, "%-*s  %-12s  %-20s  %s\n", nameWidth, workspace.Name, workspace.Status, workspace.CreatedAt, strings.Join(labels, ",")); err != nil {
				return err
			}
		}
		return nil
	}, page); err != nil {
		return err
	}
	if output == "json" || output == "yaml" || output == "yml" || page.NextPageToken == "" {
		return nil
	}
	_, err := fmt.Fprintf(tokenWriter, "Next page token: %s\n", page.NextPageToken)
	return err
}

func (a *App) WorkspaceGet(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("workspace get requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	ws, err := c.GetWorkspace(ctx, name)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(ws, "", "  ")
	fmt.Println(string(b))
	return nil
}

func (a *App) WorkspaceDelete(name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("workspace delete requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.DeleteWorkspace(ctx, name); err != nil {
		return err
	}
	fmt.Printf("workspace %s deleted\n", name)
	return nil
}

func (a *App) WorkspaceMemberAdd(ws, member string) error {
	return a.WorkspaceMemberAddRole(ws, member, "user")
}

func (a *App) WorkspaceMemberAddRole(ws, member, role string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("workspace member add requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.WorkspaceMemberAdd(ctx, ws, member, role); err != nil {
		return err
	}
	fmt.Printf("workspace member add: %s → %s role=%s\n", member, ws, role)
	return nil
}

func (a *App) WorkspaceMemberRemove(ws, member string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("workspace member remove requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.WorkspaceMemberRemove(ctx, ws, member); err != nil {
		return err
	}
	fmt.Printf("workspace member remove: %s ← %s\n", member, ws)
	return nil
}

func (a *App) WorkspaceMemberList(ws string) error {
	return a.WorkspaceMemberListPage(ws, 100, "", "table")
}

type workspaceMemberListItem struct {
	Subject string `json:"subject" yaml:"subject"`
	Role    string `json:"role" yaml:"role"`
}
type workspaceMemberListOutput struct {
	Members       []workspaceMemberListItem `json:"members" yaml:"members"`
	NextPageToken string                    `json:"next_page_token" yaml:"next_page_token"`
}

func (a *App) WorkspaceMemberListPage(ws string, pageSize int32, pageToken, output string) error {
	if strings.TrimSpace(ws) == "" {
		return fmt.Errorf("workspace is required")
	}
	if pageSize <= 0 {
		return fmt.Errorf("workspace member list: --page-size must be a positive integer")
	}
	if output != "table" && output != "yaml" && output != "json" {
		return fmt.Errorf("workspace member list: output must be table|yaml|json")
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("workspace member list requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	page, err := c.ListWorkspaceMembersPage(ctx, ws, pageSize, pageToken)
	if err != nil {
		return err
	}
	data := workspaceMemberListOutput{Members: make([]workspaceMemberListItem, 0, len(page.Members)), NextPageToken: page.NextPageToken}
	for _, member := range page.Members {
		data.Members = append(data.Members, workspaceMemberListItem{Subject: member.Subject, Role: member.Role})
	}
	if err := outfmt.Emit(os.Stdout, output, func(w io.Writer) error {
		for _, member := range page.Members {
			if _, err := fmt.Fprintf(w, "%s\trole=%s\n", member.Subject, member.Role); err != nil {
				return err
			}
		}
		return nil
	}, data); err != nil {
		return err
	}
	if output == "table" && page.NextPageToken != "" {
		fmt.Fprintf(os.Stderr, "Next page token: %s\n", page.NextPageToken)
	}
	return nil
}

func (a *App) ServiceExpose(sandbox, name, port string, authorizationModes ...string) error {
	authorizationMode := "strip"
	if len(authorizationModes) > 0 && strings.TrimSpace(authorizationModes[0]) != "" {
		authorizationMode = strings.TrimSpace(authorizationModes[0])
	}
	if authorizationMode != "strip" && authorizationMode != "bearer-passthrough" {
		return fmt.Errorf("unsupported service authorization mode %q; choose strip or bearer-passthrough", authorizationMode)
	}
	guestPort, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || guestPort <= 0 {
		return fmt.Errorf("invalid port %q", port)
	}
	if name == "" {
		name = sandbox
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("service expose requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	mode := klorq.ServiceAuthorizationModeStrip
	if authorizationMode == "bearer-passthrough" {
		mode = klorq.ServiceAuthorizationModeBearerPassthrough
	}
	service, err := c.ExposeServiceWithOptions(ctx, sandbox, name, uint32(guestPort), true, mode)
	if err != nil {
		return err
	}
	fmt.Printf("service %s exposed on %s:%d\n", name, sandbox, guestPort)
	fmt.Printf("  %s\n", service.URL)
	return nil
}

// ProviderRefreshConfigure sets gateway refresh strategy for a credential key.
func (a *App) ProviderRefreshConfigure(opt providerflags.RefreshConfigureArgs) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.ConfigureProviderRefresh(ctx, opt.Name, opt.CredKey, opt.Strategy, opt.Material, opt.ExpiresAtMS, opt.SecretMaterialKeys...); err != nil {
		return err
	}
	fmt.Printf("provider refresh configured: %s key=%s strategy=%s\n", opt.Name, opt.CredKey, opt.Strategy)
	return nil
}

// ProviderRefreshRotate triggers credential rotation on the gateway host env.
func (a *App) ProviderRefreshRotate(name, key string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.RotateProviderRefresh(ctx, name, key); err != nil {
		return err
	}
	fmt.Printf("provider refresh rotate: %s key=%s\n", name, key)
	return nil
}

// ProviderRefreshDelete removes refresh configuration for a credential key.
func (a *App) ProviderRefreshDelete(name, key string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.DeleteProviderRefresh(ctx, name, key); err != nil {
		return err
	}
	fmt.Printf("provider refresh delete: %s key=%s\n", name, key)
	return nil
}

type ServiceListOptions struct {
	Sandbox       string
	PageSize      int
	PageToken     string
	AllWorkspaces bool
	Output        string
}

type ServiceListItem struct {
	ID                string `json:"id,omitempty" yaml:"id,omitempty"`
	Name              string `json:"name" yaml:"name"`
	Sandbox           string `json:"sandbox" yaml:"sandbox"`
	Port              int    `json:"port" yaml:"port"`
	SandboxID         string `json:"sandbox_id,omitempty" yaml:"sandbox_id,omitempty"`
	Workspace         string `json:"workspace,omitempty" yaml:"workspace,omitempty"`
	URL               string `json:"url,omitempty" yaml:"url,omitempty"`
	Domain            bool   `json:"domain" yaml:"domain"`
	AuthorizationMode string `json:"authorization_mode,omitempty" yaml:"authorization_mode,omitempty"`
}

type serviceListOutput struct {
	Services      []ServiceListItem `json:"services" yaml:"services"`
	NextPageToken string            `json:"next_page_token,omitempty" yaml:"next_page_token,omitempty"`
}

func (a *App) ServiceList(sandboxFilter ...string) error {
	options := ServiceListOptions{PageSize: 100, Output: "json"}
	if len(sandboxFilter) > 0 {
		options.Sandbox = strings.TrimSpace(sandboxFilter[0])
	}
	return a.ServiceListPage(options)
}

func (a *App) ServiceListPage(options ServiceListOptions) error {
	if options.PageSize == 0 {
		options.PageSize = 100
	}
	if options.PageSize < 0 {
		return fmt.Errorf("service --page-size must not be negative")
	}
	if options.Output == "" {
		options.Output = "table"
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("service list requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	list, next, err := c.ListServicesPage(ctx, options.Sandbox, int32(options.PageSize), options.PageToken, options.AllWorkspaces)
	if err != nil {
		return err
	}
	items := make([]ServiceListItem, 0, len(list))
	for _, item := range list {
		items = append(items, ServiceListItem{
			ID: item.ID, Name: item.Name, Sandbox: item.Sandbox, Port: item.Port,
			SandboxID: item.SandboxID, Workspace: item.Workspace,
			URL: item.URL, Domain: item.Domain, AuthorizationMode: item.AuthorizationMode,
		})
	}
	return emitServiceList(options.Output, serviceListOutput{Services: items, NextPageToken: next})
}

func emitServiceList(output string, page serviceListOutput) error {
	if err := outfmt.Emit(os.Stdout, output, func(w io.Writer) error {
		if len(page.Services) == 0 {
			_, err := fmt.Fprintln(w, "No services found.")
			return err
		}
		if _, err := fmt.Fprintf(w, "%-32s  %-24s  %-6s  %s\n", "NAME", "SANDBOX", "PORT", "URL"); err != nil {
			return err
		}
		for _, item := range page.Services {
			if _, err := fmt.Fprintf(w, "%-32s  %-24s  %-6d  %s\n", item.Name, item.Sandbox, item.Port, item.URL); err != nil {
				return err
			}
		}
		return nil
	}, page); err != nil {
		return err
	}
	if output == "table" && page.NextPageToken != "" {
		fmt.Fprintf(os.Stderr, "Next page token: %s\n", page.NextPageToken)
	}
	return nil
}

func (a *App) ServiceGet(sandbox, name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("service get requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	rec, err := c.GetSandboxService(ctx, sandbox, name)
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	fmt.Println(string(b))
	return nil
}

func (a *App) ServiceDelete(sandbox, name string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("service delete requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if err := c.DeleteSandboxService(ctx, sandbox, name); err != nil {
		return err
	}
	fmt.Printf("service %s deleted\n", name)
	return nil
}

func (a *App) RuleList() error {
	return a.RuleListFilter("", "")
}

func (a *App) RuleReject(id string) error {
	return a.RuleRejectReason(id, "")
}
