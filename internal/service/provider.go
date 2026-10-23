package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/KLORQ/quarshell-cli/internal/outfmt"
	"github.com/KLORQ/quarshell-cli/internal/providerflags"
	"github.com/KLORQ/quarshell-cli/internal/storage/gwconfig"
	"github.com/KLORQ/quarshell-core/env"
	"github.com/KLORQ/quarshell-policy/provider"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
	"gopkg.in/yaml.v3"
)

// ProviderProfileList lists builtin + custom profiles on the current gateway.
func (a *App) ProviderProfileList(scopeOptions ...string) error {
	scope, workspace := a.profileScope(scopeOptions...)
	return a.ProviderProfileListPage(scope, workspace, 100, "", "table")
}

type providerProfileListItem struct {
	ID              string `json:"id" yaml:"id"`
	Category        string `json:"category" yaml:"category"`
	Source          string `json:"source" yaml:"source"`
	Scope           string `json:"scope" yaml:"scope"`
	ResourceVersion uint64 `json:"resource_version,omitempty" yaml:"resource_version,omitempty"`
}
type providerProfileListOutput struct {
	Profiles      []providerProfileListItem `json:"profiles" yaml:"profiles"`
	NextPageToken string                    `json:"next_page_token" yaml:"next_page_token"`
}

