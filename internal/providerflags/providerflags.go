// Package providerflags parses provider CLI flags.
package providerflags

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// CreateArgs is the normalized provider create request.
type CreateArgs struct {
	Name          string
	Profile       string // --type
	GlobalProfile bool
	FromExisting  bool
	EnvVars       []string
	Credentials   map[string]string

	FromOIDCToken       bool
	FromGCloudADC       bool
	RuntimeCredentials  bool
	Config              map[string]string // --config KEY=VALUE
	CredentialExpiresAt map[string]int64  // KEY → epoch ms
}

// ParseCreate accepts the explicit provider creation form:
//
//	quarshell provider create --name NAME --type PROFILE [--from-existing|--credential KEY]
func ParseCreate(args []string, lookupEnv func(string) (string, bool)) (CreateArgs, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	out := CreateArgs{Credentials: map[string]string{}, Config: map[string]string{}}
	seenName, seenProfile := false, false
	seenGlobalProfile := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if seenName {
				return CreateArgs{}, fmt.Errorf("--name may be specified only once")
			}
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" || strings.HasPrefix(args[i], "-") {
				return CreateArgs{}, fmt.Errorf("--name needs a value")
			}
			out.Name, seenName = args[i], true
		case "--type":
			if seenProfile {
				return CreateArgs{}, fmt.Errorf("--type may be specified only once")
			}
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" || strings.HasPrefix(args[i], "-") {
				return CreateArgs{}, fmt.Errorf("--type needs a value")
			}
			out.Profile, seenProfile = args[i], true
		case "--from-existing":
			out.FromExisting = true
		case "--global-profile":
			if seenGlobalProfile {
				return CreateArgs{}, fmt.Errorf("--global-profile may be specified only once")
			}
			out.GlobalProfile, seenGlobalProfile = true, true
		case "--from-oidc-token":
			out.FromOIDCToken = true
		case "--from-gcloud-adc":
			out.FromGCloudADC = true
		case "--runtime-credentials":
			out.RuntimeCredentials = true
		case "--config":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--config needs KEY=VALUE")
			}
			k, v, ok := strings.Cut(args[i], "=")
			if !ok || strings.TrimSpace(k) == "" {
				return CreateArgs{}, fmt.Errorf("--config needs KEY=VALUE")
			}
			k = strings.TrimSpace(k)
			out.Config[k] = v
		case "--credential-expires-at":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--credential-expires-at needs KEY=TIMESTAMP")
			}
			k, ts, ok := strings.Cut(args[i], "=")
			if !ok {
				return CreateArgs{}, fmt.Errorf("--credential-expires-at needs KEY=TIMESTAMP")
			}
			k = strings.TrimSpace(k)
			if k == "" {
				return CreateArgs{}, fmt.Errorf("--credential-expires-at needs KEY=TIMESTAMP")
			}
			ms, err := parseCredentialExpiry(strings.TrimSpace(ts))
			if err != nil {
				return CreateArgs{}, err
			}
			if out.CredentialExpiresAt == nil {
				out.CredentialExpiresAt = map[string]int64{}
			}
			out.CredentialExpiresAt[k] = ms
		case "--env":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--env needs a value")
			}
			for k := range strings.SplitSeq(args[i], ",") {
				k = strings.TrimSpace(k)
				if k != "" {
					out.EnvVars = append(out.EnvVars, k)
				}
			}
		case "--credential":
			i++
			if i >= len(args) {
				return CreateArgs{}, fmt.Errorf("--credential needs KEY or KEY=VALUE")
			}
			raw := args[i]
			if k, v, ok := strings.Cut(raw, "="); ok {
				k = strings.TrimSpace(k)
				if k == "" {
					return CreateArgs{}, fmt.Errorf("--credential key cannot be empty")
				}
				out.Credentials[k] = v
				out.EnvVars = appendUnique(out.EnvVars, k)
			} else {
				key := strings.TrimSpace(raw)
				if key == "" {
					return CreateArgs{}, fmt.Errorf("--credential needs a non-empty KEY")
				}
				v, ok := lookupEnv(key)
				if !ok || strings.TrimSpace(v) == "" {
					return CreateArgs{}, fmt.Errorf("--credential %s: env var not set on host", key)
				}
				out.Credentials[key] = v
				out.EnvVars = appendUnique(out.EnvVars, key)
			}
		default:
			return CreateArgs{}, fmt.Errorf("unknown flag %q (usage: provider create --name NAME --type PROFILE …)", args[i])
		}
	}
	if out.Name == "" {
		return CreateArgs{}, fmt.Errorf("provider create: --name required")
	}
	if out.Profile == "" {
		return CreateArgs{}, fmt.Errorf("provider create: --type required")
	}
	return out, nil
}

func parseCredentialExpiry(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty timestamp")
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("timestamp must be greater than or equal to zero")
		}
		return n, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return 0, fmt.Errorf("timestamp must be epoch ms or RFC3339")
	}
	ms := t.UTC().UnixMilli()
	if ms < 0 {
		return 0, fmt.Errorf("timestamp must be greater than or equal to zero")
	}
	return ms, nil
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// RefreshConfigureArgs for provider refresh configure.
type RefreshConfigureArgs struct {
	Name               string
	CredKey            string
	Strategy           string
	Material           map[string]string
	SecretMaterialKeys []string
	ExpiresAtMS        int64
}

