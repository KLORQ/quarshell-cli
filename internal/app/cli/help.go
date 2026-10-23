package cli

import (
	"fmt"
	"strings"
)

// wantsHelp reports whether argv asks for help (-h/--help/help).
func wantsHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "-h", "--help", "help":
			return true
		}
	}
	return false
}

// helpPath strips help flags and returns the command path for nested help.
func helpPath(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "-h", "--help", "help":
			continue
		default:
			out = append(out, a)
		}
	}
	return out
}

func printHelp(args []string) error {
	path := helpPath(args)
	fmt.Print(helpText(path))
	return nil
}

func helpText(path []string) string {
	if len(path) == 0 {
		return rootHelp
	}
	key := strings.Join(path, " ")
	if t, ok := helpTree[key]; ok {
		return t
	}
	if t, ok := leafCommandHelp(key); ok {
		return t
	}
	for i := len(path) - 1; i >= 1; i-- {
		parent := strings.Join(path[:i], " ")
		if t, ok := helpTree[parent]; ok {
			return t
		}
	}
	if t, ok := helpTree[path[0]]; ok {
		return t
	}
	return rootHelp
}

const rootHelp = `quarshell — agent sandbox CLI

Usage:
  quarshell [global flags] <command> [flags]

Global flags:
  -g, --gateway NAME     Select gateway (or QUARSHELL_GATEWAY)
  --gateway-endpoint URL Connect directly to a gateway endpoint
  --gateway-insecure    Skip TLS certificate verification for the gateway
  --workspace NAME       Default workspace (or QUARSHELL_WORKSPACE)
  -o, --output FORMAT    text|json|yaml
  -v, --verbose          Enable debug logging (repeatable)
      --color <WHEN>     Color output: auto, always, or never [env: QUARSHELL_COLOR]
                          auto honors NO_COLOR, then FORCE_COLOR
  -V, --version          Print the CLI version

Commands:
  sandbox (sb)       Create and manage sandboxes
  cp                 Copy files between the host and a sandbox
  exec               Execute a command in a sandbox
  provider           Provider instances and profiles
  profile            Reusable provider profile definitions
  policy (pol)       Network policy get/set/update/list/history
  rule (rl)          Approval rules
  gateway (gw)       Add/select/login gateways
  workspace (ws)     Workspaces and members
  service (svc)      Expose HTTP services through the gateway
  forward (fwd)      TCP port forwards
  settings           Gateway/local settings
  logs (lg)          Sandbox logs
  status             Gateway connectivity
  init               Write an agent starter policy
  doctor (dr)        Check system prerequisites
  whoami             Identity (supports -o json)
  term               Interactive TUI
  install            Install CLI + ensure local gateway
  ssh-proxy          SSH ProxyCommand helper used by sandbox ssh-config
  completions        Shell completions
  version            Print version

Run 'quarshell <command> --help' for details.
`

