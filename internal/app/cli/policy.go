package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/KLORQ/quarshell-cli/internal/osargs"
	"github.com/KLORQ/quarshell-cli/internal/service"
	corepolicy "github.com/KLORQ/quarshell-core/policy"
	gatewayv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/gateway/v1"
	sandboxv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/sandbox/v1"
)

func runPolicy(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem policy get|set|update|list|history|delete|check …")
	}
	switch args[0] {
	case "check": // cautem-only validation helper
		if len(args) > 2 {
			return fmt.Errorf("usage: cautem policy check [path]")
		}
		path := "policies/default.yaml"
		if len(args) > 1 {
			path = args[1]
		}
		return a.PolicyCheck(path)
	case "get":
		parsed, err := osargs.ParsePolicyGet(args[1:])
		if err != nil {
			return err
		}
		if parsed.Global {
			return a.PolicyGlobalGet()
		}
		if parsed.Rev > 0 {
			return a.PolicyGetRevision(parsed.Name, parsed.Rev)
		}
		return a.PolicyGet(parsed.Name, parsed.View)
	case "set":
		parsed, err := osargs.ParsePolicySet(args[1:])
		if err != nil {
			return err
		}
		if parsed.Global {
			return a.PolicyGlobalSet(parsed.Path)
		}
		return a.PolicySet(parsed.Name, parsed.Path, parsed.Wait)
	case "update":
		return runPolicyUpdate(a, args[1:])
	case "history":
		return runPolicyHistory(a, args[1:])
	case "list", "ls":
		global := false
		name := ""
		pageSize, pageToken, output := int32(100), "", "table"
		seen := map[string]bool{}
		for i := 1; i < len(args); i++ {
			arg := args[i]
			flag, value, hasValue := strings.Cut(arg, "=")
			if flag == "-o" {
				flag = "--output"
			}
			if flag == "--page-size" || flag == "--page-token" || flag == "--output" {
				if seen[flag] {
					return fmt.Errorf("policy list: %s may be specified only once", flag)
				}
				seen[flag] = true
				if !hasValue {
					if i+1 >= len(args) || (flag != "--page-token" && strings.HasPrefix(args[i+1], "-")) {
						return fmt.Errorf("policy list: %s needs a value", flag)
					}
					i++
					value = args[i]
				}
				switch flag {
				case "--page-size":
					n, err := strconv.ParseInt(value, 10, 32)
					if err != nil || n <= 0 {
						return fmt.Errorf("policy list: --page-size must be a positive integer")
					}
					pageSize = int32(n)
				case "--page-token":
					pageToken = value
				case "--output":
					output = value
				}
				continue
			}
			switch arg {
			case "--global":
				if global {
					return fmt.Errorf("--global may be specified only once")
				}
				global = true
			default:
				if strings.HasPrefix(args[i], "-") {
					return fmt.Errorf("unknown flag %q", args[i])
				}
				if name != "" {
					return fmt.Errorf("usage: cautem policy list [sandbox|--global]")
				}
				name = args[i]
			}
		}
		if global {
			if name != "" {
				return fmt.Errorf("--global cannot be combined with a sandbox name")
			}
			if seen["--page-size"] || seen["--page-token"] || seen["--output"] {
				return fmt.Errorf("--global cannot be combined with sandbox history pagination or output flags")
			}
			fmt.Println("policy list --global: (use policy get --global for current YAML)")
			return a.PolicyGlobalGet()
		}
		if name == "" {
			return fmt.Errorf("usage: cautem policy list <sandbox> [--page-size N] [--page-token TOKEN] [-o table|yaml|json]")
		}
		return a.PolicyListPage(name, pageSize, pageToken, output)
	case "delete", "rm":
		global := false
		for _, f := range args[1:] {
			if f != "--global" {
				return fmt.Errorf("unknown argument %q", f)
			}
			if global {
				return fmt.Errorf("--global may be specified only once")
			}
			global = true
		}
		if !global {
			return fmt.Errorf("usage: cautem policy delete --global")
		}
		fmt.Println("policy delete --global: clearing gateway global policy")
		return a.PolicyGlobalClear()
	default:
		return fmt.Errorf("unknown policy subcommand %q", args[0])
	}
}

