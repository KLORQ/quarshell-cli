package service

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	sandboxv1 "github.com/KLORQ/quarshell-proto/gen/go/openshell/sandbox/v1"
	"golang.org/x/term"
)

func ParseOpenShellSettingValue(key, raw string) (*sandboxv1.SettingValue, error) {
	return parseOpenShellSettingValue(key, raw)
}

// SettingsGet displays effective sandbox settings or Gateway-global settings.
func (a *App) SettingsGet(name string, global, asJSON bool) error {
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("settings get requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if global {
		config, err := c.GetGatewaySettings(ctx)
		if err != nil {
			return err
		}
		settings := make(map[string]any, len(config.GetSettings()))
		for key, value := range config.GetSettings() {
			settings[key] = settingValueJSON(value)
		}
		data := map[string]any{"scope": "global", "settings_revision": config.GetSettingsRevision(), "settings": settings}
		if asJSON {
			return printSettingsJSON(data)
		}
		fmt.Println("Scope:         global")
		fmt.Printf("Settings Rev:  %d\n", config.GetSettingsRevision())
		printSettingValues(settings)
		return nil
	}
	sandbox, err := a.resolveSandboxTarget(name)
	if err != nil {
		return err
	}
	config, err := c.GetSandboxSettings(ctx, a.workspaceName(), sandbox)
	if err != nil {
		return err
	}
	settings := make(map[string]any, len(config.GetSettings()))
	for key, item := range config.GetSettings() {
		settings[key] = map[string]any{"value": settingValueJSON(item.GetValue()), "scope": settingScopeName(item.GetScope())}
	}
	policySource := "sandbox"
	if config.GetPolicySource() == sandboxv1.PolicySource_POLICY_SOURCE_GLOBAL {
		policySource = "global"
	}
	if asJSON {
		return printSettingsJSON(map[string]any{"sandbox": sandbox, "workspace": a.workspaceName(), "config_revision": config.GetConfigRevision(), "policy_source": policySource, "policy_hash": config.GetPolicyHash(), "settings": settings})
	}
	fmt.Printf("Sandbox:       %s\nConfig Rev:    %d\nPolicy Source: %s\nPolicy Hash:   %s\n", sandbox, config.GetConfigRevision(), policySource, config.GetPolicyHash())
	if len(settings) == 0 {
		fmt.Println("Settings:      No settings available.")
		return nil
	}
	fmt.Println("Settings:")
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := config.GetSettings()[key]
		fmt.Printf("  %s = %s (%s)\n", key, settingValueString(item.GetValue()), settingScopeName(item.GetScope()))
	}
	return nil
}

func (a *App) SettingsSet(name, key, raw string, global, yes bool) error {
	if global {
		if err := confirmGlobalSettingMutation("update", key, yes); err != nil {
			return err
		}
	}
	value, err := parseOpenShellSettingValue(key, raw)
	if err != nil {
		return err
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("settings set requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	if global {
		err = c.PutGatewaySettingValue(ctx, key, value)
	} else {
		var sandbox string
		sandbox, err = a.resolveSandboxTarget(name)
		if err == nil {
			err = c.PutSandboxSettingValue(ctx, a.workspaceName(), sandbox, key, value)
		}
		name = sandbox
	}
	if err != nil {
		return err
	}
	scope := "sandbox"
	if global {
		scope = "global"
	}
	fmt.Printf("set %s setting %s=%s", scope, key, raw)
	if !global {
		fmt.Printf(" for %s", name)
	}
	fmt.Println()
	return nil
}

func (a *App) SettingsDelete(name, key string, global, yes bool) error {
	if global {
		if err := confirmGlobalSettingMutation("delete", key, yes); err != nil {
			return err
		}
	}
	c, err := a.gatewayClient()
	if err != nil {
		return fmt.Errorf("settings delete requires a configured Gateway: %w", err)
	}
	ctx, cancel := a.withTimeout(TimeoutAPI)
	defer cancel()
	var responseDeleted bool
	if global {
		response, deleteErr := c.DeleteGatewaySetting(ctx, key)
		err = deleteErr
		if response != nil {
			responseDeleted = response.GetDeleted()
		}
	} else {
		var sandbox string
		sandbox, err = a.resolveSandboxTarget(name)
		if err == nil {
			response, deleteErr := c.DeleteSandboxSetting(ctx, a.workspaceName(), sandbox, key)
			err = deleteErr
			if response != nil {
				responseDeleted = response.GetDeleted()
			}
		}
		name = sandbox
	}
	if err != nil {
		return err
	}
	scope := "sandbox"
	if global {
		scope = "global"
	}
	if responseDeleted {
		fmt.Printf("deleted %s setting %s", scope, key)
	} else {
		fmt.Printf("%s setting %s not found", scope, key)
	}
	if !global {
		fmt.Printf(" for %s", name)
	}
	fmt.Println()
	return nil
}

func confirmGlobalSettingMutation(action, key string, yes bool) error {
	if yes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("global setting %s requires confirmation; pass --yes in non-interactive mode", action)
	}
	fmt.Fprintf(os.Stderr, "Confirm %s of Gateway-global setting %q? [y/N] ", action, key)
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read setting confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("global setting %s cancelled", action)
	}
}

