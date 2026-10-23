package cli

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/KLORQ/quarshell-cli/internal/osargs"
	"github.com/KLORQ/quarshell-cli/internal/service"
	"github.com/KLORQ/quarshell-cli/internal/storage/templates"
	"golang.org/x/term"
)

func runSandbox(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: quarshell sandbox create|list|get|stop|start|delete|exec|connect|upload|download|ssh-config|provider")
	}
	switch args[0] {
	case "create":
		opt, err := parseSandboxCreate(args[1:])
		if err != nil {
			return err
		}
		return a.SandboxCreate(opt)
	case "list", "ls":
		opt := service.SandboxListOpts{PageSize: 100, Output: "table"}
		seen := map[string]bool{}
		for i := 1; i < len(args); i++ {
			arg := args[i]
			flag := strings.SplitN(arg, "=", 2)[0]
			seenFlag := flag
			switch flag {
			case "--limit":
				seenFlag = "--page-size"
			case "-o":
				seenFlag = "--output"
			}
			if seen[seenFlag] {
				return fmt.Errorf("sandbox list: duplicate flag %q", seenFlag)
			}
			seen[seenFlag] = true
			value := func() (string, error) {
				if eq := strings.IndexByte(arg, '='); eq >= 0 {
					return arg[eq+1:], nil
				}
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return "", fmt.Errorf("%s needs a value", arg)
				}
				i++
				return args[i], nil
			}
			switch flag {
			case "--page-size", "--limit", "--page-token", "--selector", "-o", "--output":
				v, err := value()
				if err != nil {
					return err
				}
				switch flag {
				case "--page-size", "--limit":
					n, err := strconv.ParseInt(v, 10, 32)
					if err != nil || n <= 0 {
						return fmt.Errorf("sandbox list: --page-size must be a positive integer")
					}
					opt.PageSize = int32(n)
				case "--page-token":
					opt.PageToken = v
				case "--selector":
					opt.Selector = v
				case "-o", "--output":
					opt.Output = v
				}
			case "--ids":
				opt.IDs = true
			case "--names":
				opt.Names = true
			case "--all-workspaces":
				opt.AllWorkspaces = true
			default:
				return fmt.Errorf("sandbox list: unknown argument %q", arg)
			}
		}
		if opt.IDs && opt.Names {
			return fmt.Errorf("sandbox list: --ids conflicts with --names")
		}
		if (opt.IDs || opt.Names) && seen["--output"] {
			return fmt.Errorf("sandbox list: --ids/--names conflict with --output")
		}
		return a.SandboxList(opt)
	case "get", "status":
		name := ""
		policyOnly := false
		for _, arg := range args[1:] {
			switch arg {
			case "--policy-only":
				if policyOnly {
					return fmt.Errorf("sandbox get: --policy-only may be specified only once")
				}
				policyOnly = true
			default:
				if strings.HasPrefix(arg, "-") {
					return fmt.Errorf("sandbox get: unknown flag %q", arg)
				}
				if name != "" {
					return fmt.Errorf("usage: quarshell sandbox get NAME [--policy-only]")
				}
				name = arg
			}
		}
		if name == "" {
			return fmt.Errorf("usage: quarshell sandbox get NAME [--policy-only]")
		}
		if policyOnly {
			return a.PolicyGet(name, "full")
		}
		return a.SandboxStatus(name)
	case "stop":
		name, err := osargs.ParseSandboxTarget(args[1:], "stop")
		if err != nil {
			return err
		}
		return a.SandboxStop(name)
	case "start":
		name, err := osargs.ParseSandboxTarget(args[1:], "start")
		if err != nil {
			return err
		}
		return a.SandboxStart(name)
	case "rm", "remove", "delete":
		parsed, err := osargs.ParseSandboxDelete(args[1:])
		if err != nil {
			return err
		}
		return a.SandboxDelete(parsed.Names, parsed.All)
	case "exec":
		return runExec(a, args[1:])
	case "upload", "download":
		parsed, err := osargs.ParseSandboxTransfer(args[1:])
		if err != nil {
			return err
		}
		if args[0] == "download" && parsed.NoGitIgnore {
			return fmt.Errorf("--no-git-ignore is only valid for sandbox upload")
		}
		dest := parsed.Dest
		if args[0] == "upload" {
			if dest == "" {
				dest = "/workspace"
			}
			return a.SandboxUpload(parsed.Path, parsed.Name+":"+dest, parsed.NoGitIgnore)
		}
		if dest == "" {
			dest = "."
		}
		return a.Copy(parsed.Name+":"+parsed.Path, dest)
	case "ssh-config":
		parsed, err := osargs.ParseSandboxSSHConfig(args[1:])
		if err != nil {
			return err
		}
		return a.SandboxSSHConfig(parsed.Name, parsed.Install)
	case "connect":
		parsed, err := osargs.ParseSandboxConnect(args[1:])
		if err != nil {
			return err
		}
		return a.SandboxConnect(service.ConnectOpts{Name: parsed.Name, Editor: parsed.Editor, Argv: parsed.Argv})
	case "template":
		return runSandboxTemplate(a, args[1:])
	case "provider":
		if len(args) < 2 {
			return fmt.Errorf("usage: quarshell sandbox provider list|attach|detach|status …")
		}
		switch args[1] {
		case "list", "ls":
			sandbox, pageSize, pageToken, output, err := parseSandboxProviderListArgs(args[2:])
			if err != nil {
				return err
			}
			return a.SandboxProviderListPage(sandbox, pageSize, pageToken, output)
		case "attach":
			if len(args) != 4 {
				return fmt.Errorf("usage: quarshell sandbox provider attach <sandbox> <provider>")
			}
			return a.ProviderAttach(args[2], args[3])
		case "detach":
			if len(args) != 4 {
				return fmt.Errorf("usage: quarshell sandbox provider detach <sandbox> <provider>")
			}
			return a.ProviderDetach(args[2], args[3])
		case "status":
			sandbox, provider, receipt, output, wait, timeout, err := parseSandboxProviderStatusArgs(args[2:])
			if err != nil {
				return err
			}
			return a.SandboxProviderStatus(sandbox, provider, receipt, wait, timeout, output)
		default:
			return fmt.Errorf("unknown sandbox provider subcommand %q", args[1])
		}
	default:
		return fmt.Errorf("unknown sandbox subcommand %q", args[0])
	}
}