func runPolicyHistory(a *service.App, args []string) error {
	global := false
	sandbox := ""
	pageSize, pageToken, output := int32(100), "", "table"
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, hasValue := strings.Cut(arg, "=")
		if flag == "-o" {
			flag = "--output"
		}
		if flag == "--global" {
			if hasValue {
				return fmt.Errorf("policy history: --global does not accept a value")
			}
			if global {
				return fmt.Errorf("policy history: --global may be specified only once")
			}
			global = true
			continue
		}
		if flag == "--page-size" || flag == "--page-token" || flag == "--output" {
			if seen[flag] {
				return fmt.Errorf("policy history: %s may be specified only once", flag)
			}
			seen[flag] = true
			if !hasValue {
				if i+1 >= len(args) || (flag != "--page-token" && strings.HasPrefix(args[i+1], "-")) {
					return fmt.Errorf("policy history: %s needs a value", flag)
				}
				i++
				value = args[i]
			}
			switch flag {
			case "--page-size":
				n, err := strconv.ParseInt(value, 10, 32)
				if err != nil || n <= 0 {
					return fmt.Errorf("policy history: --page-size must be a positive integer")
				}
				pageSize = int32(n)
			case "--page-token":
				pageToken = value
			case "--output":
				output = value
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("policy history: unknown flag %q", arg)
		}
		if sandbox != "" {
			return fmt.Errorf("usage: cautem policy history [SANDBOX|--global] [--page-size N] [--page-token TOKEN] [-o table|yaml|json]")
		}
		sandbox = arg
	}
	if global && sandbox != "" {
		return fmt.Errorf("policy history: --global cannot be combined with a sandbox name")
	}
	if !global && sandbox == "" {
		return fmt.Errorf("usage: cautem policy history [SANDBOX|--global] [--page-size N] [--page-token TOKEN] [-o table|yaml|json]")
	}
	return a.PolicyHistoryPage(sandbox, global, pageSize, pageToken, output)
}

