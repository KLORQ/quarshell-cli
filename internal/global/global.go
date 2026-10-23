// Package global parses OpenShell-style CLI globals once before subcommands.
package global

import (
	"os"
	"strings"
)

// Context is the resolved CLI session context.
type Context struct {
	GatewayName     string // -g / --gateway (name)
	GatewayURL      string // resolved endpoint override
	Workspace       string // --workspace / OPENSHELL_WORKSPACE
	Output          string // -o / --output: json|yaml|text
	Version         bool   // --version / -V
	GatewayInsecure bool   // --gateway-insecure
	Verbose         bool   // --verbose / -v
	Color           string // --color / QUARSHELL_COLOR: auto|always|never
}

// Parse strips leading global flags from args and returns the remainder.
//
// Supported globals (anywhere before the first non-flag positional command group
// is handled by stripping all leading dash-args that match known globals):
//
//	-g/--gateway NAME
//	--gateway-endpoint URL
//	--workspace NAME
//	-o/--output json|yaml|text
//
// Env: OPENSHELL_GATEWAY (name or URL), CAUTEM_GATEWAY_URL, OPENSHELL_WORKSPACE, CAUTEM_WORKSPACE.
func Parse(args []string) (Context, []string) {
	ctx := Context{
		Workspace:       firstNonEmpty(os.Getenv("CAUTEM_WORKSPACE"), os.Getenv("OPENSHELL_WORKSPACE"), "default"),
		Output:          "text",
		Color:           firstNonEmpty(os.Getenv("QUARSHELL_COLOR"), "auto"),
		GatewayInsecure: envEnabled("CAUTEM_GATEWAY_INSECURE") || envEnabled("OPENSHELL_GATEWAY_INSECURE"),
	}
	if v := firstNonEmpty(os.Getenv("CAUTEM_GATEWAY_URL"), os.Getenv("OPENSHELL_GATEWAY_ENDPOINT"), os.Getenv("OPENSHELL_GATEWAY")); v != "" {
		if looksLikeURL(v) {
			ctx.GatewayURL = v
		} else {
			ctx.GatewayName = v
		}
	}

	out := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--version" || arg == "-V" {
			ctx.Version = true
		}
	}
	i := 0
	// Only peel globals from the front so `sandbox create --workspace DIR` keeps its flag.
	for i < len(args) {
		a := args[i]
		switch a {
		case "-g", "--gateway":
			i++
			if i < len(args) {
				v := args[i]
				if looksLikeURL(v) {
					ctx.GatewayURL = v
					ctx.GatewayName = ""
				} else {
					ctx.GatewayName = v
				}
				i++
			}
		case "--gateway-endpoint":
			i++
			if i < len(args) {
				ctx.GatewayURL, ctx.GatewayName = args[i], ""
				i++
			}
		case "--gateway-insecure":
			ctx.GatewayInsecure = true
			i++
		case "--verbose", "-v", "-vv", "-vvv":
			ctx.Verbose = true
			i++
		case "--color":
			i++
			ctx.Color = ""
			if i < len(args) && !strings.HasPrefix(args[i], "-") {
				ctx.Color = strings.ToLower(args[i])
				i++
			}
		case "--workspace":
			i++
			if i < len(args) {
				ctx.Workspace = args[i]
				i++
			}
		case "-o", "--output":
			i++
			if i < len(args) {
				ctx.Output = strings.ToLower(args[i])
				i++
			}
		default:
			if after, ok := strings.CutPrefix(a, "-g="); ok {
				v := after
				if looksLikeURL(v) {
					ctx.GatewayURL = v
				} else {
					ctx.GatewayName = v
				}
				i++
				continue
			}
			if after, ok := strings.CutPrefix(a, "--gateway="); ok {
				v := after
				if looksLikeURL(v) {
					ctx.GatewayURL = v
				} else {
					ctx.GatewayName = v
				}
				i++
				continue
			}
			if after, ok := strings.CutPrefix(a, "--gateway-endpoint="); ok {
				ctx.GatewayURL, ctx.GatewayName = after, ""
				i++
				continue
			}
			if after, ok := strings.CutPrefix(a, "--color="); ok {
				ctx.Color = strings.ToLower(after)
				i++
				continue
			}
			if after, ok := strings.CutPrefix(a, "--workspace="); ok {
				ctx.Workspace = after
				i++
				continue
			}
			if after, ok := strings.CutPrefix(a, "-o="); ok {
				ctx.Output = strings.ToLower(after)
				i++
				continue
			}
			if after, ok := strings.CutPrefix(a, "--output="); ok {
				ctx.Output = strings.ToLower(after)
				i++
				continue
			}
			out = append(out, args[i:]...)
			out = stripGatewayEndpoint(out, &ctx)
			return ctx, out
		}
	}
	out = stripGatewayEndpoint(out, &ctx)
	return ctx, out
}

func stripGatewayEndpoint(args []string, ctx *Context) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(result, args[i:]...)
		}
		if arg == "--gateway-insecure" {
			ctx.GatewayInsecure = true
			continue
		}
		if arg == "--verbose" || arg == "-v" || arg == "-vv" || arg == "-vvv" {
			ctx.Verbose = true
			continue
		}
		if arg == "--color" {
			ctx.Color = ""
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				ctx.Color = strings.ToLower(args[i+1])
				i++
			}
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--color="); ok {
			ctx.Color = strings.ToLower(value)
			continue
		}
		if arg == "--gateway-endpoint" {
			if i+1 < len(args) {
				ctx.GatewayURL, ctx.GatewayName = args[i+1], ""
				i++
			}
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--gateway-endpoint="); ok {
			ctx.GatewayURL, ctx.GatewayName = value, ""
			continue
		}
		result = append(result, arg)
	}
	return result
}

func looksLikeURL(s string) bool {
	s = strings.TrimSpace(s)
	return strings.Contains(s, "://") || strings.HasPrefix(s, "127.") || strings.HasPrefix(s, "localhost")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func envEnabled(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