func parseSandboxProviderStatusArgs(args []string) (sandbox, provider, receipt, output string, wait bool, timeout time.Duration, err error) {
	output, timeout = "table", 30*time.Second
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, hasValue := strings.Cut(arg, "=")
		if flag == "-o" {
			flag = "--output"
		}
		switch flag {
		case "--receipt", "--output", "--timeout":
			if !hasValue {
				i++
				if i >= len(args) {
					return "", "", "", "", false, 0, fmt.Errorf("sandbox provider status: %s needs a value", flag)
				}
				value = args[i]
			}
			switch flag {
			case "--receipt":
				receipt = value
			case "--output":
				output = value
			case "--timeout":
				seconds, parseErr := strconv.Atoi(value)
				if parseErr != nil || seconds <= 0 {
					return "", "", "", "", false, 0, fmt.Errorf("sandbox provider status: --timeout must be a positive number of seconds")
				}
				timeout = time.Duration(seconds) * time.Second
			}
		case "--wait":
			if hasValue {
				parsed, parseErr := strconv.ParseBool(value)
				if parseErr != nil {
					return "", "", "", "", false, 0, fmt.Errorf("sandbox provider status: --wait must be a boolean")
				}
				wait = parsed
			} else {
				wait = true
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return "", "", "", "", false, 0, fmt.Errorf("sandbox provider status: unknown flag %q", arg)
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		return "", "", "", "", false, 0, fmt.Errorf("usage: quarshell sandbox provider status <sandbox> <provider> [--receipt ID] [--wait] [--timeout SECONDS] [-o table|yaml|json]")
	}
	if output != "table" && output != "yaml" && output != "json" {
		return "", "", "", "", false, 0, fmt.Errorf("sandbox provider status: --output must be table|yaml|json")
	}
	return positional[0], positional[1], receipt, output, wait, timeout, nil
}

func parseSandboxProviderListArgs(args []string) (sandbox string, pageSize int32, pageToken, output string, err error) {
	pageSize, output = 100, "table"
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, hasValue := strings.Cut(arg, "=")
		if flag == "-o" {
			flag = "--output"
		}
		if flag != "--page-size" && flag != "--page-token" && flag != "--output" {
			if strings.HasPrefix(arg, "-") {
				return "", 0, "", "", fmt.Errorf("sandbox provider list: unknown flag %q", arg)
			}
			if sandbox != "" {
				return "", 0, "", "", fmt.Errorf("sandbox provider list: unexpected argument %q", arg)
			}
			sandbox = arg
			continue
		}
		if seen[flag] {
			return "", 0, "", "", fmt.Errorf("sandbox provider list: %s may be specified only once", flag)
		}
		seen[flag] = true
		if !hasValue {
			if i+1 >= len(args) || (flag != "--page-token" && strings.HasPrefix(args[i+1], "-")) {
				return "", 0, "", "", fmt.Errorf("sandbox provider list: %s needs a value", flag)
			}
			i++
			value = args[i]
		}
		switch flag {
		case "--page-size":
			parsed, parseErr := strconv.ParseInt(value, 10, 32)
			if parseErr != nil || parsed <= 0 {
				return "", 0, "", "", fmt.Errorf("sandbox provider list: --page-size must be a positive integer")
			}
			pageSize = int32(parsed)
		case "--page-token":
			pageToken = value
		case "--output":
			if value != "table" && value != "yaml" && value != "json" {
				return "", 0, "", "", fmt.Errorf("sandbox provider list: --output must be table|yaml|json")
			}
			output = value
		}
	}
	return sandbox, pageSize, pageToken, output, nil
}

