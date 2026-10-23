package cli

import (
	"fmt"
	"strings"
)

type leafHelp [3]string

func newLeafHelp(description, usage string, notes ...string) leafHelp {
	entry := leafHelp{description, usage, ""}
	if len(notes) > 0 {
		entry[2] = notes[0]
	}
	return entry
}

// leafCommandHelp documents command paths that are too specific to be useful
// with only their parent group's summary.
func leafCommandHelp(path string) (string, bool) {
	entry, ok := leafHelpEntries[path]
	if !ok {
		return "", false
	}
	var out strings.Builder
	fmt.Fprintf(&out, "cautem %s — %s\n\nUsage:\n", path, entry[0])
	for _, line := range strings.Split(entry[1], "\n") {
		fmt.Fprintf(&out, "  cautem %s\n", line)
	}
	if entry[2] != "" {
		fmt.Fprintf(&out, "\n%s\n", entry[2])
	}
	return out.String(), true
}

var leafHelpEntries = map[string]leafHelp{
	"rule get":                   newLeafHelp("list approval requests", "rule get [SANDBOX] [--status pending|approved|rejected]", "Use `rule history` to include completed requests."),
	"rule approve":               newLeafHelp("approve one request", "rule approve --chunk-id ID", "The request ID is shown by `rule get`."),
	"rule reject":                newLeafHelp("reject one request", "rule reject --chunk-id ID [--reason TEXT]", "The request ID is shown by `rule get`."),
	"rule approve-all":           newLeafHelp("approve eligible requests", "rule approve-all [SANDBOX] [--include-security-flagged]", "Security-flagged requests are skipped unless explicitly included."),
	"rule history":               newLeafHelp("list completed approval requests", "rule history [SANDBOX]"),
	"workspace get":              newLeafHelp("show one workspace", "workspace get NAME"),
	"service expose":             newLeafHelp("expose a sandbox HTTP service", "service expose SANDBOX PORT [NAME] [--authorization-mode strip|bearer-passthrough]", "bearer-passthrough forwards one syntactically valid application Bearer header; strip remains the default."),
	"service list":               newLeafHelp("list exposed services", "service list [SANDBOX] [--page-size N] [--page-token TOKEN] [--all-workspaces] [-o table|yaml|json]", "Lists one cursor page in the selected workspace. Table output includes the service URL and next page token; JSON/YAML include endpoint metadata and next_page_token. --all-workspaces requires a Gateway platform administrator and overrides workspace scope."),
	"service get":                newLeafHelp("show an exposed service", "service get SANDBOX [NAME]"),
	"service delete":             newLeafHelp("remove an exposed service", "service delete SANDBOX [NAME]"),
	"forward start":              newLeafHelp("forward a local TCP port", "forward start [BIND:]PORT [SANDBOX] [-d|--background]"),
	"forward stop":               newLeafHelp("stop a TCP port forward", "forward stop PORT [SANDBOX]"),
	"forward list":               newLeafHelp("list active TCP port forwards", "forward list"),
	"forward service":            newLeafHelp("forward a local port to a sandbox loopback service", "forward service --target-port PORT [--target-host HOST] [--local [BIND:]PORT] [SANDBOX]", "The target host must be loopback. Omit --local to use the target port locally; use --local 0 to let the OS choose a port."),
	"term":                       newLeafHelp("launch the interactive sandbox browser", "term [--theme auto|dark|light]", "QUARSHELL_THEME sets the default. Auto uses COLORFGBG when available and falls back to dark."),
	"policy set":                 newLeafHelp("set a sandbox or gateway policy", "policy set [SANDBOX] --policy PATH [--wait] [--global] [--yes]"),
	"policy update":              newLeafHelp("update policy rules", "policy update SANDBOX [--add-endpoint SPEC [--rule-name NAME]|--add-allow HOST:PORT[,PORT...]:METHOD:PATH|--add-deny HOST:PORT[,PORT...]:METHOD:PATH|--remove-endpoint HOST:PORT|--remove-rule NAME] [--binary PATH|--any-binary] [--endpoint-path PATH] [--dry-run] [--wait] [--timeout SECONDS]", "Endpoint additions, L7 additions, and removals use the Gateway atomic merge API and may be combined. L7 operations require --rule-name and the full binary scope (--binary PATH or --any-binary); list every port on the target endpoint. --endpoint-path disambiguates endpoints with path selectors. --dry-run previews the merged policy without updating the Gateway. --timeout defaults to 60 seconds."),
	"policy get":                 newLeafHelp("show the effective or base policy", "policy get [SANDBOX] [--full|--base] [--rev N] [--global]"),
	"policy list":                newLeafHelp("list policy revisions for a sandbox", "policy list SANDBOX [--page-size N] [--page-token TOKEN] [-o table|yaml|json]", "Lists one Gateway cursor page. Table output prints the next page token; JSON/YAML include next_page_token."),
	"policy history":             newLeafHelp("list sandbox or global policy revision history", "policy history [SANDBOX|--global] [--page-size N] [--page-token TOKEN] [-o table|yaml|json]", "Global history requires a Gateway platform administrator. Table output prints the next page token; JSON/YAML include next_page_token."),
	"policy delete":              newLeafHelp("clear the gateway-wide policy", "policy delete --global"),
	"settings get":               newLeafHelp("read sandbox or Gateway settings", "settings get [SANDBOX] [--global] [--json]"),
	"settings set":               newLeafHelp("write a setting", "settings set [SANDBOX] --key KEY --value VALUE [--global] [--yes]"),
	"settings delete":            newLeafHelp("delete a setting", "settings delete [SANDBOX] --key KEY [--global] [--yes]"),
	"provider create":            newLeafHelp("create a provider instance", "provider create --name NAME --type PROFILE [--global-profile] [--from-existing|--credential KEY] [--config KEY=VALUE]", "--global-profile reads the profile from the global catalog; the provider instance remains in the selected workspace."),
	"provider get":               newLeafHelp("show one provider instance", "provider get NAME"),
	"sandbox provider status":    newLeafHelp("check whether a sandbox applied a provider change", "sandbox provider status [OPTIONS] <NAME> <PROVIDER>"),
	"provider list":              newLeafHelp("list provider instances", "provider list [--page-size N] [--page-token TOKEN] [--label-selector EXPR] [--all-workspaces] [-o table|yaml|json]"),
	"provider list-profiles":     newLeafHelp("list provider profile definitions", "provider list-profiles [--workspace NAME|--global] [--page-size N] [--page-token TOKEN] [-o table|yaml|json]"),
	"provider update":            newLeafHelp("update provider credentials", "provider update NAME [--from-existing|--credential KEY[=VALUE]]"),
	"provider delete":            newLeafHelp("delete one provider instance", "provider delete NAME"),
	"profile list":               newLeafHelp("list reusable provider profiles", "profile list [--workspace NAME|--global] [--page-size N] [--page-token TOKEN] [-o table|yaml|json]"),
	"profile describe":           newLeafHelp("show a provider profile", "profile describe ID [-o yaml|json] [--workspace NAME|--global]"),
	"profile export":             newLeafHelp("export a provider profile", "profile export ID [-o yaml|json] [--workspace NAME|--global]"),
	"profile import":             newLeafHelp("import provider profiles from a local source or HTTPS URL", "profile import SOURCE [--workspace NAME|--global]\nprofile import -f|--file|--from PATH\nprofile import --url HTTPS_URL", "SOURCE may be one YAML/JSON file, a directory of YAML/JSON files, or an HTTPS URL. The source can also be supplied positionally."),
	"profile update":             newLeafHelp("update an imported provider profile", "profile update SOURCE [--workspace NAME|--global]\nprofile update -f|--file|--from PATH\nprofile update --url HTTPS_URL", "The profile must include the current resource_version; export the profile first and preserve that version."),
	"profile lint":               newLeafHelp("validate local or HTTPS provider profiles", "profile lint SOURCE\nprofile lint -f|--file|--from PATH\nprofile lint --url HTTPS_URL", "SOURCE may be one YAML/JSON file, a directory of YAML/JSON files, or an HTTPS URL."),
	"profile delete":             newLeafHelp("delete a provider profile", "profile delete ID [--workspace NAME|--global]"),
	"gateway add":                newLeafHelp("register a gateway", "gateway add ENDPOINT [--name NAME] [--local|--remote USER@HOST] [--oidc-issuer URL] [--oidc-client-id ID]"),
	"gateway remove":             newLeafHelp("remove a registered gateway", "gateway remove NAME"),
	"gateway login":              newLeafHelp("authenticate to the selected gateway", "gateway login [TOKEN]"),
	"gateway logout":             newLeafHelp("end the selected gateway session", "gateway logout"),
	"gateway select":             newLeafHelp("select a registered gateway", "gateway select [NAME]"),
	"gateway info":               newLeafHelp("show selected gateway information", "gateway info"),
	"gateway list":               newLeafHelp("list registered gateways", "gateway list"),
	"doctor check":               newLeafHelp("check the local environment", "doctor check"),
	"workspace member add":       newLeafHelp("add a workspace member", "workspace member add --workspace NAME --subject SUBJECT [--role user|admin]"),
	"workspace member remove":    newLeafHelp("remove a workspace member", "workspace member remove --workspace NAME --subject SUBJECT"),
	"workspace member list":      newLeafHelp("list workspace members", "workspace member list --workspace NAME [--page-size N] [--page-token TOKEN] [-o table|yaml|json]"),
	"provider refresh status":    newLeafHelp("show credential refresh status", "provider refresh status NAME"),
	"provider refresh configure": newLeafHelp("configure credential refresh", "provider refresh configure NAME --credential-key KEY --strategy env|oauth2-refresh-token|oauth2-client-credentials|aws-sts-assume-role [--material KEY=VALUE] [--secret-material-env KEY[=ENVVAR]] [--secret-material-key KEY] [--credential-expires-at TIMESTAMP]", "Use --secret-material-env to read a non-empty value from the CLI process environment without putting the value in argv; those keys are automatically marked secret. --material passes its value in argv."),
	"provider refresh rotate":    newLeafHelp("rotate a refresh credential", "provider refresh rotate NAME --credential-key KEY"),
	"provider refresh delete":    newLeafHelp("remove a refresh credential", "provider refresh delete NAME --credential-key KEY"),
	"provider profile list":      newLeafHelp("list reusable provider profiles", "provider profile list [--workspace NAME|--global] [--page-size N] [--page-token TOKEN] [-o table|yaml|json]"),
	"provider profile describe":  newLeafHelp("show a provider profile", "provider profile describe ID [-o yaml|json] [--workspace NAME|--global]"),
	"provider profile export":    newLeafHelp("export a provider profile", "provider profile export ID [-o yaml|json] [--workspace NAME|--global]"),
	"provider profile import":    newLeafHelp("import provider profiles from a local source or HTTPS URL", "provider profile import SOURCE [--workspace NAME|--global]\nprovider profile import -f|--file|--from PATH\nprovider profile import --url HTTPS_URL", "SOURCE may be one YAML/JSON file, a directory of YAML/JSON files, or an HTTPS URL. The source can also be supplied positionally."),
	"provider profile update":    newLeafHelp("update an imported provider profile", "provider profile update SOURCE [--workspace NAME|--global]\nprovider profile update -f|--file|--from PATH\nprovider profile update --url HTTPS_URL", "The profile must include the current resource_version; export the profile first and preserve that version."),
	"provider profile lint":      newLeafHelp("validate local or HTTPS provider profiles", "provider profile lint SOURCE\nprovider profile lint -f|--file|--from PATH\nprovider profile lint --url HTTPS_URL", "SOURCE may be one YAML/JSON file, a directory of YAML/JSON files, or an HTTPS URL."),
	"provider profile delete":    newLeafHelp("delete a provider profile", "provider profile delete ID [--workspace NAME|--global]"),
}