// ParseRefreshConfigure parses flags after `refresh configure <name>`.
func ParseRefreshConfigure(name string, args []string) (RefreshConfigureArgs, error) {
	out := RefreshConfigureArgs{Name: name, Material: map[string]string{}, Strategy: "env"}
	seenCredentialKey, seenStrategy, seenExpiry := false, false, false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--credential-key":
			if seenCredentialKey {
				return RefreshConfigureArgs{}, fmt.Errorf("--credential-key may be specified only once")
			}
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" || strings.HasPrefix(args[i], "-") {
				return RefreshConfigureArgs{}, fmt.Errorf("--credential-key needs a value")
			}
			key := strings.TrimSpace(args[i])
			if out.CredKey != "" && out.CredKey != key {
				return RefreshConfigureArgs{}, fmt.Errorf("credential key conflicts with --credential-expires-at")
			}
			out.CredKey, seenCredentialKey = key, true
		case "--strategy":
			if seenStrategy {
				return RefreshConfigureArgs{}, fmt.Errorf("--strategy may be specified only once")
			}
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" || strings.HasPrefix(args[i], "-") {
				return RefreshConfigureArgs{}, fmt.Errorf("--strategy needs a value")
			}
			out.Strategy, seenStrategy = args[i], true
		case "--material":
			i++
			if i >= len(args) {
				return RefreshConfigureArgs{}, fmt.Errorf("--material needs KEY=VALUE")
			}
			k, v, ok := strings.Cut(args[i], "=")
			k = strings.TrimSpace(k)
			if !ok || k == "" {
				return RefreshConfigureArgs{}, fmt.Errorf("--material needs KEY=VALUE")
			}
			if _, exists := out.Material[k]; exists {
				return RefreshConfigureArgs{}, fmt.Errorf("duplicate material key %q", k)
			}
			out.Material[k] = v
		case "--secret-material-env":
			i++
			if i >= len(args) {
				return RefreshConfigureArgs{}, fmt.Errorf("--secret-material-env needs KEY[=ENVVAR]")
			}
			key, envName, hasEnv := strings.Cut(args[i], "=")
			key = strings.TrimSpace(key)
			if !hasEnv {
				envName = key
			}
			envName = strings.TrimSpace(envName)
			if key == "" || envName == "" {
				return RefreshConfigureArgs{}, fmt.Errorf("--secret-material-env needs KEY[=ENVVAR]")
			}
			value, ok := os.LookupEnv(envName)
			if !ok || strings.TrimSpace(value) == "" {
				return RefreshConfigureArgs{}, fmt.Errorf("--secret-material-env %s requires local env var %q to be set to a non-empty value", key, envName)
			}
			if _, exists := out.Material[key]; exists {
				return RefreshConfigureArgs{}, fmt.Errorf("duplicate material key %q", key)
			}
			out.Material[key] = value
			if !contains(out.SecretMaterialKeys, key) {
				out.SecretMaterialKeys = append(out.SecretMaterialKeys, key)
			}
		case "--secret-material-key":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" || strings.HasPrefix(args[i], "-") {
				return RefreshConfigureArgs{}, fmt.Errorf("--secret-material-key needs a key")
			}
			key := strings.TrimSpace(args[i])
			if contains(out.SecretMaterialKeys, key) {
				return RefreshConfigureArgs{}, fmt.Errorf("secret material key %q supplied more than once", key)
			}
			out.SecretMaterialKeys = append(out.SecretMaterialKeys, key)
		case "--credential-expires-at":
			if seenExpiry {
				return RefreshConfigureArgs{}, fmt.Errorf("--credential-expires-at may be specified only once")
			}
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" {
				return RefreshConfigureArgs{}, fmt.Errorf("--credential-expires-at needs a timestamp")
			}
			ms, err := parseCredentialExpiry(args[i])
			if err != nil {
				return RefreshConfigureArgs{}, err
			}
			out.ExpiresAtMS = ms
			seenExpiry = true
		default:
			return RefreshConfigureArgs{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if out.CredKey == "" {
		return RefreshConfigureArgs{}, fmt.Errorf("--credential-key required")
	}
	if !seenStrategy {
		return RefreshConfigureArgs{}, fmt.Errorf("--strategy required")
	}
	return out, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// ParseRefreshKey parses `refresh rotate|delete <name> --credential-key K`.
func ParseRefreshKey(name string, args []string) (string, error) {
	key := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--credential-key":
			if key != "" {
				return "", fmt.Errorf("--credential-key may be specified only once")
			}
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" || strings.HasPrefix(args[i], "-") {
				return "", fmt.Errorf("--credential-key needs a value")
			}
			key = strings.TrimSpace(args[i])
		default:
			return "", fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if key == "" {
		return "", fmt.Errorf("--credential-key required")
	}
	return key, nil
}