func runPolicyUpdate(a *service.App, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cautem policy update <sandbox> --add-endpoint SPEC [--add-allow SPEC] [--add-deny SPEC] [--binary PATH] [--wait]")
	}
	name := args[0]
	wait := false
	dryRun := false
	timeout := service.TimeoutPolicyWait
	var endpoints, allows, denies, binaries []string
	var removeEndpoints, removeRules []string
	ruleName := ""
	seenRuleName := false
	anyBinary := false
	var endpointPath *string
	for i := 1; i < len(args); i++ {
		flag, value, hasValue := strings.Cut(args[i], "=")
		readValue := func(want string) (string, error) {
			if hasValue {
				if strings.TrimSpace(value) == "" {
					return "", fmt.Errorf("%s needs a non-empty value", want)
				}
				return value, nil
			}
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return "", fmt.Errorf("%s needs a value", want)
			}
			i++
			return args[i], nil
		}
		switch flag {
		case "--add-endpoint":
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			endpoints = append(endpoints, v)
		case "--add-allow":
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			allows = append(allows, v)
		case "--add-deny":
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			denies = append(denies, v)
		case "--binary":
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			binaries = append(binaries, v)
		case "--any-binary":
			if hasValue {
				return fmt.Errorf("%s does not take a value", flag)
			}
			if anyBinary {
				return fmt.Errorf("--any-binary may be specified only once")
			}
			anyBinary = true
		case "--endpoint-path":
			if endpointPath != nil {
				return fmt.Errorf("--endpoint-path may be specified only once")
			}
			var v string
			if hasValue {
				v = value
			} else {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
					return fmt.Errorf("%s needs a value (use --endpoint-path= for an empty selector)", flag)
				}
				i++
				v = args[i]
			}
			endpointPath = &v
		case "--remove-endpoint":
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			removeEndpoints = append(removeEndpoints, v)
		case "--remove-rule":
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			removeRules = append(removeRules, v)
		case "--rule-name":
			if seenRuleName {
				return fmt.Errorf("--rule-name may be specified only once")
			}
			seenRuleName = true
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			ruleName = strings.TrimSpace(v)
		case "--wait":
			if hasValue {
				return fmt.Errorf("%s does not take a value", flag)
			}
			wait = true
		case "--dry-run":
			if hasValue {
				return fmt.Errorf("%s does not take a value", flag)
			}
			if dryRun {
				return fmt.Errorf("--dry-run may be specified only once")
			}
			dryRun = true
		case "--timeout":
			v, err := readValue(flag)
			if err != nil {
				return err
			}
			seconds, err := strconv.ParseUint(v, 10, 64)
			if err != nil || seconds > uint64((1<<63-1)/int64(time.Second)) {
				return fmt.Errorf("invalid --timeout %q (want non-negative seconds)", v)
			}
			timeout = time.Duration(seconds) * time.Second
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if seenRuleName && len(endpoints) > 0 && len(endpoints) != 1 {
		return fmt.Errorf("--rule-name can override exactly one --add-endpoint")
	}
	if seenRuleName && len(endpoints) == 0 && len(allows)+len(denies) == 0 {
		return fmt.Errorf("--rule-name requires --add-endpoint or an L7 addition")
	}
	if anyBinary && len(binaries) > 0 {
		return fmt.Errorf("--any-binary cannot be combined with --binary")
	}
	if anyBinary && len(allows)+len(denies) == 0 {
		return fmt.Errorf("--any-binary requires --add-allow or --add-deny")
	}
	if endpointPath != nil && len(allows)+len(denies) == 0 {
		return fmt.Errorf("--endpoint-path requires --add-allow or --add-deny")
	}
	for _, binary := range binaries {
		if strings.TrimSpace(binary) == "" {
			return fmt.Errorf("--binary values must not be empty")
		}
	}
	if len(binaries) > 0 && len(endpoints) == 0 && len(allows)+len(denies) == 0 {
		return fmt.Errorf("--binary requires --add-endpoint, --add-allow, or --add-deny")
	}
	useMergeAPI := len(removeEndpoints)+len(removeRules) > 0 || seenRuleName || len(allows)+len(denies) > 0
	if useMergeAPI {
		if len(allows)+len(denies) > 0 && (!seenRuleName || (!anyBinary && len(binaries) == 0)) {
			return fmt.Errorf("L7 additions require --rule-name and either --binary PATH or --any-binary")
		}
		operations := make([]*gatewayv1.PolicyMergeOperation, 0, len(endpoints)+len(removeEndpoints)+len(removeRules)+len(allows)+len(denies))
		for _, spec := range endpoints {
			parsed, err := corepolicy.ParseEndpointSpec(spec)
			if err != nil {
				return err
			}
			name := generatedPolicyRuleName(parsed.Host, parsed.Port)
			if ruleName != "" {
				name = ruleName
			}
			if strings.HasPrefix(strings.ToLower(name), "_provider_") {
				return fmt.Errorf("--rule-name uses reserved _provider_ prefix")
			}
			endpoint, err := policyEndpointProto(parsed)
			if err != nil {
				return err
			}
			protoRule := &sandboxv1.NetworkPolicyRule{Name: name, Endpoints: []*sandboxv1.NetworkEndpoint{endpoint}}
			seenBinaries := make(map[string]bool, len(binaries))
			for _, binary := range binaries {
				path := strings.TrimSpace(binary)
				if seenBinaries[path] {
					continue
				}
				seenBinaries[path] = true
				protoRule.Binaries = append(protoRule.Binaries, &sandboxv1.NetworkBinary{Path: path})
			}
			operations = append(operations, &gatewayv1.PolicyMergeOperation{Operation: &gatewayv1.PolicyMergeOperation_AddRule{AddRule: &gatewayv1.AddNetworkRule{RuleName: name, Rule: protoRule}}})
		}
		for _, spec := range allows {
			fragment, ports, err := parseL7MergeSpec(spec)
			if err != nil {
				return err
			}
			for _, port := range ports {
				target := l7MergeTarget(ruleName, fragment.host, port, endpointPath, binaries, anyBinary)
				rule := &sandboxv1.L7Rule{Allow: &sandboxv1.L7Allow{Method: fragment.method, Path: fragment.path}}
				operations = append(operations, &gatewayv1.PolicyMergeOperation{Operation: &gatewayv1.PolicyMergeOperation_AddAllowRules{AddAllowRules: &gatewayv1.AddAllowRules{Target: target, Rules: []*sandboxv1.L7Rule{rule}}}})
			}
		}
		for _, spec := range denies {
			fragment, ports, err := parseL7MergeSpec(spec)
			if err != nil {
				return err
			}
			for _, port := range ports {
				target := l7MergeTarget(ruleName, fragment.host, port, endpointPath, binaries, anyBinary)
				rule := &sandboxv1.L7DenyRule{Method: fragment.method, Path: fragment.path}
				operations = append(operations, &gatewayv1.PolicyMergeOperation{Operation: &gatewayv1.PolicyMergeOperation_AddDenyRules{AddDenyRules: &gatewayv1.AddDenyRules{Target: target, DenyRules: []*sandboxv1.L7DenyRule{rule}}}})
			}
		}
		for _, spec := range removeEndpoints {
			spec = strings.TrimSpace(spec)
			var host, portText string
			var found bool
			if strings.HasPrefix(spec, "[") {
				end := strings.Index(spec, "]:")
				if end >= 0 {
					host, portText, found = spec[1:end], spec[end+2:], true
				}
			} else {
				host, portText, found = strings.Cut(spec, ":")
			}
			if !found || host == "" || strings.Contains(portText, ":") {
				return fmt.Errorf("--remove-endpoint expects host:port, got %q", spec)
			}
			port, err := strconv.Atoi(portText)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("--remove-endpoint has invalid port in %q", spec)
			}
			operations = append(operations, &gatewayv1.PolicyMergeOperation{Operation: &gatewayv1.PolicyMergeOperation_RemoveEndpoint{RemoveEndpoint: &gatewayv1.RemoveNetworkEndpoint{Host: host, Port: uint32(port)}}})
		}
		for _, ruleName := range removeRules {
			if strings.TrimSpace(ruleName) == "" {
				return fmt.Errorf("--remove-rule values must not be empty")
			}
			trimmed := strings.TrimSpace(ruleName)
			if strings.HasPrefix(strings.ToLower(trimmed), "_provider_") {
				return fmt.Errorf("--remove-rule uses reserved _provider_ prefix")
			}
			operations = append(operations, &gatewayv1.PolicyMergeOperation{Operation: &gatewayv1.PolicyMergeOperation_RemoveRule{RemoveRule: &gatewayv1.RemoveNetworkRule{RuleName: trimmed}}})
		}
		if len(operations) == 0 {
			return fmt.Errorf("policy update requires at least one operation flag")
		}
		return a.PolicyApplyMerge(name, operations, dryRun, timeout)
	}
	if len(endpoints)+len(allows)+len(denies) == 0 {
		return fmt.Errorf("policy update requires at least one operation flag")
	}
	return a.PolicyUpdate(name, endpoints, allows, denies, binaries, wait, dryRun, timeout)
}