func parseSandboxCreate(rest []string) (service.SandboxCreateOpts, error) {
	opt := service.SandboxCreateOpts{Labels: map[string]string{}}
	exposeModeSet := false
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--" {
			opt.Argv = append([]string{}, rest[i+1:]...)
			break
		}
		switch rest[i] {
		case "--name":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--name needs a value")
			}
			opt.Name = rest[i]
		case "--gateway-workspace":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--gateway-workspace needs a name")
			}
			opt.GatewayWorkspace = rest[i]
		case "--image":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--image needs a value")
			}
			opt.Image = rest[i]
		case "--policy":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--policy needs a value")
			}
			opt.Policy = rest[i]
		case "--label":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--label needs key=value")
			}
			k, v, ok := strings.Cut(rest[i], "=")
			if !ok || k == "" {
				return service.SandboxCreateOpts{}, fmt.Errorf("--label needs key=value")
			}
			opt.Labels[k] = v
		case "--gateway":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--gateway needs a URL")
			}
			opt.GatewayURL = rest[i]
		case "--from":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--from needs an alias")
			}
			opt.From = rest[i]
		case "--ssh":
			return service.SandboxCreateOpts{}, fmt.Errorf("sandbox create: --ssh was removed; SSH relay is configured automatically when a gateway is selected")
		case "--gpu":
			opt.GPU = true
			count, next, err := parseOptionalGPUCount(rest, i)
			if err != nil {
				return service.SandboxCreateOpts{}, err
			}
			i, opt.GPUCount = next, count
		case "--provider":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--provider needs a profile id or instance name (e.g. github)")
			}
			opt.Providers = append(opt.Providers, rest[i])
		case "--auto-providers":
			opt.AutoProviders = true
		case "--no-auto-providers":
			opt.NoAutoProviders = true
		case "--no-credential-warnings":
			opt.NoCredentialWarnings = true
		case "--env":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--env needs KEY=VALUE")
			}
			k, v, ok := strings.Cut(rest[i], "=")
			if !ok || k == "" {
				return service.SandboxCreateOpts{}, fmt.Errorf("--env needs KEY=VALUE")
			}
			if opt.Env == nil {
				opt.Env = map[string]string{}
			}
			opt.Env[k] = v
		case "--detach":
			opt.Detach = true
		case "--editor":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--editor needs vscode|cursor")
			}
			opt.Editor = rest[i]
		case "--forward":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--forward needs PORT")
			}
			p, ok := parsePortNumber(rest[i])
			if !ok {
				return service.SandboxCreateOpts{}, fmt.Errorf("invalid --forward port")
			}
			opt.Forwards = append(opt.Forwards, p)
		case "--upload":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--upload needs PATH")
			}
			opt.Upload = rest[i]
		case "--no-git-ignore":
			opt.NoGitIgnore = true
		case "--cpu":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--cpu needs a float")
			}
			cpu, err := service.ParseCPUQuantity(rest[i])
			if err != nil {
				return service.SandboxCreateOpts{}, err
			}
			opt.CPU = cpu
		case "--memory":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--memory needs a size (512m, 4g, …)")
			}
			opt.Memory = rest[i]
		case "--template":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--template needs NAME")
			}
			opt.Template = rest[i]
		case "--restart-policy":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--restart-policy needs never|on-failure|always")
			}
			opt.RestartPolicy = strings.ToLower(strings.TrimSpace(rest[i]))
			if opt.RestartPolicy != "never" && opt.RestartPolicy != "on-failure" && opt.RestartPolicy != "always" {
				return service.SandboxCreateOpts{}, fmt.Errorf("invalid --restart-policy %q; want never|on-failure|always", rest[i])
			}
		case "--driver-config-json":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--driver-config-json needs JSON")
			}
			opt.DriverConfigJSON = rest[i]
		case "--expose":
			if opt.ExposePort != 0 {
				return service.SandboxCreateOpts{}, fmt.Errorf("--expose may be specified only once")
			}
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--expose needs a port")
			}
			port, ok := parsePortNumber(rest[i])
			if !ok {
				return service.SandboxCreateOpts{}, fmt.Errorf("--expose port must be between 1 and 65535")
			}
			opt.ExposePort = port
		case "--expose-authorization-mode":
			if exposeModeSet {
				return service.SandboxCreateOpts{}, fmt.Errorf("--expose-authorization-mode may be specified only once")
			}
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--expose-authorization-mode needs a value")
			}
			opt.ExposeAuthorizationMode = strings.TrimSpace(rest[i])
			if opt.ExposeAuthorizationMode != "strip" && opt.ExposeAuthorizationMode != "bearer-passthrough" {
				return service.SandboxCreateOpts{}, fmt.Errorf("unsupported --expose-authorization-mode %q; choose strip or bearer-passthrough", opt.ExposeAuthorizationMode)
			}
			exposeModeSet = true
		case "--approval-mode":
			i++
			if sandboxCreateValueMissing(rest, i) {
				return service.SandboxCreateOpts{}, fmt.Errorf("--approval-mode needs manual|auto")
			}
			opt.ApprovalMode = rest[i]
		case "--no-keep":
			opt.NoKeep = true
		case "--tty":
			opt.ForceTTY = true
		default:
			return service.SandboxCreateOpts{}, fmt.Errorf("unknown flag %q", rest[i])
		}
	}
	if opt.NoGitIgnore && opt.Upload == "" {
		return service.SandboxCreateOpts{}, fmt.Errorf("--no-git-ignore requires --upload")
	}
	if opt.ExposeAuthorizationMode != "" && opt.ExposePort == 0 {
		return service.SandboxCreateOpts{}, fmt.Errorf("--expose-authorization-mode requires --expose")
	}
	return opt, nil
}

