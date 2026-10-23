package service

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseSandboxLabelSelector(t *testing.T) {
	got, err := parseSandboxLabelSelector("team=ml, tier = prod,")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"team": "ml", "tier": "prod"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selector = %#v, want %#v", got, want)
	}
	for _, input := range []string{
		"team", "=ml", "team=-prod", "-team=prod", "app@name=x", "example.com./app=x",
		strings.Repeat("a", 64) + "=x", strings.Repeat("a", 254) + "=x",
		"/app=x", "a/b/c=x", "app=value-",
		"app=" + strings.Repeat("v", 64),
	} {
		if _, err := parseSandboxLabelSelector(input); err == nil {
			t.Errorf("parseSandboxLabelSelector(%q) succeeded, want error", input)
		}
	}
	for _, input := range []string{"", "  ", "env=", "env=,team=platform", "team=ml,,tier=prod", "env=prod,", "kubernetes.io/app=web"} {
		if _, err := parseSandboxLabelSelector(input); err != nil {
			t.Errorf("parseSandboxLabelSelector(%q) error = %v, want success", input, err)
		}
	}
	got, err = parseSandboxLabelSelector("team=ml,team=ops")
	if err != nil || got["team"] != "ops" {
		t.Fatalf("duplicate-key selector result = %#v, error %v; want last value", got, err)
	}
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = "k" + strconv.Itoa(i) + "=v"
	}
	if _, err := parseSandboxLabelSelector(strings.Join(tooMany, ",")); err == nil {
		t.Fatal("selector with 65 pairs succeeded, want upstream 64-pair limit")
	}
	maxPairs := make([]string, 64)
	for i := range maxPairs {
		maxPairs[i] = "k" + strconv.Itoa(i) + "=v"
	}
	if _, err := parseSandboxLabelSelector(strings.Join(maxPairs, ",")); err != nil {
		t.Fatalf("selector with 64 pairs error = %v, want success", err)
	}
}
