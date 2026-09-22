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
