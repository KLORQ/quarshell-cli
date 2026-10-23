package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/outfmt"
	"github.com/KLORQ/quarshell-core/engine"
	"github.com/KLORQ/quarshell-core/policy"
	gatewayv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/gateway/v1"
	sandboxv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/sandbox/v1"
	"github.com/KLORQ/quarshell-sdk/go/klorq"
	"github.com/KLORQ/quarshell-slogx"
	"gopkg.in/yaml.v3"
)

// PolicyCheck loads and validates a policy YAML file.
func (a *App) PolicyCheck(path string) error {
	const op = "cli.policy.check"
	log := a.op(op, slog.String("path", path))
	log.Info("checking policy")
	doc, err := policy.Load(path)
	if err != nil {
		log.Error("failed to load policy", slogx.Err(err))
		return fmt.Errorf("policy check: %w", err)
	}
	doc, err = a.mergeGatewayGlobal(doc)
	if err != nil {
		log.Error("failed to merge global policy", slogx.Err(err))
		return fmt.Errorf("policy check: %w", err)
	}
	if err := doc.Validate(); err != nil {
		log.Error("policy validation failed", slogx.Err(err))
		return fmt.Errorf("policy check: %w", err)
	}
	var eng engine.Allowlist
	if err := eng.Apply(doc); err != nil {
		log.Error("policy engine apply failed", slogx.Err(err))
		return fmt.Errorf("policy check: engine: %w", err)
	}
	log.Info("policy check ok", slog.Int("allow_rules", len(doc.AllowRules())), slog.String("harden", doc.HardenMode()))
	fmt.Printf("policy check: ok version=%d harden=%s allow_rules=%d include_workdir=%v",
		doc.Version, doc.HardenMode(), len(doc.AllowRules()),
		doc.IncludeWorkdir())
	if doc.Inference != nil && len(doc.Inference.Providers) > 0 {
		fmt.Printf(" inference=%v", doc.Inference.Providers)
	}
	if len(doc.Binaries) > 0 {
		fmt.Printf(" binaries=%d", len(doc.Binaries))
	}
	fmt.Println()
	return nil
}

// PolicyApplyMerge applies gateway-native incremental policy operations.
func (a *App) PolicyApplyMerge(sandbox string, operations []*gatewayv1.PolicyMergeOperation, dryRun bool, timeout time.Duration) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(timeout)
	defer cancel()
	if dryRun {
		base, err := c.GetSandboxPolicy(ctx, sandbox, "base")
		if err != nil {
			return err
		}
		doc, err := policy.Parse(base)
		if err != nil {
			return fmt.Errorf("policy update dry-run: parse base: %w", err)
		}
		if err := applyPolicyMergePreview(&doc, operations); err != nil {
			return err
		}
		if err := doc.Validate(); err != nil {
			return fmt.Errorf("policy update dry-run: %w", err)
		}
		out, err := yaml.Marshal(doc)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	}
	response, err := c.UpdateSandboxPolicyMerge(ctx, sandbox, operations)
	if err != nil {
		return err
	}
	fmt.Printf("policy update: applied revision=%d hash=%s\n", response.GetVersion(), response.GetPolicyHash())
	return nil
}