func sandboxCreateValueMissing(args []string, index int) bool {
	return index >= len(args) || args[index] == "--" || strings.HasPrefix(args[index], "--")
}

func parsePortNumber(value string) (int, bool) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, false
	}
	return port, true
}

func runExec(a *service.App, args []string) error {
	parsed, err := osargs.ParseSandboxExec(args)
	if err != nil {
		return err
	}
	env := make([]string, 0, len(parsed.Env))
	for k, v := range parsed.Env {
		env = append(env, k+"="+v)
	}
	tty := parsed.TTY
	if !parsed.TTYSet {
		tty = term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	}
	return a.Exec(service.ExecOpts{
		Name:         parsed.Name,
		Argv:         parsed.Argv,
		TTY:          tty,
		Env:          env,
		WorkDir:      parsed.WorkDir,
		Timeout:      time.Duration(parsed.TimeoutSec) * time.Second,
		NoLoginShell: parsed.NoLoginShell,
		Stdin:        os.Stdin,
	})
}

func runSandboxTemplate(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: quarshell sandbox template create|list|get|delete …")
	}
	switch args[0] {
	case "create":
		t := templates.Template{}
		gateway := false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--gateway":
				gateway = true
			case "--name":
				i++
				if i >= len(args) {
					return fmt.Errorf("--name required")
				}
				t.Name = args[i]
			case "--image":
				i++
				if i >= len(args) {
					return fmt.Errorf("--image needs value")
				}
				t.Image = args[i]
			case "--from":
				i++
				if i >= len(args) {
					return fmt.Errorf("--from needs value")
				}
				t.From = args[i]
			case "--policy":
				i++
				if i >= len(args) {
					return fmt.Errorf("--policy needs path")
				}
				t.Policy = args[i]
			case "--cpu":
				i++
				if i >= len(args) {
					return fmt.Errorf("--cpu needs value")
				}
				cpu, err := service.ParseCPUQuantity(args[i])
				if err != nil {
					return err
				}
				t.CPU = cpu
			case "--memory":
				i++
				if i >= len(args) {
					return fmt.Errorf("--memory needs value")
				}
				t.Memory = args[i]
			case "--gpu":
				count, next, err := parseOptionalGPUCount(args, i)
				if err != nil {
					return err
				}
				i, t.GPUCount = next, count
			case "--env":
				i++
				if i >= len(args) {
					return fmt.Errorf("--env needs KEY=VALUE")
				}
				key, value, ok := strings.Cut(args[i], "=")
				if !ok || key == "" {
					return fmt.Errorf("--env needs KEY=VALUE")
				}
				if t.Env == nil {
					t.Env = map[string]string{}
				}
				t.Env[key] = value
			case "--label":
				i++
				if i >= len(args) {
					return fmt.Errorf("--label needs KEY=VALUE")
				}
				key, value, ok := strings.Cut(args[i], "=")
				if !ok || key == "" {
					return fmt.Errorf("--label needs KEY=VALUE")
				}
				if t.Labels == nil {
					t.Labels = map[string]string{}
				}
				t.Labels[key] = value
			case "--driver-config-json":
				i++
				if i >= len(args) {
					return fmt.Errorf("--driver-config-json needs JSON")
				}
				t.DriverConfigJSON = args[i]
			case "--provider":
				i++
				if i >= len(args) {
					return fmt.Errorf("--provider needs value")
				}
				t.Providers = append(t.Providers, args[i])
			case "--annotation":
				i++
				if i >= len(args) {
					return fmt.Errorf("--annotation needs KEY=VALUE")
				}
				key, value, ok := strings.Cut(args[i], "=")
				key = strings.TrimSpace(key)
				if !ok || key == "" {
					return fmt.Errorf("--annotation needs KEY=VALUE")
				}
				if t.Annotations == nil {
					t.Annotations = map[string]string{}
				}
				if _, exists := t.Annotations[key]; exists {
					return fmt.Errorf("duplicate annotation key %q", key)
				}
				t.Annotations[key] = value
			case "--ready-within":
				if t.ReadyWithin != "" {
					return fmt.Errorf("--ready-within may be specified only once")
				}
				i++
				if i >= len(args) || strings.TrimSpace(args[i]) == "" {
					return fmt.Errorf("--ready-within needs a duration (for example 30s, 5m, 1h)")
				}
				duration, err := time.ParseDuration(args[i])
				if err != nil || duration <= 0 {
					return fmt.Errorf("--ready-within must be a positive Go duration")
				}
				t.ReadyWithin = duration.String()
			case "--max-burst":
				if t.MaxBurst != 0 {
					return fmt.Errorf("--max-burst may be specified only once")
				}
				i++
				if i >= len(args) {
					return fmt.Errorf("--max-burst needs a positive count")
				}
				count, err := strconv.ParseUint(args[i], 10, 32)
				if err != nil || count == 0 {
					return fmt.Errorf("--max-burst must be a positive integer")
				}
				t.MaxBurst = uint32(count)
			default:
				if strings.HasPrefix(args[i], "-") || t.Name != "" {
					return fmt.Errorf("unknown flag %q", args[i])
				}
				t.Name = args[i]
			}
		}
		if t.Name == "" {
			return fmt.Errorf("template create: --name required")
		}
		if !gateway && (len(t.Annotations) > 0 || t.ReadyWithin != "" || t.MaxBurst > 0) {
			return fmt.Errorf("template create: --annotation, --ready-within, and --max-burst require --gateway")
		}
		if gateway {
			return a.GatewayTemplateCreate(t)
		}
		return a.TemplateCreate(t)
	case "list", "ls":
		gateway := false
		pageSize, pageToken, selector, output := int32(100), "", "", "table"
		seen := map[string]bool{}
		for i := 1; i < len(args); i++ {
			arg := args[i]
			flag, value, hasValue := strings.Cut(arg, "=")
			if flag == "-o" {
				flag = "--output"
			}
			if flag == "--gateway" {
				gateway = true
				continue
			}
			if flag != "--page-size" && flag != "--page-token" && flag != "--label-selector" && flag != "--output" {
				return fmt.Errorf("unknown template list flag %q", arg)
			}
			if seen[flag] {
				return fmt.Errorf("template list: %s may be specified only once", flag)
			}
			seen[flag] = true
			if !hasValue {
				if i+1 >= len(args) || (flag != "--page-token" && strings.HasPrefix(args[i+1], "-")) {
					return fmt.Errorf("template list: %s needs a value", flag)
				}
				i++
				value = args[i]
			}
			switch flag {
			case "--page-size":
				n, err := strconv.ParseInt(value, 10, 32)
				if err != nil || n <= 0 {
					return fmt.Errorf("template list: --page-size must be a positive integer")
				}
				pageSize = int32(n)
			case "--page-token":
				pageToken = value
			case "--label-selector":
				selector = value
			case "--output":
				output = value
			}
		}
		if gateway {
			return a.GatewayTemplateListPage(pageSize, pageToken, selector, output)
		}
		if seen["--page-size"] || seen["--page-token"] || seen["--label-selector"] || seen["--output"] {
			return fmt.Errorf("template list: pagination, selector and output flags require --gateway")
		}
		return a.TemplateList()
	case "get":
		name, gateway, err := parseTemplateTarget(args[1:])
		if err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("usage: quarshell sandbox template get <name>")
		}
		if gateway {
			return a.GatewayTemplateGet(name)
		}
		return a.TemplateGet(name)
	case "delete", "rm":
		name, gateway, err := parseTemplateTarget(args[1:])
		if err != nil {
			return err
		}
		if name == "" {
			return fmt.Errorf("usage: quarshell sandbox template delete <name>")
		}
		if gateway {
			return a.GatewayTemplateDelete(name)
		}
		return a.TemplateDelete(name)
	default:
		return fmt.Errorf("unknown template subcommand %q", args[0])
	}
}

