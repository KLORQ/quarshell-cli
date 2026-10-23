package service

import "testing"

func TestParseOpenShellSettingValue(t *testing.T) {
	tests := []struct {
		key, raw, want string
		wantErr        bool
	}{
		{"ocsf_json_enabled", "yes", "true", false},
		{"agent_policy_proposals_enabled", "0", "false", false},
		{"ocsf_schema_version", "1.3", "1.3", false},
		{"proposal_approval_mode", "auto", "auto", false},
		{"ocsf_json_enabled", "perhaps", "", true},
		{"proposal_approval_mode", "autom", "", true},
		{"not_registered", "value", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.key+"/"+tc.raw, func(t *testing.T) {
			value, err := ParseOpenShellSettingValue(tc.key, tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%t", err, tc.wantErr)
			}
			if err == nil && settingValueString(value) != tc.want {
				t.Fatalf("value=%q want %q", settingValueString(value), tc.want)
			}
		})
	}
}