func applyPolicyMergePreview(doc *policy.Document, operations []*gatewayv1.PolicyMergeOperation) error {
	for i, operation := range operations {
		if add := operation.GetAddRule(); add != nil {
			if err := applyPolicyAddRulePreview(doc, add); err != nil {
				return fmt.Errorf("policy update dry-run: operation %d: %w", i, err)
			}
			continue
		}
		if add := operation.GetAddAllowRules(); add != nil {
			if err := applyPolicyL7Preview(doc, add.GetTarget(), add.GetRules(), nil); err != nil {
				return fmt.Errorf("policy update dry-run: operation %d: %w", i, err)
			}
			continue
		}
		if add := operation.GetAddDenyRules(); add != nil {
			if err := applyPolicyL7Preview(doc, add.GetTarget(), nil, add.GetDenyRules()); err != nil {
				return fmt.Errorf("policy update dry-run: operation %d: %w", i, err)
			}
			continue
		}
		if remove := operation.GetRemoveRule(); remove != nil {
			name := strings.TrimSpace(remove.GetRuleName())
			if strings.HasPrefix(strings.ToLower(name), "_provider_") {
				return fmt.Errorf("policy update dry-run: rule name uses reserved _provider_ prefix")
			}
			delete(doc.NetworkPolicies, name)
			continue
		}
		remove := operation.GetRemoveEndpoint()
		if remove == nil {
			return fmt.Errorf("policy update dry-run: operation %d is not a removal", i)
		}
		ruleName, host, port := strings.TrimSpace(remove.GetRuleName()), strings.TrimSpace(remove.GetHost()), int(remove.GetPort())
		if strings.HasPrefix(strings.ToLower(ruleName), "_provider_") {
			return fmt.Errorf("policy update dry-run: rule name uses reserved _provider_ prefix")
		}
		for name, rule := range doc.NetworkPolicies {
			if strings.HasPrefix(strings.ToLower(name), "_provider_") {
				continue
			}
			if ruleName != "" && name != ruleName {
				continue
			}
			endpoints := rule.Endpoints[:0]
			for _, endpoint := range rule.Endpoints {
				if !strings.EqualFold(endpoint.Host, host) {
					endpoints = append(endpoints, endpoint)
					continue
				}
				if len(endpoint.Ports) != 0 {
					ports := endpoint.Ports[:0]
					for _, candidate := range endpoint.Ports {
						if candidate != port {
							ports = append(ports, candidate)
						}
					}
					endpoint.Ports = ports
					if len(ports) != 0 {
						endpoint.Port = ports[0]
						endpoints = append(endpoints, endpoint)
					}
					continue
				}
				if endpoint.Port != port {
					endpoints = append(endpoints, endpoint)
				}
			}
			rule.Endpoints = endpoints
			doc.NetworkPolicies[name] = rule
		}
	}
	return nil
}

func applyPolicyL7Preview(doc *policy.Document, target *gatewayv1.L7RuleTarget, allows []*sandboxv1.L7Rule, denies []*sandboxv1.L7DenyRule) error {
	if target == nil || strings.TrimSpace(target.GetRuleName()) == "" || len(target.GetPorts()) != 1 {
		return fmt.Errorf("L7 target requires rule_name and exactly one port")
	}
	paths := make([]string, 0, len(target.GetBinaries()))
	for _, binary := range target.GetBinaries() {
		if binary == nil || strings.TrimSpace(binary.GetPath()) == "" {
			return fmt.Errorf("L7 target binary paths must not be empty")
		}
		paths = append(paths, strings.TrimSpace(binary.GetPath()))
	}
	if target.GetAnyBinary() == (len(paths) > 0) {
		return fmt.Errorf("L7 target requires either binaries or any_binary")
	}
	for name, rule := range doc.NetworkPolicies {
		if name != target.GetRuleName() {
			continue
		}
		rulePaths := make([]string, 0, len(rule.Binaries))
		for _, binary := range rule.Binaries {
			rulePaths = append(rulePaths, strings.TrimSpace(binary.Path))
		}
		sort.Strings(paths)
		sort.Strings(rulePaths)
		if target.GetAnyBinary() && len(rulePaths) != 0 || !target.GetAnyBinary() && !reflect.DeepEqual(paths, rulePaths) {
			return fmt.Errorf("L7 target binary scope does not match rule %q", name)
		}
		matches := 0
		for idx := range rule.Endpoints {
			endpoint := &rule.Endpoints[idx]
			if !strings.EqualFold(endpoint.Host, target.GetHost()) || target.Path != nil && endpoint.Path != target.GetPath() {
				continue
			}
			portMatch := endpoint.Port == int(target.GetPorts()[0])
			for _, port := range endpoint.Ports {
				portMatch = portMatch || port == int(target.GetPorts()[0])
			}
			if portMatch {
				matches++
				selected := endpoint
				if len(allows) > 0 && selected.Access != "" {
					methods := []string{"*"}
					switch strings.ToLower(selected.Access) {
					case "read-only":
						if strings.EqualFold(selected.Protocol, "websocket") {
							methods = []string{"GET"}
						} else {
							methods = []string{"GET", "HEAD", "OPTIONS"}
						}
					case "read-write":
						if strings.EqualFold(selected.Protocol, "websocket") {
							methods = []string{"GET", "WEBSOCKET_TEXT"}
						} else {
							methods = []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH"}
						}
					case "full":
					default:
						return fmt.Errorf("unsupported access preset %q", selected.Access)
					}
					for _, method := range methods {
						selected.Rules = append(selected.Rules, policy.L7Rule{Allow: &policy.L7Allow{Method: method, Path: "**"}})
					}
					selected.Access = ""
				}
				for _, item := range allows {
					if item == nil || item.GetAllow() == nil {
						return fmt.Errorf("allow rules must include allow payloads")
					}
					candidate := policy.L7Rule{Allow: &policy.L7Allow{Method: item.GetAllow().GetMethod(), Path: item.GetAllow().GetPath()}}
					if !containsPolicyL7Rule(selected.Rules, candidate) {
						selected.Rules = append(selected.Rules, candidate)
					}
				}
				if len(denies) > 0 && selected.Access == "" && len(selected.Rules) == 0 {
					return fmt.Errorf("endpoint %s:%d has no allow base", target.GetHost(), target.GetPorts()[0])
				}
				for _, item := range denies {
					if item == nil {
						return fmt.Errorf("deny rules must not be nil")
					}
					candidate := policy.L7DenyRule{Method: item.GetMethod(), Path: item.GetPath()}
					if !containsPolicyL7Deny(selected.DenyRules, candidate) {
						selected.DenyRules = append(selected.DenyRules, candidate)
					}
				}
			}
		}
		if matches == 0 {
			return fmt.Errorf("L7 target endpoint %s:%d was not found", target.GetHost(), target.GetPorts()[0])
		}
		if matches > 1 {
			return fmt.Errorf("L7 target endpoint %s:%d is ambiguous; specify --endpoint-path", target.GetHost(), target.GetPorts()[0])
		}
		doc.NetworkPolicies[name] = rule
		return nil
	}
	return fmt.Errorf("L7 target rule %q was not found", target.GetRuleName())
}