type l7MergeSpec struct {
	host   string
	method string
	path   string
}

func parseL7MergeSpec(spec string) (l7MergeSpec, []int, error) {
	spec = strings.TrimSpace(spec)
	var host, rest string
	if strings.HasPrefix(spec, "[") {
		end := strings.Index(spec, "]:")
		if end < 0 {
			return l7MergeSpec{}, nil, fmt.Errorf("L7 spec expects host:port[,port...]:METHOD:path, got %q", spec)
		}
		host, rest = spec[1:end], spec[end+2:]
	} else {
		var found bool
		host, rest, found = strings.Cut(spec, ":")
		if !found {
			return l7MergeSpec{}, nil, fmt.Errorf("L7 spec expects host:port[,port...]:METHOD:path, got %q", spec)
		}
	}
	portText, rest, found := strings.Cut(rest, ":")
	if !found || strings.TrimSpace(host) == "" {
		return l7MergeSpec{}, nil, fmt.Errorf("L7 spec expects host:port[,port...]:METHOD:path, got %q", spec)
	}
	method, path, found := strings.Cut(rest, ":")
	method, path = strings.TrimSpace(method), strings.TrimSpace(path)
	if !found || method == "" || path == "" {
		return l7MergeSpec{}, nil, fmt.Errorf("L7 spec expects host:port[,port...]:METHOD:path, got %q", spec)
	}
	ports := strings.Split(portText, ",")
	out := make([]int, 0, len(ports))
	seen := make(map[int]bool, len(ports))
	for _, item := range ports {
		port, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || port < 1 || port > 65535 {
			return l7MergeSpec{}, nil, fmt.Errorf("L7 spec has invalid port %q", item)
		}
		if !seen[port] {
			seen[port] = true
			out = append(out, port)
		}
	}
	return l7MergeSpec{host: strings.Trim(host, "[]"), method: method, path: path}, out, nil
}