func (a *App) ProviderProfileListPage(scope, workspace string, pageSize int32, pageToken, output string) error {
	if pageSize <= 0 {
		return fmt.Errorf("provider profile list: --page-size must be a positive integer")
	}
	if output != "table" && output != "yaml" && output != "json" {
		return fmt.Errorf("provider profile list: output must be table|yaml|json")
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	page, err := c.ListProviderProfilesPage(a.apiCtx(), scope, workspace, pageSize, pageToken)
	if err != nil {
		return err
	}
	data := providerProfileListOutput{Profiles: make([]providerProfileListItem, 0, len(page.Profiles)), NextPageToken: page.NextPageToken}
	for _, profile := range page.Profiles {
		data.Profiles = append(data.Profiles, providerProfileListItem{ID: profile.ID, Category: profile.Category, Source: profile.Source, Scope: profile.Scope, ResourceVersion: profile.ResourceVersion})
	}
	if err := outfmt.Emit(os.Stdout, output, func(w io.Writer) error {
		if _, err := fmt.Fprintln(w, "NAME\tTYPE\tCATEGORY\tSOURCE\tSCOPE"); err != nil {
			return err
		}
		for _, p := range data.Profiles {
			if _, err := fmt.Fprintf(w, "%s\tprovider\t%s\t%s\t%s\n", p.ID, p.Category, p.Source, p.Scope); err != nil {
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

// ProviderProfileImport uploads a profile YAML to the gateway.
func (a *App) ProviderProfileImport(path string, scopeOptions ...string) error {
	profiles, err := readProfiles(path)
	if err != nil {
		return err
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	scope, workspace := a.profileScope(scopeOptions...)
	for _, item := range profiles {
		if err := c.CreateProfileScoped(a.apiCtx(), item.profile.ID, item.data, scope, workspace); err != nil {
			return fmt.Errorf("profile import %s: %w", item.profile.ID, err)
		}
		fmt.Printf("imported profile %s\n", item.profile.ID)
	}
	return nil
}

type profileDocument struct {
	profile provider.Profile
	data    []byte
}

func readProfiles(source string) ([]profileDocument, error) {
	if strings.HasPrefix(source, "https://") {
		client := &http.Client{
			Timeout: TimeoutAPI,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if req.URL.Scheme != "https" {
					return fmt.Errorf("profile download redirect must use HTTPS")
				}
				if len(via) >= 5 {
					return fmt.Errorf("profile download: too many redirects")
				}
				return nil
			},
		}
		res, err := client.Get(source)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, fmt.Errorf("profile download: %s", res.Status)
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		if err != nil {
			return nil, err
		}
		if len(b) > 1<<20 {
			return nil, fmt.Errorf("profile download exceeds 1 MiB limit")
		}
		p, err := provider.ParseYAML(b)
		if err != nil {
			return nil, err
		}
		return []profileDocument{{profile: p, data: b}}, nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	paths := []string{source}
	if info.IsDir() {
		entries, err := os.ReadDir(source)
		if err != nil {
			return nil, err
		}
		paths = nil
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".json") {
				paths = append(paths, filepath.Join(source, entry.Name()))
			}
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no YAML/JSON profiles found in %s", source)
	}
	out := make([]profileDocument, 0, len(paths))
	ids := map[string]struct{}{}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		p, err := provider.ParseYAML(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if _, exists := ids[p.ID]; exists {
			return nil, fmt.Errorf("duplicate profile id %q in import source", p.ID)
		}
		ids[p.ID] = struct{}{}
		out = append(out, profileDocument{profile: p, data: b})
	}
	return out, nil
}

// ProviderProfileUpdate replaces an imported profile already present on the gateway.
func (a *App) ProviderProfileUpdate(path string, scopeOptions ...string) error {
	items, err := readProfiles(path)
	if err != nil {
		return err
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	scope, workspace := a.profileScope(scopeOptions...)
	for _, item := range items {
		_, source, currentVersion, err := c.GetProfileScoped(a.apiCtx(), item.profile.ID, scope, workspace)
		if err != nil {
			return fmt.Errorf("profile update %s: read current version: %w", item.profile.ID, err)
		}
		if source != "custom" {
			return fmt.Errorf("profile update %s: profile is not imported on gateway", item.profile.ID)
		}
		version, parseErr := strconv.ParseUint(currentVersion, 10, 64)
		if parseErr != nil || item.profile.ResourceVersion == 0 || item.profile.ResourceVersion != version {
			return fmt.Errorf("profile update %s: resource_version required; export the current profile and preserve its version", item.profile.ID)
		}
		if err := c.PutProfileScoped(a.apiCtx(), item.profile.ID, item.data, currentVersion, scope, workspace); err != nil {
			return err
		}
		fmt.Printf("updated profile %s\n", item.profile.ID)
	}
	return nil
}

// ProviderProfileLint validates one or more local or HTTPS provider profiles.
func (a *App) ProviderProfileLint(source string) error {
	profiles, err := readProfiles(source)
	if err != nil {
		return err
	}
	for _, item := range profiles {
		if err := item.profile.ValidateRuntime(); err != nil {
			return err
		}
		fmt.Printf("profile %s: valid\n", item.profile.ID)
	}
	return nil
}

// ProviderProfileShow prints a local builtin/custom file or validates path.
func (a *App) ProviderProfileShow(idOrPath string) error {
	return a.ProviderProfileShowFmt(idOrPath, "yaml")
}

// ProviderProfileShowFmt prints a profile as yaml or json wrapper.
func (a *App) ProviderProfileShowFmt(idOrPath, format string, scopeOptions ...string) error {
	var b []byte
	var err error
	if strings.Contains(idOrPath, "/") || strings.HasSuffix(idOrPath, ".yaml") || strings.HasSuffix(idOrPath, ".yml") {
		b, err = os.ReadFile(idOrPath)
	} else {
		if c, clientErr := a.gatewayClient(); clientErr == nil {
			scope, workspace := a.profileScope(scopeOptions...)
			b, _, _, err = c.GetProfileScoped(a.apiCtx(), idOrPath, scope, workspace)
		}
		if err != nil || len(b) == 0 {
			dir := provider.FindBuiltinDir()
			if dir == "" {
				return fmt.Errorf("provider profile %q not found in gateway or local catalog", idOrPath)
			}
			b, err = os.ReadFile(filepath.Join(dir, idOrPath+".yaml"))
		}
	}
	if err != nil {
		return err
	}
	switch strings.ToLower(format) {
	case "json":
		var doc any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	default:
		fmt.Print(string(b))
		return nil
	}
}

// ProviderCreate registers an instance and stores credential values on the gateway
// (OpenShell-like). Values are never returned by list/get.
func (a *App) ProviderCreate(args providerflags.CreateArgs) error {
	credentials := args.Credentials
	if credentials == nil {
		credentials = map[string]string{}
	}
	gwURL, _ := a.currentGatewayURL()
	_, profileWorkspace := a.profileScope()
	if args.GlobalProfile {
		profileWorkspace = ""
	}
	prof, err := a.loadProfileInWorkspace(args.Profile, gwURL, profileWorkspace)
	if err != nil {
		return err
	}
	if err := prof.ValidateRuntime(); err != nil {
		return err
	}
	envVars := args.EnvVars
	if args.FromExisting || len(envVars) == 0 {
		discovered, err := prof.DiscoverEnvVars()
		if err != nil {
			return err
		}
		discoveredConfig := prof.DiscoverConfig()
		if args.FromExisting && len(discovered) == 0 && len(discoveredConfig) == 0 {
			return fmt.Errorf("provider %q: no existing local credentials found for discovery.credentials", prof.ID)
		}
		if args.Config == nil {
			args.Config = map[string]string{}
		}
		for key, value := range discoveredConfig {
			if _, explicit := args.Config[key]; !explicit {
				args.Config[key] = value
			}
		}
		if args.FromExisting || len(envVars) == 0 {
			envVars = discovered
		}
	}
	for _, k := range envVars {
		if _, ok := credentials[k]; ok {
			continue
		}
		if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
			credentials[k] = v
		}
	}
	if args.FromOIDCToken {
		cfg, _, err := gwconfig.Load()
		if err != nil {
			return err
		}
		tok := ""
		if cfg.Current != "" {
			tok = strings.TrimSpace(cfg.Gateways[cfg.Current].Token)
		}
		if tok == "" {
			return fmt.Errorf("provider create --from-oidc-token: no gateway token (run: quarshell gateway login)")
		}
		key := "QUARSHELL_GATEWAY_TOKEN"
		if len(envVars) > 0 {
			key = envVars[0]
		}
		credentials[key] = tok
		if !containsString(envVars, key) {
			envVars = append(envVars, key)
		}
	}
	if args.FromGCloudADC {
		adc, err := readGCloudADC()
		if err != nil {
			return fmt.Errorf("provider create --from-gcloud-adc: %w", err)
		}
		key := "GOOGLE_APPLICATION_CREDENTIALS_JSON"
		credentials[key] = adc
		if !containsString(envVars, key) {
			envVars = append(envVars, key)
		}
	}
	if len(envVars) == 0 && len(credentials) > 0 {
		for k := range credentials {
			envVars = append(envVars, k)
		}
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	_, workspace := a.profileScope()
	rec := klorq.ProviderRecord{
		Name:                  args.Name,
		Type:                  args.Profile,
		Workspace:             workspace,
		EnvVars:               envVars,
		Credentials:           credentials,
		RuntimeCredentials:    args.RuntimeCredentials,
		CredentialExpiresAtMS: args.CredentialExpiresAt,
		Config:                args.Config,

		Refresh: profileRefreshConfig(prof, envVars)}
	if err := c.PutProvider(a.apiCtx(), rec); err != nil {
		return err
	}
	fmt.Printf("provider %s type=%s env=%v (values stored encrypted on gateway; never printed)\n", args.Name, args.Profile, envVars)
	return nil
}

// profileRefreshConfig carries supported OpenShell refresh metadata into the
// gateway provider record. Secret values are resolved later from encrypted storage.
func profileRefreshConfig(prof provider.Profile, selectedEnvKeys []string) map[string]klorq.ProviderRefreshConfig {
	selected := make(map[string]struct{}, len(selectedEnvKeys))
	for _, key := range selectedEnvKeys {
		selected[strings.TrimSpace(key)] = struct{}{}
	}
	credentialEnvKey := func(name string) string {
		for _, candidate := range prof.Credentials {
			if candidate.Name != name {
				continue
			}
			for _, key := range candidate.EnvVars {
				key = strings.TrimSpace(key)
				if _, ok := selected[key]; ok {
					return key
				}
			}
			if len(candidate.EnvVars) > 0 {
				return strings.TrimSpace(candidate.EnvVars[0])
			}
		}
		return ""
	}
	out := map[string]klorq.ProviderRefreshConfig{}
	for _, credential := range prof.Credentials {
		if credential.Refresh == nil {
			continue
		}
		strategy := strings.TrimSpace(credential.Refresh.Strategy)
		switch strategy {
		case "oauth2_refresh_token":
			strategy = "oauth2-refresh-token"
		case "oauth2_client_credentials":
			strategy = "oauth2-client-credentials"
		}
		if strategy != "oauth2-refresh-token" && strategy != "oauth2-client-credentials" {
			continue // executor support is required before activating another strategy
		}
		primaryKey := ""
		for _, candidate := range credential.EnvVars {
			candidate = strings.TrimSpace(candidate)
			if _, ok := selected[candidate]; ok {
				primaryKey = candidate
				break
			}
		}
		if primaryKey == "" {
			continue
		}
		cfg := klorq.ProviderRefreshConfig{
			CredentialKey:        primaryKey,
			Strategy:             strategy,
			Material:             map[string]string{},
			Outputs:              map[string]string{"access_token": primaryKey},
			RefreshBeforeSeconds: credential.Refresh.RefreshBeforeSeconds,
			MaxLifetimeSeconds:   credential.Refresh.MaxLifetimeSeconds,
		}
		if credential.Refresh.TokenURL != "" {
			cfg.Material["token_url"] = credential.Refresh.TokenURL
		}
		if len(credential.Refresh.Scopes) > 0 {
			cfg.Material["scope"] = strings.Join(credential.Refresh.Scopes, " ")
		}
		for _, material := range credential.Refresh.Material {
			if key := credentialEnvKey(material.Name); key != "" {
				if cfg.MaterialCredentialKeys == nil {
					cfg.MaterialCredentialKeys = map[string]string{}
				}
				cfg.MaterialCredentialKeys[material.Name] = key
			}
		}
		for _, output := range credential.Refresh.AdditionalOutputs {
			if key := credentialEnvKey(output.Credential); key != "" {
				cfg.Outputs[output.Output] = key
			}
		}
		out[primaryKey] = cfg
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsString(list []string, s string) bool {
	return slices.Contains(list, s)
}

// ProviderUpdate refreshes credential values for an existing instance.
func (a *App) ProviderUpdate(name string, fromExisting bool, credentials map[string]string) error {
	if credentials == nil {
		credentials = map[string]string{}
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	list, err := c.ListProviders(a.apiCtx())
	if err != nil {
		return err
	}
	var rec klorq.ProviderRecord
	found := false
	for _, p := range list {
		if p.Name == name {
			rec = p
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("provider %q not found", name)
	}
	if fromExisting {
		for _, k := range rec.EnvVars {
			if _, ok := credentials[k]; ok {
				continue
			}
			if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" {
				credentials[k] = v
			}
		}
	}
	if len(credentials) == 0 {
		return fmt.Errorf("provider update: no credentials (use --from-existing or --credential)")
	}
	rec.Credentials = credentials
	if err := c.PutProvider(a.apiCtx(), rec); err != nil {
		return err
	}
	fmt.Printf("provider %s updated (values stored encrypted on gateway)\n", name)
	return nil
}

// prepareProviders resolves --provider flags: discover host env, ensure gateway
// instances, compose profile endpoints into the create-time policy (OpenShell-like).
func (a *App) resolveProviderForCreateInWorkspace(name, gwURL, workspace string) (provider.Profile, []string, string, error) {
	// Prefer an existing gateway instance selected with --provider <instance>.
	if gwURL != "" {
		c := a.clientFor(gwURL)
		c.Workspace = workspace
		list, err := c.ListProviders(a.apiCtx())
		if err != nil {
			return provider.Profile{}, nil, "", fmt.Errorf("provider %q: gateway %s: %w (is quarshell-gateway running?)", name, gwURL, err)
		}
		for _, rec := range list {
			if rec.Name != name {
				continue
			}
			if rec.Workspace != "" && rec.Workspace != workspace {
				return provider.Profile{}, nil, "", fmt.Errorf("provider %q belongs to workspace %q", name, rec.Workspace)
			}
			prof, err := a.loadProfileInWorkspace(rec.Type, gwURL, workspace)
			if err != nil {
				return provider.Profile{}, nil, "", fmt.Errorf("provider %q: profile type %q: %w", name, rec.Type, err)
			}
			keys := rec.EnvVars
			if len(keys) == 0 {
				keys, err = prof.DiscoverEnvVars()
				if err != nil {
					return provider.Profile{}, nil, "", err
				}
			}
			return prof, keys, rec.Name, nil
		}
	}
	// Treat name as profile id: discover env and auto-create instance (e.g. --provider github).
	prof, err := a.loadProfileInWorkspace(name, gwURL, workspace)
	if err != nil {
		hint := ""
		if gwURL != "" {
			hint = fmt.Sprintf(" (no gateway instance %q; create with: quarshell provider create --name %s --type <profile> — or use --provider <profile-id>)", name, name)
		}
		return provider.Profile{}, nil, "", fmt.Errorf("provider %q: not a gateway instance or builtin profile%s: %w", name, hint, err)
	}
	keys, err := prof.DiscoverEnvVars()
	if err != nil {
		return provider.Profile{}, nil, "", err
	}
	if gwURL != "" {
		c := a.clientFor(gwURL)
		c.Workspace = workspace
		creds := map[string]string{}
		for _, k := range keys {
			if v, ok := os.LookupEnv(k); ok && strings.TrimSpace(v) != "" && !env.IsPlaceholder(v) {
				creds[k] = v
			}
		}
		if err := c.PutProvider(a.apiCtx(), klorq.ProviderRecord{
			Name: name, Type: prof.ID, Workspace: workspace, EnvVars: keys, Credentials: creds,
			Config: prof.DiscoverConfig(),
		}); err != nil {
			return provider.Profile{}, nil, "", fmt.Errorf("provider %q: register on gateway: %w", name, err)
		}
	}
	return prof, keys, name, nil
}

func loadBuiltinProfile(idOrPath string) (provider.Profile, error) {
	if strings.Contains(idOrPath, "/") || strings.HasSuffix(idOrPath, ".yaml") || strings.HasSuffix(idOrPath, ".yml") {
		return provider.LoadFile(idOrPath)
	}
	dir := provider.FindBuiltinDir()
	if dir == "" {
		return provider.Profile{}, fmt.Errorf("built-in provider profile catalog not found")
	}
	path := filepath.Join(dir, idOrPath+".yaml")
	return provider.LoadFile(path)
}

// loadProfile resolves imported profiles from the selected gateway first, then
// falls back to local builtin profiles for backward compatibility.
func (a *App) loadProfileInWorkspace(id, gatewayURL, workspace string) (provider.Profile, error) {
	if gatewayURL != "" && !strings.Contains(id, "/") && !strings.HasSuffix(id, ".yaml") && !strings.HasSuffix(id, ".yml") {
		scope := "global"
		if workspace != "" {
			scope = "workspace"
		}
		b, _, _, err := a.clientFor(gatewayURL).GetProfileScoped(a.apiCtx(), id, scope, workspace)
		if err == nil {
			return provider.ParseYAML(b)
		}
	}
	return loadBuiltinProfile(id)
}

func (a *App) profileScope(scopeOptions ...string) (scope, workspace string) {
	if len(scopeOptions) >= 2 {
		scope, workspace = scopeOptions[0], scopeOptions[1]
		if scope == "global" {
			workspace = ""
		}
		return scope, workspace
	}
	if a != nil && a.GlobalWorkspace != "" && a.GlobalWorkspace != "default" {
		return "workspace", a.GlobalWorkspace
	}
	return "global", ""
}

// ProviderList lists gateway provider instances.
func (a *App) ProviderList() error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	list, err := c.ListProviders(a.apiCtx())
	if err != nil {
		return err
	}
	for _, p := range list {
		fmt.Printf("%s\ttype=%s\tenv=%v\n", p.Name, p.Type, p.EnvVars)
	}
	return nil
}

type providerListOutput struct {
	Providers     []klorq.ProviderRecord `json:"providers" yaml:"providers"`
	NextPageToken string                 `json:"next_page_token" yaml:"next_page_token"`
}

// ProviderListPage prints one cursor page of Gateway provider instances.
func (a *App) ProviderListPage(pageSize int32, pageToken, selector, output string, allWorkspaces bool) error {
	if pageSize <= 0 {
		return fmt.Errorf("provider list: --page-size must be a positive integer")
	}
	if output != "table" && output != "yaml" && output != "json" {
		return fmt.Errorf("provider list: output must be table|yaml|json")
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	page, err := c.ListProvidersPage(a.apiCtx(), pageSize, pageToken, selector, allWorkspaces)
	if err != nil {
		return err
	}
	if err := outfmt.Emit(os.Stdout, output, func(w io.Writer) error {
		for _, provider := range page.Providers {
			if _, err := fmt.Fprintf(w, "%s\ttype=%s\tenv=%v\n", provider.Name, provider.Type, provider.EnvVars); err != nil {
				return err
			}
		}
		return nil
	}, providerListOutput{Providers: page.Providers, NextPageToken: page.NextPageToken}); err != nil {
		return err
	}
	if output == "table" && page.NextPageToken != "" {
		fmt.Fprintf(os.Stderr, "Next page token: %s\n", page.NextPageToken)
	}
	return nil
}

// ProviderAttach attaches a provider instance through the OpenShell Gateway.
func (a *App) ProviderAttach(sandbox, providerName string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	receipt, err := c.AttachProviderWithReceipt(a.apiCtx(), sandbox, providerName)
	if err != nil {
		return err
	}
	fmt.Printf("attached %s → sandbox %s\n", providerName, sandbox)
	if receipt != "" {
		fmt.Printf("change ID: %s\n", receipt)
	}
	return nil
}

// ProviderDetach detaches a provider instance through the OpenShell Gateway.
func (a *App) ProviderDetach(sandbox, providerName string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	receipt, err := c.DetachProviderWithReceipt(a.apiCtx(), sandbox, providerName)
	if err != nil {
		return err
	}
	fmt.Printf("detached %s from sandbox %s\n", providerName, sandbox)
	if receipt != "" {
		fmt.Printf("change ID: %s\n", receipt)
	}
	return nil
}

// ProviderEffective prints composed YAML for a sandbox.
func (a *App) ProviderEffective(sandbox string) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	b, err := c.EffectivePolicy(a.apiCtx(), sandbox)
	if err != nil {
		return err
	}
	fmt.Print(string(b))
	return nil
}

func readGCloudADC() (string, error) {
	candidates := []string{}
	if p := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); p != "" {
		candidates = append(candidates, p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "gcloud", "application_default_credentials.json"))
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err == nil && len(bytesTrim(b)) > 0 {
			return string(b), nil
		}
	}
	return "", fmt.Errorf("ADC file not found (set GOOGLE_APPLICATION_CREDENTIALS or run gcloud auth application-default login)")
}
