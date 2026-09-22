//go:build !windows

package multillm

import (
	"os"
	"strings"
)

func credentialFilePermissionsSafe(info os.FileInfo) bool {
	return info.Mode().Perm()&0o077 == 0
}

func decodeCredentialFile(path string, raw []byte) (string, error) {
	if strings.HasSuffix(strings.ToLower(path), ".dpapi") {
		return "", errUnsupportedProtectedCredential
	}
	return string(raw), nil
}
