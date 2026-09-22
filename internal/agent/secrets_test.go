package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretStoreEncryptsAtRestAndListsMetadata(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_CREDENTIAL_KEY", "test-only-credential-key")
	root := t.TempDir()
	store, err := NewSecretStore(filepath.Join(root, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("github_token", "super-secret-value"); err != nil {
		t.Fatal(err)
	}
	value, err := store.Get("github_token")
	if err != nil || value != "super-secret-value" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "secrets", "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "super-secret-value") {
		t.Fatal("plaintext secret was persisted")
	}
	if len(store.List()) != 1 || store.List()[0].Name != "github_token" {
		t.Fatalf("unexpected metadata: %+v", store.List())
	}
}

func TestDLPRedactsKnownCredentialShapes(t *testing.T) {
	input := "Authorization: Bearer abcdefghijklmnop1234 and key ghp_abcdefghijklmnopqrstuvwxyz123456"
	findings := ScanDLP(input)
	if len(findings) < 2 {
		t.Fatalf("expected at least two DLP findings, got %+v", findings)
	}
	redacted := RedactDLP(input)
	if strings.Contains(redacted, "abcdefghijklmnop1234") || strings.Contains(redacted, "ghp_abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("DLP did not redact: %s", redacted)
	}
}
