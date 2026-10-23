package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/KLORQ/quarshell-cli/internal/osargs"
	"github.com/KLORQ/quarshell-cli/internal/service"
)

func runForward(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem forward start|stop|list …")
	}
	switch args[0] {
	case "start":
		parsed, err := osargs.ParseForwardStart(args[1:])
		if err != nil {
			return err
		}
		if parsed.Name == "" {
			return fmt.Errorf("usage: cautem forward start [bind:]port <sandbox> [-d]")
		}
		return a.ForwardStart(parsed.Name, parsed.Port, parsed.Port, parsed.Background)
	case "stop":
		parsed, err := osargs.ParseForwardStop(args[1:])
		if err != nil {
			return err
		}
		id := parsed.Port
		if parsed.Name != "" {
			id = parsed.Name + ":" + parsed.Port
		}
		return a.ForwardStop(id)
	case "list", "ls":
		if len(args) != 1 {
			return fmt.Errorf("usage: cautem forward list")
		}
		return a.ForwardList()
	case "service":
		return runForwardService(a, args[1:])
	default:
		return fmt.Errorf("unknown forward subcommand %q", args[0])
	}
}

func runForwardService(a *service.App, args []string) error {
	targetPort, local, targetHost, sandbox := 0, "", "127.0.0.1", ""
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			flag, value, hasValue := strings.Cut(arg, "=")
			if seen[flag] {
				return fmt.Errorf("%s may be specified only once", flag)
			}
			seen[flag] = true
			if !hasValue {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return fmt.Errorf("%s requires a value", flag)
				}
				i++
				value = args[i]
			}
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s requires a non-empty value", flag)
			}
			switch flag {
			case "--target-port":
				port, err := strconv.Atoi(value)
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("invalid target port %q", value)
				}
				targetPort = port
			case "--target-host":
				targetHost = value
			case "--local":
				local = value
			default:
				return fmt.Errorf("unknown flag %q", arg)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") || sandbox != "" {
			return fmt.Errorf("usage: cautem forward service [--target-port PORT] [--target-host HOST] [--local [bind:]PORT] [NAME]")
		}
		sandbox = arg
	}
	if targetPort == 0 {
		return fmt.Errorf("usage: cautem forward service --target-port PORT [--target-host HOST] [--local [bind:]PORT] [NAME]")
	}
	if local == "" {
		local = strconv.Itoa(targetPort)
	}
	return a.ForwardService(sandbox, local, targetHost, targetPort)
}

func runService(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem service expose|list|get|delete …")
	}
	switch args[0] {
	case "expose":
		parsed, err := osargs.ParseServiceExpose(args[1:])
		if err != nil {
			return err
		}
		return a.ServiceExpose(parsed.Sandbox, parsed.Service, parsed.Port, parsed.AuthorizationMode)
	case "list", "ls":
		options, err := parseServiceListArgs(args[1:])
		if err != nil {
			return err
		}
		return a.ServiceListPage(options)
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem service get <sandbox> [service]")
		}
		if len(args) > 3 {
			return fmt.Errorf("unexpected argument %q", args[3])
		}
		sandbox, name := args[1], "default"
		if len(args) > 2 {
			name = args[2]
		}
		return a.ServiceGet(sandbox, name)
	case "delete", "rm":
		if len(args) < 2 {
			return fmt.Errorf("usage: cautem service delete <sandbox> [service]")
		}
		if len(args) > 3 {
			return fmt.Errorf("unexpected argument %q", args[3])
		}
		sandbox, name := args[1], "default"
		if len(args) > 2 {
			name = args[2]
		}
		return a.ServiceDelete(sandbox, name)
	default:
		return fmt.Errorf("unknown service subcommand %q", args[0])
	}
}