func parseTemplateTarget(args []string) (name string, gateway bool, err error) {
	for _, arg := range args {
		if arg == "--gateway" {
			gateway = true
			continue
		}
		if strings.HasPrefix(arg, "-") || name != "" {
			return "", false, fmt.Errorf("invalid template target argument %q", arg)
		}
		name = arg
	}
	return name, gateway, nil
}

func parseOptionalGPUCount(args []string, flagIndex int) (count, lastConsumedIndex int, err error) {
	count, lastConsumedIndex = 1, flagIndex
	if flagIndex+1 >= len(args) || args[flagIndex+1] == "--" {
		return count, lastConsumedIndex, nil
	}
	next := args[flagIndex+1]
	parsed, parseErr := strconv.Atoi(next)
	if parseErr != nil {
		if strings.HasPrefix(next, "-") {
			return count, lastConsumedIndex, nil
		}
		return 0, flagIndex, fmt.Errorf("invalid --gpu count %q; expected a positive integer", next)
	}
	if parsed < 1 || uint64(parsed) > math.MaxUint32 {
		return 0, flagIndex, fmt.Errorf("invalid --gpu count %q; expected a positive integer", next)
	}
	return parsed, flagIndex + 1, nil
}

// runSSHProxy is the SSH ProxyCommand hook (token / name / legacy modes).
func runSSHProxy(a *service.App, args []string) error {
	parsed, err := osargs.ParseSSHProxy(args)
	if err != nil {
		return err
	}
	return a.SSHProxy(service.SSHProxyOpts{
		GatewayURL:  parsed.GatewayURL,
		GatewayName: parsed.GatewayName,
		SandboxID:   parsed.SandboxID,
		Token:       parsed.Token,
		Name:        parsed.Name,
	})
}

