package service

import "testing"

func TestGatewaySecretSnapshotDoesNotRestoreRevokedEnvironmentSecret(t *testing.T) {
	environment := map[string]string{"API_TOKEN": "revoked-value", "LOCAL_TOKEN": "local-value"}
	managed := map[string]struct{}{"API_TOKEN": {}}
	snapshot := gatewayProxySecretSnapshot(environment, map[string]string{}, managed)
	if _, ok := snapshot["API_TOKEN"]; ok {
		t.Fatal("empty gateway snapshot restored a credential from the process environment")
	}
	if snapshot["LOCAL_TOKEN"] != "local-value" {
		t.Fatal("environment fallback for a never-managed local key was lost")
	}
}

func TestGatewaySecretSnapshotMirrorsGitHubAliasesFromCurrentGrant(t *testing.T) {
	snapshot := gatewayProxySecretSnapshot(nil, map[string]string{"GITHUB_TOKEN": "current-value"}, map[string]struct{}{"GITHUB_TOKEN": {}, "GH_TOKEN": {}})
	if snapshot["GH_TOKEN"] != "current-value" {
		t.Fatalf("GH_TOKEN alias=%q, want current gateway grant", snapshot["GH_TOKEN"])
	}
}