func containsPolicyL7Rule(items []policy.L7Rule, candidate policy.L7Rule) bool {
	for _, item := range items {
		if reflect.DeepEqual(item, candidate) {
			return true
		}
	}
	return false
}

func containsPolicyL7Deny(items []policy.L7DenyRule, candidate policy.L7DenyRule) bool {
	for _, item := range items {
		if reflect.DeepEqual(item, candidate) {
			return true
		}
	}
	return false
}

func applyPolicyAddRulePreview(doc *policy.Document, add *gatewayv1.AddNetworkRule) error {
	if add == nil || add.GetRule() == nil || strings.TrimSpace(add.GetRuleName()) == "" {
		return fmt.Errorf("add_rule requires a rule name and rule")
	}
	name := strings.TrimSpace(add.GetRuleName())
	incoming := policy.NetworkPolicy{Name: name}
	for _, binary := range add.GetRule().GetBinaries() {
		if binary != nil {
			incoming.Binaries = append(incoming.Binaries, policy.NetworkBinary{Path: binary.GetPath()})
		}
	}
	for _, endpoint := range add.GetRule().GetEndpoints() {
		if endpoint == nil {
			continue
		}
		incoming.Endpoints = append(incoming.Endpoints, policy.AllowRule{
			Host: endpoint.GetHost(), Port: int(endpoint.GetPort()), Protocol: endpoint.GetProtocol(),
			Access: accessPresetName(endpoint.GetAccess()), Enforcement: enforcementModeName(endpoint.GetEnforcement()),
		})
	}
	if len(incoming.Endpoints) == 0 {
		return fmt.Errorf("add_rule must include an endpoint")
	}
	if doc.NetworkPolicies == nil {
		doc.NetworkPolicies = make(map[string]policy.NetworkPolicy)
	}
	existing, ok := doc.NetworkPolicies[name]
	if !ok {
		doc.NetworkPolicies[name] = incoming
		return nil
	}
	existingBinaries, incomingBinaries := networkBinaryPaths(existing.Binaries), networkBinaryPaths(incoming.Binaries)
	sort.Strings(existingBinaries)
	sort.Strings(incomingBinaries)
	if !reflect.DeepEqual(existingBinaries, incomingBinaries) {
		return fmt.Errorf("rule %q has a different binary scope", name)
	}
	for _, endpoint := range incoming.Endpoints {
		if !containsPolicyEndpoint(existing.Endpoints, endpoint) {
			existing.Endpoints = append(existing.Endpoints, endpoint)
		}
	}
	doc.NetworkPolicies[name] = existing
	return nil
}

