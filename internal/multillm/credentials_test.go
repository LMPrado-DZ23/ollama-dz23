//go:build !windows

package multillm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialValueSupportsProtectedFileConvention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.key")
	if err := os.WriteFile(path, []byte("secret-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_PROVIDER_KEY", "")
	t.Setenv("TEST_PROVIDER_KEY_FILE", path)
	if got := credentialValue("TEST_PROVIDER_KEY"); got != "secret-from-file" {
		t.Fatalf("credential = %q", got)
	}
}

func TestCredentialValueRejectsBroadFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.key")
	if err := os.WriteFile(path, []byte("unsafe"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_PROVIDER_KEY", "")
	t.Setenv("TEST_PROVIDER_KEY_FILE", path)
	if got := credentialValue("TEST_PROVIDER_KEY"); got != "" {
		t.Fatalf("unsafe credential was loaded: %q", got)
	}
}
