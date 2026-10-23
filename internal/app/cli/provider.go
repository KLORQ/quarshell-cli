package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/KLORQ/quarshell-cli/internal/providerflags"
	"github.com/KLORQ/quarshell-cli/internal/service"
)

func runProvider(a *service.App, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: quarshell provider create --name NAME --type PROFILE [--from-existing|--credential KEY]")
	}
	switch args[0] {
	case "list-profiles":
		rest, scope, workspace, err := parseProfileScope(a, args[1:])
		if err != nil {
			return err
		}
		if len(rest) != 0 {
			pageSize, pageToken, output, err := parseProfileListOptions(rest)
			if err != nil {
				return err
			}
			return a.ProviderProfileListPage(scope, workspace, pageSize, pageToken, output)
		}
		return a.ProviderProfileListPage(scope, workspace, 100, "", "table")
	case "profile":
		if len(args) < 2 {
			return fmt.Errorf("usage: quarshell provider profile list|show|import|export|delete|lint …")
		}
		rest, scope, workspace, err := parseProfileScope(a, args[2:])
		if err != nil {
			return err
		}
		switch args[1] {
		case "list", "ls":
			pageSize, pageToken, output, err := parseProfileListOptions(rest)
			if err != nil {
				return err
			}
			return a.ProviderProfileListPage(scope, workspace, pageSize, pageToken, output)
		case "show", "describe", "export":
			id, outFmt, err := parseProfileIO(rest, false)
			if err != nil {
				return err
			}
			if id == "" {
				return fmt.Errorf("usage: quarshell provider profile export <id> [-o yaml|json]")
			}
			return a.ProviderProfileShowFmt(id, outFmt, scope, workspace)
		case "import":
			path, _, err := parseProfileIO(rest, true)
			if err != nil {
				return err
			}
			if path == "" {
				return fmt.Errorf("usage: quarshell provider profile import -f <file.yaml>")
			}
			return a.ProviderProfileImport(path, scope, workspace)
		case "update":
			path, _, err := parseProfileIO(rest, true)
			if err != nil {
				return err
			}
			if path == "" {
				return fmt.Errorf("usage: quarshell provider profile update -f <file.yaml>")
			}
			return a.ProviderProfileUpdate(path, scope, workspace)
		case "delete":
			if len(args) < 3 {
				return fmt.Errorf("usage: quarshell provider profile delete <id>")
			}
			if len(rest) != 1 {
				return fmt.Errorf("usage: quarshell profile delete <id> [--workspace NAME|--global]")
			}
			return a.ProviderProfileDelete(rest[0], scope, workspace)
		case "lint":
			path, _, err := parseProfileIO(rest, true)
			if err != nil {
				return err
			}
			if path == "" {
				return fmt.Errorf("usage: quarshell provider profile lint -f <file.yaml>")
			}
			return a.ProviderProfileLint(path)
		default:
			return fmt.Errorf("unknown profile subcommand %q", args[1])
		}
	case "create":
		parsed, err := providerflags.ParseCreate(args[1:], os.LookupEnv)
		if err != nil {
			return err
		}
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--credential" && strings.Contains(args[i+1], "=") {
				fmt.Fprintln(os.Stderr, "warn: --credential KEY=VALUE may appear in shell history/ps; prefer bare --credential KEY")
				break
			}
		}
		return a.ProviderCreate(parsed)
	case "update":
		if len(args) < 2 {
			return fmt.Errorf("usage: quarshell provider update <name> [--from-existing|--credential KEY[=VALUE]]")
		}
		name := args[1]
		fromExisting := false
		credentials := map[string]string{}
		for i := 2; i < len(args); i++ {
			switch args[i] {
			case "--from-existing":
				fromExisting = true
			case "--credential":
				i++
				if i >= len(args) {
					return fmt.Errorf("--credential needs KEY or KEY=VALUE")
				}
				raw := args[i]
				if k, v, ok := strings.Cut(raw, "="); ok {
					fmt.Fprintln(os.Stderr, "warn: --credential KEY=VALUE may appear in shell history/ps; prefer bare --credential KEY")
					credentials[strings.TrimSpace(k)] = v
				} else {
					key := strings.TrimSpace(raw)
					v, ok := os.LookupEnv(key)
					if !ok || strings.TrimSpace(v) == "" {
						return fmt.Errorf("--credential %s: env var not set on host", key)
					}
					credentials[key] = v
				}
			default:
				return fmt.Errorf("unknown flag %q", args[i])
			}
		}
		return a.ProviderUpdate(name, fromExisting, credentials)
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("usage: quarshell provider get <name>")
		}
		return a.ProviderGet(args[1])
	case "delete", "rm":
		if len(args) != 2 {
			return fmt.Errorf("usage: quarshell provider delete <name>")
		}
		return a.ProviderDelete(args[1])
	case "refresh":
		if len(args) < 2 {
			return fmt.Errorf("usage: quarshell provider refresh <name>|status|configure|rotate|delete …")
		}
		switch args[1] {
		case "status":
			if len(args) != 3 {
				return fmt.Errorf("usage: quarshell provider refresh status <name>")
			}
			return a.ProviderRefreshStatus(args[2])
		case "configure":
			if len(args) < 3 {
				return fmt.Errorf("usage: quarshell provider refresh configure <name> --credential-key K --strategy S")
			}
			opt, err := providerflags.ParseRefreshConfigure(args[2], args[3:])
			if err != nil {
				return err
			}
			return a.ProviderRefreshConfigure(opt)
		case "rotate":
			if len(args) < 3 {
				return fmt.Errorf("usage: quarshell provider refresh rotate <name> --credential-key K")
			}
			key, err := providerflags.ParseRefreshKey(args[2], args[3:])
			if err != nil {
				return err
			}
			return a.ProviderRefreshRotate(args[2], key)
		case "delete":
			if len(args) < 3 {
				return fmt.Errorf("usage: quarshell provider refresh delete <name> --credential-key K")
			}
			key, err := providerflags.ParseRefreshKey(args[2], args[3:])
			if err != nil {
				return err
			}
			return a.ProviderRefreshDelete(args[2], key)
		default:
			if len(args) != 2 {
				return fmt.Errorf("usage: quarshell provider refresh <name>")
			}
			return a.ProviderRefresh(args[1])
		}
	case "list", "ls":
		pageSize, pageToken, selector, output, allWorkspaces := int32(100), "", "", "table", false
		seen := map[string]bool{}
		for i := 1; i < len(args); i++ {
			arg := args[i]
			flag, value, hasValue := strings.Cut(arg, "=")
			if flag == "-o" {
				flag = "--output"
			}
			if flag == "--all-workspaces" {
				if allWorkspaces {
					return fmt.Errorf("provider list: --all-workspaces may be specified only once")
				}
				allWorkspaces = true
				continue
			}
			if flag != "--page-size" && flag != "--page-token" && flag != "--label-selector" && flag != "--output" {
				if !strings.HasPrefix(arg, "-") {
					return fmt.Errorf("provider list: extra argument %q", arg)
				}
				return fmt.Errorf("provider list: unknown flag %q", arg)
			}
			if seen[flag] {
				return fmt.Errorf("provider list: %s may be specified only once", flag)
			}
			seen[flag] = true
			if !hasValue {
				if i+1 >= len(args) || (flag != "--page-token" && strings.HasPrefix(args[i+1], "-")) {
					return fmt.Errorf("provider list: %s needs a value", flag)
				}
				i++
				value = args[i]
			}
			switch flag {
			case "--page-size":
				n, err := strconv.ParseInt(value, 10, 32)
				if err != nil || n <= 0 {
					return fmt.Errorf("provider list: --page-size must be a positive integer")
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
		return a.ProviderListPage(pageSize, pageToken, selector, output, allWorkspaces)
	case "effective":
		if len(args) != 2 {
			return fmt.Errorf("usage: quarshell provider effective <sandbox>")
		}
		return a.ProviderEffective(args[1])
	default:
		return fmt.Errorf("unknown provider subcommand %q", args[0])
	}
}

func parseProfileListOptions(args []string) (pageSize int32, pageToken, output string, err error) {
	pageSize, output = 100, "table"
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		flag, value, hasValue := strings.Cut(arg, "=")
		if flag == "-o" {
			flag = "--output"
		}
		if flag != "--page-size" && flag != "--page-token" && flag != "--output" {
			return 0, "", "", fmt.Errorf("unknown profile list flag %q", arg)
		}
		if seen[flag] {
			return 0, "", "", fmt.Errorf("%s may be specified only once", flag)
		}
		seen[flag] = true
		if !hasValue {
			if i+1 >= len(args) || (flag != "--page-token" && strings.HasPrefix(args[i+1], "-")) {
				return 0, "", "", fmt.Errorf("%s needs a value", flag)
			}
			i++
			value = args[i]
		}
		switch flag {
		case "--page-size":
			n, parseErr := strconv.ParseInt(value, 10, 32)
			if parseErr != nil || n <= 0 {
				return 0, "", "", fmt.Errorf("--page-size must be a positive integer")
			}
			pageSize = int32(n)
		case "--page-token":
			pageToken = value
		case "--output":
			output = value
		}
	}
	if output != "table" && output != "yaml" && output != "json" {
		return 0, "", "", fmt.Errorf("--output must be table|yaml|json")
	}
	return pageSize, pageToken, output, nil
}

func parseProfileScope(a *service.App, args []string) (rest []string, scope, workspace string, err error) {
	scope, workspace = "global", ""
	if a != nil && a.GlobalWorkspace != "" && a.GlobalWorkspace != "default" {
		scope, workspace = "workspace", a.GlobalWorkspace
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--global":
			scope, workspace = "global", ""
		case "--workspace":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return nil, "", "", fmt.Errorf("--workspace needs a name")
			}
			scope, workspace = "workspace", args[i]
		default:
			if after, ok := strings.CutPrefix(args[i], "--workspace="); ok {
				workspace = strings.TrimSpace(after)
				if workspace == "" {
					return nil, "", "", fmt.Errorf("--workspace needs a name")
				}
				scope = "workspace"
				continue
			}
			rest = append(rest, args[i])
		}
	}
	return rest, scope, workspace, nil
}