func parseServiceListArgs(args []string) (service.ServiceListOptions, error) {
	options := service.ServiceListOptions{PageSize: 100, Output: "table"}
	seen := make(map[string]bool)
	for i := 0; i < len(args); i++ {
		flag, value, hasValue := strings.Cut(args[i], "=")
		if flag == "--all-workspaces" {
			if hasValue || seen[flag] {
				return service.ServiceListOptions{}, fmt.Errorf("%s may be specified once without a value", flag)
			}
			seen[flag] = true
			options.AllWorkspaces = true
			continue
		}
		if flag != "--page-size" && flag != "--page-token" && flag != "--output" && flag != "-o" {
			if strings.HasPrefix(args[i], "-") {
				return service.ServiceListOptions{}, fmt.Errorf("unknown flag %q", args[i])
			}
			if options.Sandbox != "" {
				return service.ServiceListOptions{}, fmt.Errorf("unexpected argument %q", args[i])
			}
			options.Sandbox = args[i]
			continue
		}
		seenKey := flag
		if flag == "-o" {
			seenKey = "--output"
		}
		if seen[seenKey] {
			return service.ServiceListOptions{}, fmt.Errorf("%s may be specified only once", seenKey)
		}
		seen[seenKey] = true
		if !hasValue {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return service.ServiceListOptions{}, fmt.Errorf("%s needs a value", flag)
			}
			i++
			value = args[i]
		}
		switch flag {
		case "--page-size":
			pageSize, err := strconv.ParseInt(value, 10, 32)
			if err != nil || pageSize <= 0 {
				return service.ServiceListOptions{}, fmt.Errorf("--page-size must be a positive integer")
			}
			options.PageSize = int(pageSize)
		case "--page-token":
			options.PageToken = value
		case "--output", "-o":
			if value != "table" && value != "yaml" && value != "json" {
				return service.ServiceListOptions{}, fmt.Errorf("--output must be table|yaml|json")
			}
			options.Output = value
		}
	}
	return options, nil
}

func runRule(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cautem rule get|approve|reject|history|clear …")
	}
	switch args[0] {
	case "get", "list", "ls":
		status := ""
		sandbox := ""
		seenStatus := false
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--status":
				if seenStatus {
					return fmt.Errorf("--status may be specified only once")
				}
				i++
				if i >= len(args) {
					return fmt.Errorf("--status needs pending|approved|rejected")
				}
				status = args[i]
				seenStatus = true
			default:
				if strings.HasPrefix(args[i], "-") {
					return fmt.Errorf("unknown flag %q", args[i])
				}
				if sandbox != "" {
					return fmt.Errorf("usage: cautem rule get [sandbox] [--status STATUS]")
				}
				sandbox = args[i]
			}
		}
		return a.RuleListFilter(sandbox, status)
	case "history":
		if len(args) > 2 {
			return fmt.Errorf("usage: cautem rule history [sandbox]")
		}
		sandbox := ""
		if len(args) > 1 {
			sandbox = args[1]
		}
		return a.RuleListFilter(sandbox, "")
	case "approve":
		id, reason, err := parseRuleAction(args[1:])
		if err != nil {
			return err
		}
		_ = reason
		return a.RuleApprove(id)
	case "approve-all":
		sandbox := ""
		includeSecurityFlagged := false
		for _, arg := range args[1:] {
			switch {
			case arg == "--include-security-flagged":
				includeSecurityFlagged = true
			case strings.HasPrefix(arg, "-"):
				return fmt.Errorf("unknown flag %q", arg)
			case sandbox == "":
				sandbox = arg
			default:
				return fmt.Errorf("usage: cautem rule approve-all [NAME] [--include-security-flagged]")
			}
		}
		return a.RuleApproveAll(sandbox, includeSecurityFlagged)
	case "reject":
		id, reason, err := parseRuleAction(args[1:])
		if err != nil {
			return err
		}
		return a.RuleRejectReason(id, reason)
	default:
		return fmt.Errorf("unknown rule subcommand %q", args[0])
	}
}

func parseRuleAction(args []string) (id, reason string, err error) {
	seenID, seenReason := false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--chunk-id":
			if seenID {
				return "", "", fmt.Errorf("chunk ID may be specified only once")
			}
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("--chunk-id needs a value")
			}
			id = args[i]
			seenID = true
		case "--reason":
			if seenReason {
				return "", "", fmt.Errorf("--reason may be specified only once")
			}
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("--reason needs a value")
			}
			reason = args[i]
			seenReason = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("unknown flag %q", args[i])
			}
			if seenID {
				return "", "", fmt.Errorf("unexpected extra argument %q", args[i])
			}
			id, seenID = args[i], true
		}
	}
	if id == "" {
		return "", "", fmt.Errorf("usage: cautem rule approve|reject [--chunk-id] ID [--reason TEXT]")
	}
	return id, reason, nil
}

func runLogs(a *service.App, args []string) error {
	parsed, err := osargs.ParseLogs(args)
	if err != nil {
		return err
	}
	opt := service.LogsOpts{
		Follow:   parsed.Tail,
		All:      parsed.All,
		Lines:    uint32(parsed.N),
		LinesSet: parsed.NSet,
		Since:    parsed.Since,
		Source:   parsed.Source,
		Level:    parsed.Level,
	}
	if parsed.Name != "" {
		for n := range strings.SplitSeq(parsed.Name, ",") {
			n = strings.TrimSpace(n)
			if n != "" {
				opt.Names = append(opt.Names, n)
			}
		}
	}
	return a.LogsOpts(opt)
}