func parseOpenShellSettingValue(key, raw string) (*sandboxv1.SettingValue, error) {
	stringValue := func(value string) *sandboxv1.SettingValue {
		return &sandboxv1.SettingValue{Value: &sandboxv1.SettingValue_StringValue{StringValue: value}}
	}
	switch key {
	case "ocsf_json_enabled", "agent_policy_proposals_enabled":
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "true", "yes", "1":
			return &sandboxv1.SettingValue{Value: &sandboxv1.SettingValue_BoolValue{BoolValue: true}}, nil
		case "false", "no", "0":
			return &sandboxv1.SettingValue{Value: &sandboxv1.SettingValue_BoolValue{BoolValue: false}}, nil
		default:
			return nil, fmt.Errorf("invalid bool value %q for key %q; expected true,false,yes,no,1,0", raw, key)
		}
	case "ocsf_schema_version":
		if raw != "" && raw != "1.1" && raw != "1.3" {
			return nil, fmt.Errorf("invalid value %q for key %q; expected one of: , 1.1, 1.3", raw, key)
		}
		return stringValue(raw), nil
	case "proposal_approval_mode":
		if raw != "manual" && raw != "auto" {
			return nil, fmt.Errorf("invalid value %q for key %q; expected one of: manual, auto", raw, key)
		}
		return stringValue(raw), nil
	default:
		return nil, fmt.Errorf("unknown setting key %q", key)
	}
}

func settingValueString(value *sandboxv1.SettingValue) string {
	if value == nil {
		return ""
	}
	switch item := value.GetValue().(type) {
	case *sandboxv1.SettingValue_StringValue:
		return item.StringValue
	case *sandboxv1.SettingValue_BoolValue:
		return strconv.FormatBool(item.BoolValue)
	case *sandboxv1.SettingValue_IntValue:
		return strconv.FormatInt(item.IntValue, 10)
	case *sandboxv1.SettingValue_BytesValue:
		return base64.StdEncoding.EncodeToString(item.BytesValue)
	default:
		return ""
	}
}

func settingValueJSON(value *sandboxv1.SettingValue) any {
	if value == nil {
		return nil
	}
	switch item := value.GetValue().(type) {
	case *sandboxv1.SettingValue_StringValue:
		return item.StringValue
	case *sandboxv1.SettingValue_BoolValue:
		return item.BoolValue
	case *sandboxv1.SettingValue_IntValue:
		return item.IntValue
	case *sandboxv1.SettingValue_BytesValue:
		return base64.StdEncoding.EncodeToString(item.BytesValue)
	default:
		return nil
	}
}

func settingScopeName(scope sandboxv1.SettingScope) string {
	if scope == sandboxv1.SettingScope_SETTING_SCOPE_GLOBAL {
		return "global"
	}
	if scope == sandboxv1.SettingScope_SETTING_SCOPE_SANDBOX {
		return "sandbox"
	}
	return "unset"
}

func printSettingsJSON(value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(b))
	return err
}

func printSettingValues(settings map[string]any) {
	if len(settings) == 0 {
		fmt.Println("Settings:      No settings available.")
		return
	}
	fmt.Println("Settings:")
	keys := make([]string, 0, len(settings))
	for key := range settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		encoded, _ := json.Marshal(settings[key])
		fmt.Printf("  %s = %s\n", key, encoded)
	}
}
