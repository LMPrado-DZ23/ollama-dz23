//go:build windows

package multillm

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialValueDecryptsPowerShellDPAPIFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.dpapi")
	script := fmt.Sprintf(`ConvertTo-SecureString 'dpapi-round-trip' -AsPlainText -Force | ConvertFrom-SecureString | Set-Content -Encoding ASCII -NoNewline -LiteralPath '%s'`, strings.ReplaceAll(path, "'", "''"))
	command := exec.Command("powershell.exe", "-NoProfile", "-Command",
		script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create DPAPI fixture: %v: %s", err, output)
	}
	t.Setenv("TEST_DPAPI_KEY", "")
	t.Setenv("TEST_DPAPI_KEY_FILE", path)
	if got := credentialValue("TEST_DPAPI_KEY"); got != "dpapi-round-trip" {
		t.Fatalf("credential = %q", got)
	}
}

func TestDesktopCanReplaceLegacyConfiguratorCredential(t *testing.T) {
	desktopFixture(t)
	root := t.TempDir()
	t.Setenv("LOCALAPPDATA", root)
	path := filepath.Join(root, "Ollama DZ23", "secrets", "DZ23_TEST_API_KEY.dpapi")
	t.Setenv("DZ23_TEST_API_KEY_FILE", path)
	if credentialManagedExternally("DZ23_TEST_API_KEY") {
		t.Fatal("legacy DZ23 key is not editable")
	}
	if err := saveManagedCredential("DZ23_TEST_API_KEY", "replacement-legacy-secret"); err != nil {
		t.Fatal(err)
	}
	if credentialValue("DZ23_TEST_API_KEY") != "replacement-legacy-secret" {
		t.Fatal("legacy key not readable")
	}
	t.Setenv("DZ23_TEST_API_KEY_FILE", filepath.Join(root, "unrelated.dpapi"))
	if !credentialManagedExternally("DZ23_TEST_API_KEY") {
		t.Fatal("arbitrary external file became editable")
	}
}