// runProxy is the sidecar / host CONNECT proxy entrypoint.
// Usage: quarshell proxy --listen ADDR --policy PATH [--ca-out PATH]

// runProxy is the sidecar / host CONNECT proxy entrypoint.
// Usage: quarshell proxy --listen ADDR --policy PATH [--ca-out PATH]
func runProxy(a *service.App, args []string) error {
	if os.Getenv("QUARSHELL_HOST_PARENT_LIVENESS") == "1" {
		watchParentLiveness(os.Stdin, func() {
			if process, err := os.FindProcess(os.Getpid()); err == nil {
				_ = process.Kill()
			}
		})
	}
	opt := service.ProxyOpts{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--listen", "-l":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --listen needs a value")
			}
			i++
			opt.Listen = args[i]
		case "--policy", "-p":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --policy needs a value")
			}
			i++
			opt.Policy = args[i]
		case "--ca-out":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --ca-out needs a value")
			}
			i++
			opt.CAOut = args[i]
		case "--gateway":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --gateway needs a value")
			}
			i++
			opt.GatewayURL = args[i]
		case "--gateway-host-mode":
			opt.GatewayHostMode = true
		case "--sandbox":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --sandbox needs a value")
			}
			i++
			opt.Sandbox = args[i]
		case "--log-dir":
			if i+1 >= len(args) {
				return fmt.Errorf("proxy: --log-dir needs a value")
			}
			i++
			opt.LogDir = args[i]
		case "--help", "-h":
			fmt.Fprintln(os.Stderr, "usage: quarshell proxy --listen ADDR --policy PATH [--ca-out PATH] [--gateway URL] [--gateway-host-mode] [--sandbox NAME]")
			return nil
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("proxy: unknown flag %q", args[i])
			}
			return fmt.Errorf("proxy: unexpected arg %q", args[i])
		}
	}
	if strings.TrimSpace(opt.Policy) == "" {
		return fmt.Errorf("usage: quarshell proxy --listen ADDR --policy PATH [--ca-out PATH]")
	}
	return a.Proxy(opt)
}

func watchParentLiveness(reader io.Reader, terminate func()) {
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		terminate()
	}()
}
