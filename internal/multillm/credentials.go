package multillm

import (
	"errors"
	"os"
	"strings"
)

const maxCredentialFileBytes = 64 << 10

func credentialValue(envName string) string {
	if envName == "" {
		return ""
	}
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return value
	}
	path := strings.TrimSpace(os.Getenv(envName + "_FILE"))
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxCredentialFileBytes || !credentialFilePermissionsSafe(info) {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	value, err := decodeCredentialFile(path, raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

var errUnsupportedProtectedCredential = errors.New("protected credential format is unsupported on this platform")