var helpTree = map[string]string{
	"sandbox": `quarshell sandbox — manage sandboxes

Usage:
	  quarshell sandbox create|list|get|stop|start|delete|exec|connect|upload|download|ssh-config|provider|template …

Aliases: sb

Examples:
  quarshell sandbox create --name app --from ollama
  quarshell sandbox list
  quarshell sandbox exec app -- ls /
`,
	"sandbox exec": `quarshell sandbox exec — execute a command in a running sandbox

Usage:
  quarshell sandbox exec [NAME] [flags] -- COMMAND [ARG…]

Flags:
  -n, --name NAME     Select sandbox by name
  --workdir DIR       Working directory inside the sandbox
  --timeout SECONDS   Bound command execution time (0 disables the deadline)
  --tty               Force pseudo-terminal allocation
  --no-tty            Disable pseudo-terminal allocation
  --no-login-shell    Run the command with a non-login shell
  --env KEY=VALUE     Set a non-secret command environment variable (repeatable)

When stdin is piped, quarshell forwards it without allocating a pseudo-terminal.
`,
	"sandbox create": `quarshell sandbox create — create a sandbox

Usage:
  quarshell sandbox create --name NAME [flags]

Flags:
  --name NAME
  --gateway-workspace NAME  Select the Gateway workspace
  --gateway URL         Select the Gateway endpoint
  --from base|ollama|cursor|claude|IMAGE_REF|ROOTFS.tar[.gz]
  --image IMAGE
  --policy PATH
  --cpu CORES --memory SIZE   CPU also accepts millicores (500m)
  --gpu [COUNT]
  --template NAME       Apply a local template, or a shared Gateway template when no local match exists
  --provider NAME (repeatable)
  --forward PORT (repeatable)
  --expose PORT          Register an HTTP/WebSocket service during Gateway create
  --expose-authorization-mode strip|bearer-passthrough
  --label KEY=VALUE
  --env KEY=VALUE (repeatable)
  --driver-config-json JSON
  --restart-policy never|on-failure|always
  --upload PATH [--no-git-ignore]
  --auto-providers | --no-auto-providers
  --no-credential-warnings
  --approval-mode manual|auto
  --editor vscode|cursor
  --no-keep --tty --detach
	  --tty                   Allocate a PTY for the main command
	  --detach                Start the main command through Gateway create

Arguments after -- are sent as the sandbox main command. A foreground command
runs through native interactive exec after provisioning; --detach leaves the
main command running in Gateway. A terminal allocates a PTY automatically; use
--tty to force one for redirected input.
--expose keeps the sandbox and supervisor alive. --expose-authorization-mode
defaults to strip; bearer-passthrough forwards one valid Bearer header to the
service. Gateway-managed creates include the exposure in the create request;
if service registration fails, Gateway attempts to remove the new sandbox.
Exposure forces the sandbox to remain available for the registered service.
`,
	"sandbox template": `quarshell sandbox template — workload templates

Usage:
  quarshell sandbox template create|list|get|delete …

Create accepts a positional template name and workload fields:
  quarshell sandbox template create NAME --image IMAGE --cpu 2 --memory 4Gi --gpu 1
  --env KEY=VALUE --label KEY=VALUE --driver-config-json JSON

Add --gateway to create/list/get/delete a workspace-scoped template on the
selected Gateway. Without it, templates remain local to this CLI profile.
`,
	"sandbox provider": `quarshell sandbox provider — attach providers to a sandbox

Usage:
  quarshell sandbox provider list|attach|detach|status …
`,
	"sandbox list": `quarshell sandbox list — list sandboxes

Usage:
  quarshell sandbox list [--page-size N] [--page-token TOKEN] [--selector EXPR] [-o table|yaml|json]
  quarshell sandbox list [--ids|--names] [--all-workspaces]

Flags:
  --page-size N         Maximum sandboxes in this page (default 100)
  --page-token TOKEN    Continue from the previous page
  --selector EXPR       Filter by sandbox labels
  -o, --output FORMAT   table, yaml, or json
  --ids                 Print only sandbox IDs
  --names               Print only sandbox names
  --all-workspaces      Include sandboxes from every workspace

The ls alias is also accepted.
`,
	"sandbox get": `quarshell sandbox get — inspect one sandbox or print its active policy

Usage:
	  quarshell sandbox get NAME [--policy-only]

--policy-only prints the active full policy YAML to stdout.
`,
	"sandbox start": `quarshell sandbox start — start a stopped sandbox

Usage:
  quarshell sandbox start [NAME]
`,
	"sandbox stop": `quarshell sandbox stop — stop a running sandbox

Usage:
  quarshell sandbox stop [NAME]
`,
	"sandbox delete": `quarshell sandbox delete — delete one or more sandboxes

Usage:
  quarshell sandbox delete NAME...
  quarshell sandbox delete --all

The rm and remove aliases are also accepted. --all cannot be combined with names.
`,
	"sandbox upload": `quarshell sandbox upload — upload files into a sandbox

Usage:
  quarshell sandbox upload NAME LOCAL_PATH [SANDBOX_DEST] [--no-git-ignore]

The default destination is /workspace. --no-git-ignore includes files matched
by .gitignore and is available only for upload.
`,
	"sandbox download": `quarshell sandbox download — download files from a sandbox

Usage:
  quarshell sandbox download NAME SANDBOX_PATH [LOCAL_DEST]

The default local destination is the current directory.
`,
	"sandbox ssh-config": `quarshell sandbox ssh-config — print or install SSH configuration

Usage:
  quarshell sandbox ssh-config NAME [--install]
`,
	"sandbox connect": `quarshell sandbox connect — connect with an editor or command

Usage:
  quarshell sandbox connect [NAME] [--editor vscode|cursor]
  quarshell sandbox connect [NAME] -- COMMAND [ARG...]

--editor cannot be combined with a command.
`,
	"sandbox provider list": `quarshell sandbox provider list — list providers attached to a sandbox

Usage:
  quarshell sandbox provider list [SANDBOX] [--page-size N] [--page-token TOKEN] [-o table|yaml|json]

The default sandbox is the last-used sandbox. Table output prints the next page token; JSON/YAML include next_page_token.
`,
	"sandbox provider attach": `quarshell sandbox provider attach — attach a provider

Usage:
  quarshell sandbox provider attach SANDBOX PROVIDER
`,
	"sandbox provider detach": `quarshell sandbox provider detach — detach a provider

Usage:
  quarshell sandbox provider detach SANDBOX PROVIDER
`,
	"sandbox provider status": `quarshell sandbox provider status — check whether a sandbox applied a provider change

Usage:
  quarshell sandbox provider status SANDBOX PROVIDER [--receipt ID] [--wait] [--timeout SECONDS] [-o table|yaml|json]

--wait polls until the provider mutation reaches a terminal readiness state.
`,
	"sandbox template create": `quarshell sandbox template create — save workload defaults

Usage:
  quarshell sandbox template create [NAME|--name NAME] [flags]

Flags:
  NAME or --name NAME       Template name
  --gateway                 Store the template in the selected Gateway workspace
  --image IMAGE             Workload image
  --from SOURCE             Preset or image source
  --policy PATH             Network policy file
  --cpu CORES               CPU limit, including millicore values
  --memory SIZE             Memory limit
  --gpu [COUNT]             Optional GPU count
  --env KEY=VALUE           Environment value (repeatable)
  --label KEY=VALUE         Template label (repeatable)
  --annotation KEY=VALUE    Gateway template annotation (repeatable)
  --ready-within DURATION   Gateway startup readiness target (for example 5m)
  --max-burst COUNT         Gateway startup burst limit
  --driver-config-json JSON Backend-specific configuration
  --provider NAME           Provider to attach (repeatable)

--annotation, --ready-within, and --max-burst require --gateway.
`,
	"sandbox template list": `quarshell sandbox template list — list saved templates

Usage:
  quarshell sandbox template list [--gateway] [--page-size N] [--page-token TOKEN] [--label-selector EXPR] [-o table|yaml|json]
`,
	"sandbox template get": `quarshell sandbox template get — inspect a saved template

Usage:
  quarshell sandbox template get NAME [--gateway]
`,
	"sandbox template delete": `quarshell sandbox template delete — delete a saved template

Usage:
  quarshell sandbox template delete NAME [--gateway]
`,
	"provider": `quarshell provider — provider instances

Usage:
  quarshell provider create|list|get|update|delete|profile|refresh|effective …

Examples:
  quarshell provider create --name gh --type github --from-existing
  quarshell provider refresh configure NAME --credential-key K --strategy oauth2-refresh-token
`,
	"provider profile": `quarshell provider profile — custom provider YAML profiles

Usage:
  quarshell profile list|show|import|update|export|delete|lint …
  quarshell profile import --url https://example.org/profile.yaml
  quarshell profile import --from ./provider-profiles
`,
	"profile": `quarshell profile — reusable provider definitions

Usage:
  quarshell profile list|show|import|update|export|delete|lint …
`,
	"provider refresh": `quarshell provider refresh — credential refresh strategies

Usage:
  quarshell provider refresh status|configure|rotate|delete …

Strategies: env | oauth2-refresh-token | oauth2-client-credentials | aws-sts-assume-role
`,
	"policy": `quarshell policy — network policy

Usage:
  quarshell policy get|set|update|list|history|delete|check …
`,
	"gateway": `quarshell gateway — manage gateways

Usage:
  quarshell gateway ensure|add|remove|select|info|list|login|logout

  ensure   start/select local gateway on 127.0.0.1:17670 if needed
`,
	"workspace": `quarshell workspace — workspaces (gateway-backed)

Usage:
  quarshell workspace create --name NAME
  quarshell workspace get NAME
  quarshell workspace list [--page-size N] [--page-token TOKEN] [--label-selector EXPR] [-o table|yaml|json]
  quarshell workspace delete NAME...
  quarshell workspace member add|remove|list …
`,
	"workspace create": `quarshell workspace create — create a workspace

Usage:
  quarshell workspace create --name NAME [--label KEY=VALUE ...]

Repeat --label to attach workspace metadata labels.
`,
	"workspace delete": `quarshell workspace delete — delete one or more workspaces

Usage:
  quarshell workspace delete NAME...
`,
	"workspace list": `quarshell workspace list — list a page of workspaces

Usage:
  quarshell workspace list [--page-size N] [--page-token TOKEN] [--label-selector EXPR] [-o table|yaml|json]

The default page size is 100. The returned page token can be passed to --page-token.
`,
	"workspace member": `quarshell workspace member — manage members

Usage:
  quarshell workspace member add --workspace NAME --subject SUBJECT [--role user|admin]
  quarshell workspace member remove --workspace NAME --subject SUBJECT
  quarshell workspace member list --workspace NAME [--page-size N] [--page-token TOKEN] [-o table|yaml|json]
`,
	"service": `quarshell service — expose HTTP services

Usage:
  quarshell service expose <sandbox> <port> [name]
  quarshell service list|get|delete …

URL: use the service address returned by the expose command
`,
	"forward": `quarshell forward — TCP forwards into a sandbox

Usage:
  quarshell forward start <host-port> <sandbox> [-d]
  quarshell forward stop <id>
  quarshell forward list
`,
	"settings": `quarshell settings — key/value settings

Usage:
	  quarshell settings get [SANDBOX] [--global] [--json]
	  quarshell settings set [SANDBOX] --key KEY --value VALUE [--global --yes]
	  quarshell settings delete [SANDBOX] --key KEY [--global --yes]
`,
	"logs": `quarshell logs — sandbox logs

Usage:
  quarshell logs [name] [--tail] [-n N] [--since 5m]
  Name defaults to the last-used sandbox.
`,
	"doctor": `quarshell doctor — check the Docker prerequisite

Usage:
  quarshell doctor check
`,
	"install": `quarshell install — install CLI binary and ensure local gateway

Usage:
  quarshell install [--force]

Copies quarshell to ~/.local/share/quarshell/bin, symlinks ~/.local/bin/quarshell,
then starts/selects a local quarshell-gateway if needed.
`,
	"whoami": `quarshell whoami — print identity

Usage:
  quarshell whoami
  quarshell -o json whoami
`,
	"completions": `quarshell completions — shell completions

Usage:
  quarshell completions <bash|zsh|fish|powershell>
`,
	"ssh-proxy": `quarshell ssh-proxy — SSH ProxyCommand helper

Usage:
  quarshell ssh-proxy --gateway URL --sandbox-id ID --token TOKEN [--gateway-name NAME]
  quarshell ssh-proxy --gateway-name NAME --name SANDBOX
  quarshell ssh-proxy --server URL --name SANDBOX
  quarshell ssh-proxy SANDBOX

This command is invoked by SSH configurations generated with sandbox ssh-config.
`,
	"version": `quarshell version — print the installed CLI version

Usage:
  quarshell version
`,
	"status": `quarshell status — gateway connectivity

Usage:
  quarshell status
  quarshell -o json status
`,
	"init": `quarshell init — write an agent starter policy

Usage:
  quarshell init --agent cursor [--dir DIR] [--force]
`,
	"exec": `quarshell exec — execute a command in a sandbox

Usage:
  quarshell exec [--name] NAME -- COMMAND [ARG ...]
`,
	"cp": `quarshell cp — copy files between the host and a sandbox

Usage:
  quarshell cp <local-path> <sandbox>:/path
  quarshell cp <sandbox>:/path <local-path>
`,
	"term": `quarshell term — interactive sandbox browser

Usage:
  quarshell term [--theme auto|dark|light]

QUARSHELL_THEME sets the default. Auto uses COLORFGBG when available and falls back to dark.

Keys:
  ↑/↓ or j/k   Select a sandbox
  /             Search by name, status, or image
  d             Show sandbox details
  enter or c    Connect to the selected sandbox
  e             Exec in the selected sandbox
  s/S           Stop a running sandbox / start a stopped sandbox
  x             Delete the selected sandbox after confirmation
  l             Follow selected sandbox logs
  a             Follow logs from all sandboxes
  s (in logs)    Cycle source filter (local: all/sandbox/proxy; Gateway: all/gateway/sandbox)
  p             Pause/resume the live log display
  ↑/↓ or j/k    Select a live log line
  Enter         Show the selected log line
  PgUp/PgDn or Ctrl-B/Ctrl-F  Move the selected line by one viewport
  v             Start/end a visual range; j/k extend the range
  y/Y           Copy the selected line/range / visible log lines (terminal clipboard support required)
  g/G           Select the first/last live log line
  L             Print the selected sandbox logs and return
  r             Refresh the sandbox list
  q             Quit (Esc returns from logs)
`,
	"rule": `quarshell rule — approval rules (MVP)

Usage:
  quarshell rule get|approve|approve-all|reject|history|clear …
`,
}