func l7MergeTarget(ruleName, host string, port int, endpointPath *string, binaries []string, anyBinary bool) *gatewayv1.L7RuleTarget {
	target := &gatewayv1.L7RuleTarget{RuleName: strings.TrimSpace(ruleName), Host: host, Ports: []uint32{uint32(port)}, AnyBinary: anyBinary}
	if endpointPath != nil {
		value := *endpointPath
		target.Path = &value
	}
	seen := make(map[string]bool, len(binaries))
	for _, binary := range binaries {
		path := strings.TrimSpace(binary)
		if path != "" && !seen[path] {
			seen[path] = true
			target.Binaries = append(target.Binaries, &sandboxv1.NetworkBinary{Path: path})
		}
	}
	return target
}

func generatedPolicyRuleName(host string, port int) string {
	var clean strings.Builder
	for _, r := range host {
		if r == '.' || r == '-' {
			clean.WriteByte('_')
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			clean.WriteRune(r)
		}
	}
	return fmt.Sprintf("allow_%s_%d", clean.String(), port)
}

func policyEndpointProto(parsed corepolicy.EndpointSpec) (*sandboxv1.NetworkEndpoint, error) {
	endpoint := &sandboxv1.NetworkEndpoint{Host: parsed.Host, Port: uint32(parsed.Port), Protocol: parsed.Protocol}
	switch parsed.Access {
	case "":
	case "read-only":
		endpoint.Access = sandboxv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_ONLY
	case "read-write":
		endpoint.Access = sandboxv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_READ_WRITE
	case "full":
		endpoint.Access = sandboxv1.NetworkAccessPreset_NETWORK_ACCESS_PRESET_FULL
	default:
		return nil, fmt.Errorf("--add-endpoint access must be read-only|read-write|full, got %q", parsed.Access)
	}
	switch parsed.Enforcement {
	case "":
	case "enforce":
		endpoint.Enforcement = sandboxv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_ENFORCE
	case "audit":
		endpoint.Enforcement = sandboxv1.NetworkEnforcementMode_NETWORK_ENFORCEMENT_MODE_AUDIT
	default:
		return nil, fmt.Errorf("--add-endpoint enforcement must be enforce|audit, got %q", parsed.Enforcement)
	}
	if parsed.Protocol != "" && parsed.Protocol != "tcp" && parsed.Protocol != "rest" && parsed.Protocol != "websocket" && parsed.Protocol != "sql" {
		return nil, fmt.Errorf("--add-endpoint protocol must be tcp|rest|websocket|sql, got %q", parsed.Protocol)
	}
	if parsed.Protocol == "tcp" && (parsed.Access != "" || parsed.Enforcement != "") {
		return nil, fmt.Errorf("--add-endpoint tcp protocol does not accept access or enforcement")
	}
	if parsed.Enforcement != "" && parsed.Protocol == "" {
		return nil, fmt.Errorf("--add-endpoint cannot set enforcement without a protocol")
	}
	return endpoint, nil
}
