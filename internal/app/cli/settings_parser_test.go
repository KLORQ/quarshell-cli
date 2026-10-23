package cli

import (
	"strings"
	"testing"

	"github.com/KLORQ/quarshell-cli/internal/osargs"
)

func TestSettingsGetRejectsExtraArguments(t *testing.T) {
	err := runSettings(nil, []string{"get", "theme", "editor"})
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "editor"`) {
		t.Fatalf("error=%v, want extra setting name to be rejected", err)
	}
}

func TestSettingsDeleteRequiresAKey(t *testing.T) {
	if err := runSettings(nil, []string{"delete", "theme"}); err == nil || !strings.Contains(err.Error(), "--key KEY") {
		t.Fatalf("settings delete error=%v; want key usage", err)
	}
}

func TestSettingsGlobalMutationRequiresExplicitConfirmation(t *testing.T) {
	parsed, err := osargs.ParseSettingsSet([]string{"--global", "--key", "ocsf_schema_version", "--value", "", "--yes"})
	if err != nil || parsed.Value != "" {
		t.Fatalf("empty registered setting value parsed=%+v err=%v", parsed, err)
	}
	if err := runSettings(nil, []string{"set", "--global", "--key", "ocsf_schema_version", "--value", ""}); err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("global setting confirmation error=%v; want --yes requirement", err)
	}
	if err := runSettings(nil, []string{"delete", "--global", "--key", "ocsf_schema_version"}); err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("global delete confirmation error=%v; want --yes requirement", err)
	}
}

func TestSettingsGlobalGetRejectsSandbox(t *testing.T) {
	if err := runSettings(nil, []string{"get", "demo", "--global"}); err == nil || !strings.Contains(err.Error(), "does not accept a sandbox name") {
		t.Fatalf("global get error=%v; want sandbox rejection", err)
	}
}

func TestWorkspaceAndServiceCommandsRejectExtraArguments(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "workspace get", run: func() error { return runWorkspace(nil, []string{"get", "team-a", "extra"}) }, want: `unexpected argument "extra"`},
		{name: "workspace member add", run: func() error { return runWorkspace(nil, []string{"member", "add", "team-a", "alice", "extra"}) }, want: `unexpected argument "extra"`},
		{name: "service get", run: func() error { return runService(nil, []string{"get", "sandbox", "api", "extra"}) }, want: `unexpected argument "extra"`},
		{name: "service delete", run: func() error { return runService(nil, []string{"delete", "sandbox", "api", "extra"}) }, want: `unexpected argument "extra"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want substring %q", err, test.want)
			}
		})
	}
}

func TestWorkspaceDeleteAcceptsMultipleNames(t *testing.T) {
	names, err := parseWorkspaceDeleteNames([]string{"team-a", "team-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "team-a" || names[1] != "team-b" {
		t.Fatalf("names=%q, want both workspace names", names)
	}
}

func TestWorkspaceDeleteRejectsMissingOrFlagLikeNames(t *testing.T) {
	for _, args := range [][]string{nil, {""}, {"--unknown"}} {
		if _, err := parseWorkspaceDeleteNames(args); err == nil {
			t.Errorf("parseWorkspaceDeleteNames(%q) unexpectedly succeeded", args)
		}
	}
}

func TestParseWorkspaceCreateArgsParsesRepeatedLabels(t *testing.T) {
	name, labels, err := parseWorkspaceCreateArgs([]string{
		"--name=team-a", "--label", "env=dev", "--label=owner=agents", "--label", "env=staging", "--label", "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if name != "team-a" {
		t.Fatalf("name=%q, want team-a", name)
	}
	if len(labels) != 2 || labels["env"] != "staging" || labels["owner"] != "agents" {
		t.Fatalf("labels=%v, want last value per valid KEY=VALUE", labels)
	}
}

func TestParseWorkspaceCreateArgsRejectsDuplicateOrMissingName(t *testing.T) {
	for _, args := range [][]string{
		{"--name", "one", "--name", "two"},
		{"--name", "--label", "env=test"},
		{"--label", "env=test"},
	} {
		if _, _, err := parseWorkspaceCreateArgs(args); err == nil {
			t.Errorf("parseWorkspaceCreateArgs(%q) unexpectedly succeeded", args)
		}
	}
}

func TestParseWorkspaceListArgsSupportsPinnedPaginationFlags(t *testing.T) {
	options, err := parseWorkspaceListArgs([]string{
		"--page-size=25", "--page-token", "cursor-1", "--label-selector=env=staging", "-o", "json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.PageSize != 25 || options.PageToken != "cursor-1" || options.LabelSelector != "env=staging" || options.Output != "json" {
		t.Fatalf("options=%+v", options)
	}
	yamlOptions, err := parseWorkspaceListArgs([]string{"--output=yaml"})
	if err != nil || yamlOptions.Output != "yaml" {
		t.Fatalf("yaml options=%+v, error=%v", yamlOptions, err)
	}
	defaults, err := parseWorkspaceListArgs(nil)
	if err != nil || defaults.PageSize != 100 {
		t.Fatalf("default options=%+v, error=%v", defaults, err)
	}
}

func TestParseWorkspaceListArgsRejectsMalformedAndDuplicateFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--page-size", "0"},
		{"--page-size", "2147483648"},
		{"--page-size", "10", "--page-size", "20"},
		{"--page-token"},
		{"--output", "xml"},
		{"unexpected"},
	} {
		if _, err := parseWorkspaceListArgs(args); err == nil {
			t.Errorf("parseWorkspaceListArgs(%q) unexpectedly succeeded", args)
		}
	}
}
