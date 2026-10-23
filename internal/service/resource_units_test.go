package service

import (
	"math"
	"testing"
)

func TestParseCPUQuantity(t *testing.T) {
	for input, want := range map[string]float64{"500m": 0.5, "2": 2, "2.5": 2.5} {
		got, err := ParseCPUQuantity(input)
		if err != nil || math.Abs(got-want) > 1e-9 {
			t.Errorf("ParseCPUQuantity(%q)=(%v,%v), want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0", "-1", "1cpu", "NaN", "+Inf", "0m"} {
		if _, err := ParseCPUQuantity(input); err == nil {
			t.Errorf("ParseCPUQuantity(%q) unexpectedly succeeded", input)
		}
	}
}

func TestParseMemoryQuantity(t *testing.T) {
	for input, want := range map[string]int64{"4Gi": 4 << 30, "512Mi": 512 << 20, "1.5Gi": 3 << 29, "2g": 2 << 30} {
		got, err := ParseMemoryQuantity(input)
		if err != nil || got != want {
			t.Errorf("ParseMemoryQuantity(%q)=(%d,%v), want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"", "0Gi", "-1Gi", "invalid", "999999999999Ei"} {
		if _, err := ParseMemoryQuantity(input); err == nil {
			t.Errorf("ParseMemoryQuantity(%q) unexpectedly succeeded", input)
		}
	}
}

func TestValidateTemplateCreateConflictAndResources(t *testing.T) {
	if err := validateTemplateCreateConflict(SandboxCreateOpts{Template: "gpu", CPU: 1}); err == nil {
		t.Fatal("template combined with inline CPU was accepted")
	}
	if err := validateTemplateCreateConflict(SandboxCreateOpts{Template: "gpu", Policy: "policy.yaml", Labels: map[string]string{"team": "agents"}}); err != nil {
		t.Fatalf("create-time policy/labels should remain allowed: %v", err)
	}
	for _, opts := range []SandboxCreateOpts{
		{CPU: math.Inf(1)}, {Memory: "invalid"}, {GPUCount: 2},
		{DriverConfigJSON: `[]`},
	} {
		if err := validateSandboxCreateResources(opts); err == nil {
			t.Errorf("invalid resources %+v were accepted", opts)
		}
	}
}