func accessPresetName(value sandboxv1.NetworkAccessPreset) string {
	switch value {
	case sandboxv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY:
		return "read-only"
	case sandboxv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_WRITE:
		return "read-write"
	case sandboxv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_FULL:
		return "full"
	default:
		return ""
	}
}

func enforcementModeName(value sandboxv1.NetworkEnforcementMode) string {
	switch value {
	case sandboxv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_ENFORCE:
		return "enforce"
	case sandboxv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_AUDIT:
		return "audit"
	default:
		return ""
	}
}

func networkBinaryPaths(values []policy.NetworkBinary) []string {
	paths := make([]string, len(values))
	for i, value := range values {
		paths[i] = value.Path
	}
	return paths
}

func containsPolicyEndpoint(values []policy.AllowRule, wanted policy.AllowRule) bool {
	for _, value := range values {
		if reflect.DeepEqual(value, wanted) {
			return true
		}
	}
	return false
}

// PolicyGlobalGet prints gateway global policy YAML.

// PolicyGlobalGet prints gateway global policy YAML.
func (a *App) PolicyGlobalGet() error {
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	b, err := a.clientWithToken(u, a.gatewayTokenForURL(u)).GetGlobalPolicy(a.apiCtx())
	if err != nil {
		return err
	}
	os.Stdout.Write(b)
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

// PolicyGlobalSet uploads YAML file as gateway global policy.

// PolicyGlobalSet uploads YAML file as gateway global policy.
func (a *App) PolicyGlobalSet(path string) error {
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc, err := policy.Parse(b)
	if err != nil {
		return err
	}
	if err := doc.Validate(); err != nil {
		return err
	}
	if err := a.clientWithToken(u, a.gatewayTokenForURL(u)).PutGlobalPolicy(a.apiCtx(), b); err != nil {
		return err
	}
	fmt.Printf("policy global set: ok url=%s bytes=%d\n", u, len(b))
	return nil
}

// PolicyGlobalClear removes the gateway-global policy lock.

// PolicyGlobalClear removes the gateway-global policy lock.
func (a *App) PolicyGlobalClear() error {
	u, err := a.currentGatewayURL()
	if err != nil {
		return err
	}
	if err := a.clientWithToken(u, a.gatewayTokenForURL(u)).PutGlobalPolicy(a.apiCtx(), nil); err != nil {
		return err
	}
	fmt.Println("policy delete --global: ok")
	return nil
}

// PolicySet stores sandbox base policy; the gateway builds the effective
// candidate via composition (base + provider-composed profiles) — OpenShell-style.
//
// PolicySet submits a native OpenShell policy update and optionally waits for
// the Gateway to report that revision loaded.
func (a *App) PolicySet(sandboxName, path string, wait bool) error {
	return a.policySet(sandboxName, path, wait, TimeoutPolicyWait)
}

func (a *App) policySet(sandboxName, path string, wait bool, waitTimeout time.Duration) error {
	const op = "cli.policy.set"
	log := a.op(op, slog.String("sandbox", sandboxName), slog.String("path", path), slog.Bool("wait", wait))
	log.Info("setting sandbox policy")
	b, err := os.ReadFile(path)
	if err != nil {
		log.Error("failed to read policy file", slogx.Err(err))
		return err
	}
	doc, err := policy.Parse(b)
	if err != nil {
		log.Error("failed to parse policy", slogx.Err(err))
		return fmt.Errorf("policy set: %w", err)
	}
	if err := doc.Validate(); err != nil {
		log.Error("policy validation failed", slogx.Err(err))
		return fmt.Errorf("policy set: %w", err)
	}

	gw, err := a.currentGatewayURL()
	if err != nil {
		return fmt.Errorf("policy set: select an OpenShell Gateway: %w", err)
	}
	client := a.clientWithToken(gw, a.gatewayTokenForURL(gw))
	ctx, cancel := a.withTimeout(TimeoutAPILong)
	defer cancel()
	effective, version, err := client.PutSandboxPolicy(ctx, sandboxName, b)
	if err != nil {
		return fmt.Errorf("policy set: %w", err)
	}
	effectiveDoc, err := policy.Parse(effective)
	if err != nil {
		return fmt.Errorf("policy set: effective policy: %w", err)
	}
	var allowlist engine.Allowlist
	if err := allowlist.Apply(effectiveDoc); err != nil {
		return fmt.Errorf("policy set: effective policy: %w", err)
	}
	fmt.Printf("policy set: submitted sandbox=%s version=%d allow_rules=%d\n",
		sandboxName, version, len(effectiveDoc.AllowRules()))
	if wait {
		return a.waitPolicy(ctx, client, sandboxName, version, waitTimeout)
	}
	return nil
}

func (a *App) waitPolicy(parent context.Context, client *klorq.Client, sandbox string, version uint32, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = TimeoutPolicyWait
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		revision, err := client.GetPolicyRevisionStatus(ctx, sandbox, version)
		if err != nil {
			return fmt.Errorf("policy set: read revision %d status: %w", version, err)
		}
		switch strings.TrimPrefix(revision.Status, "POLICY_STATUS_") {
		case "LOADED":
			fmt.Printf("policy set: loaded version=%d\n", version)
			return nil
		case "FAILED":
			return fmt.Errorf("policy set: version %d failed to load: %s", version, revision.LoadError)
		case "SUPERSEDED":
			fmt.Printf("policy set: version %d superseded before load\n", version)
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return &ExitError{Code: 124}
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// PolicyUpdate applies incremental network changes to the sandbox base policy.

// PolicyUpdate applies incremental network changes to the sandbox base policy.
func (a *App) PolicyUpdate(sandbox string, endpoints, allows, denies, binaries []string, wait, dryRun bool, waitTimeout ...time.Duration) error {
	timeout := TimeoutPolicyWait
	if len(waitTimeout) > 0 {
		timeout = waitTimeout[0]
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx, cancel := a.withTimeout(TimeoutAPILong)
	defer cancel()
	baseYAML, err := c.GetSandboxPolicy(ctx, sandbox, "base")
	if err != nil {
		return err
	}
	doc, err := policy.Parse(baseYAML)
	if err != nil {
		return fmt.Errorf("policy update: parse base: %w", err)
	}
	u := policy.NetworkUpdate{Binaries: binaries}
	for _, s := range endpoints {
		ep, err := policy.ParseEndpointSpec(s)
		if err != nil {
			return err
		}
		u.AddEndpoints = append(u.AddEndpoints, ep)
	}
	for _, s := range allows {
		mp, err := policy.ParseMethodPathSpec(s)
		if err != nil {
			return err
		}
		u.AddAllows = append(u.AddAllows, mp)
	}
	for _, s := range denies {
		mp, err := policy.ParseMethodPathSpec(s)
		if err != nil {
			return err
		}
		u.AddDenies = append(u.AddDenies, mp)
	}
	out, err := policy.ApplyNetworkUpdate(doc, u)
	if err != nil {
		return err
	}
	raw, err := yamlMarshalDoc(out)
	if err != nil {
		return err
	}
	if dryRun {
		_, err := os.Stdout.Write(raw)
		if err == nil && (len(raw) == 0 || raw[len(raw)-1] != '\n') {
			fmt.Println()
		}
		return err
	}
	tmp, err := os.CreateTemp("", "cautem-policy-update-*.yaml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return a.policySet(sandbox, tmpPath, wait, timeout)
}

func yamlMarshalDoc(doc policy.Document) ([]byte, error) {
	return yaml.Marshal(doc)
}

// PolicyList prints policy revision metadata.

// PolicyList prints policy revision metadata.
func (a *App) PolicyList(sandbox string) error {
	return a.PolicyListPage(sandbox, 100, "", "table")
}

type policyListPageOutput struct {
	Revisions     []klorq.PolicyRevisionRecord `json:"revisions" yaml:"revisions"`
	NextPageToken string                       `json:"next_page_token" yaml:"next_page_token"`
}

func (a *App) PolicyListPage(sandbox string, pageSize int32, pageToken, output string) error {
	return a.PolicyHistoryPage(sandbox, false, pageSize, pageToken, output)
}

func (a *App) PolicyHistoryPage(sandbox string, global bool, pageSize int32, pageToken, output string) error {
	if pageSize <= 0 {
		return fmt.Errorf("policy history: --page-size must be a positive integer")
	}
	if output != "table" && output != "yaml" && output != "json" {
		return fmt.Errorf("policy history: output must be table|yaml|json")
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	var page klorq.PolicyRevisionPage
	if global {
		page, err = c.ListGlobalPolicyRevisionsPage(a.apiCtx(), pageSize, pageToken)
	} else {
		page, err = c.ListPolicyRevisionsPage(a.apiCtx(), sandbox, pageSize, pageToken)
	}
	if err != nil {
		return err
	}
	data := policyListPageOutput{Revisions: page.Revisions, NextPageToken: page.NextPageToken}
	if err := outfmt.Emit(os.Stdout, output, func(w io.Writer) error {
		if len(data.Revisions) == 0 {
			_, err := fmt.Fprintln(w, "policy revisions: (none)")
			return err
		}
		if _, err := fmt.Fprintln(w, "REVISION\tSTATUS\tPOLICY HASH\tCREATED AT"); err != nil {
			return err
		}
		for _, revision := range data.Revisions {
			createdAt := ""
			if revision.CreatedAt != nil {
				createdAt = revision.CreatedAt.Format(time.RFC3339)
			}
			if _, err := fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", revision.Revision, revision.Status, revision.PolicyHash, createdAt); err != nil {
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

// PolicyGetRevision prints base YAML for a historical revision.

// PolicyGetRevision prints base YAML for a historical revision.
func (a *App) PolicyGetRevision(sandbox string, rev int) error {
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	yamlBody, err := c.GetPolicyRevision(a.apiCtx(), sandbox, rev)
	if err != nil {
		return err
	}
	fmt.Print(yamlBody)
	if len(yamlBody) > 0 && yamlBody[len(yamlBody)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

// SandboxProviderList prints attached provider metadata (no secret values).

// SandboxProviderList prints attached provider metadata (no secret values).
func (a *App) SandboxProviderList(sandbox string) error {
	return a.SandboxProviderListPage(sandbox, 100, "", "table")
}

func (a *App) SandboxProviderListPage(sandbox string, pageSize int32, pageToken, output string) error {
	if sandbox == "" {
		var err error
		sandbox, err = a.resolveSandboxTarget("")
		if err != nil {
			return err
		}
	}
	if output != "table" && output != "yaml" && output != "json" {
		return fmt.Errorf("sandbox provider list: output must be table|yaml|json")
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	list, next, err := c.ListSandboxProvidersPage(a.apiCtx(), sandbox, pageSize, pageToken)
	if err != nil {
		return err
	}
	if err := outfmt.Emit(os.Stdout, output, func(w io.Writer) error {
		if len(list) == 0 {
			_, err := fmt.Fprintln(w, "sandbox providers: (none)")
			return err
		}
		if _, err := fmt.Fprintf(w, "%-24s  %-20s  %s\n", "NAME", "TYPE", "ENVIRONMENT"); err != nil {
			return err
		}
		for _, provider := range list {
			if _, err := fmt.Fprintf(w, "%-24s  %-20s  %v\n", provider.Name, provider.Type, provider.EnvVars); err != nil {
				return err
			}
		}
		return nil
	}, list); err != nil {
		return err
	}
	if output == "table" && next != "" {
		fmt.Fprintf(os.Stderr, "Next page token: %s\n", next)
	}
	return nil
}

// SandboxProviderStatus prints the redacted readiness status for one provider
// attachment. wait polls until the mutation reaches a terminal state.
func (a *App) SandboxProviderStatus(sandbox, provider, receiptID string, wait bool, timeout time.Duration, output string) error {
	if provider == "" {
		return fmt.Errorf("sandbox provider status: provider is required")
	}
	if output != "table" && output != "yaml" && output != "json" {
		return fmt.Errorf("sandbox provider status: output must be table|yaml|json")
	}
	if timeout <= 0 {
		return fmt.Errorf("sandbox provider status: timeout must be positive")
	}
	if sandbox == "" {
		var err error
		sandbox, err = a.resolveSandboxTarget("")
		if err != nil {
			return err
		}
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	ctx := a.apiCtx()
	status, err := c.GetSandboxProviderStatus(ctx, sandbox, provider, receiptID)
	if err != nil {
		return err
	}
	if wait {
		deadline := time.Now().Add(timeout)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for !providerReadinessTerminal(status.State) {
			if time.Now().After(deadline) {
				return fmt.Errorf("sandbox provider status: timed out waiting for %s", provider)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
			status, err = c.GetSandboxProviderStatus(ctx, sandbox, provider, receiptID)
			if err != nil {
				return err
			}
		}
	}
	return outfmt.Emit(os.Stdout, output, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "%-12s  %-40s  %-36s  %s\n", "STATE", "REASON", "RECEIPT", "OBSERVED"); err != nil {
			return err
		}
		observed := "-"
		if !status.ObservedTime.IsZero() {
			observed = status.ObservedTime.UTC().Format(time.RFC3339)
		}
		_, err := fmt.Fprintf(w, "%-12s  %-40s  %-36s  %s\n", providerReadinessShortState(status.State), status.Reason, status.ReceiptID, observed)
		return err
	}, status)
}

func providerReadinessTerminal(state string) bool {
	switch strings.TrimPrefix(state, "PROVIDER_READINESS_STATE_") {
	case "READY", "REVOKED", "WITHHELD", "FAILED", "SUPERSEDED":
		return true
	default:
		return false
	}
}

func providerReadinessShortState(state string) string {
	return strings.TrimPrefix(state, "PROVIDER_READINESS_STATE_")
}

// PolicyGet prints sandbox policy YAML. view is "base" or "full" (default full).
func (a *App) PolicyGet(sandboxName, view string) error {
	if view == "" {
		view = "full"
	}
	c, err := a.gatewayClient()
	if err != nil {
		return err
	}
	b, err := c.GetSandboxPolicy(a.apiCtx(), sandboxName, view)
	if err != nil {
		return err
	}
	fmt.Print(string(b))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func (a *App) mergeGatewayGlobal(doc policy.Document) (policy.Document, error) {
	u, err := a.currentGatewayURL()
	if err != nil || u == "" {
		return doc, nil
	}
	b, err := a.clientWithToken(u, a.gatewayTokenForURL(u)).GetGlobalPolicy(a.apiCtx())
	if err != nil || len(bytesTrim(b)) == 0 {
		return doc, nil
	}
	global, err := policy.Parse(b)
	if err != nil {
		return doc, fmt.Errorf("gateway global policy: %w", err)
	}
	return policy.MergeGlobal(doc, global)
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// InferenceList prints builtin provider presets.

func (a *App) loadOrDenyAll(path string) (policy.Document, string, error) {
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return policy.Document{}, "", err
		}
		doc, err := policy.Load(abs)
		if err != nil {
			return policy.Document{}, "", err
		}
		doc, err = a.mergeGatewayGlobal(doc)
		if err != nil {
			return policy.Document{}, "", err
		}
		if err := doc.Validate(); err != nil {
			return policy.Document{}, "", err
		}
		return doc, abs, nil
	}
	dir, err := os.MkdirTemp("", "cautem-policy-*")
	if err != nil {
		return policy.Document{}, "", err
	}
	abs := filepath.Join(dir, "deny-all.yaml")
	const body = "version: 1\nnetwork_policies: {}\n"
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		return policy.Document{}, "", err
	}
	doc, err := policy.Parse([]byte(body))
	if err != nil {
		return policy.Document{}, "", err
	}
	return doc, abs, nil
}

// InitOpts for `cautem init --agent …`.
