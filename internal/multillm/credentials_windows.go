//go:build windows

package multillm

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type dataBlob struct {
	size uint32
	data *byte
}

var cryptUnprotectData = windows.NewLazySystemDLL("crypt32.dll").NewProc("CryptUnprotectData")

func credentialFilePermissionsSafe(os.FileInfo) bool { return true }

func decodeCredentialFile(path string, raw []byte) (string, error) {
	if !strings.HasSuffix(strings.ToLower(path), ".dpapi") {
		return string(raw), nil
	}
	encoded := strings.TrimSpace(string(raw))
	ciphertext, err := hex.DecodeString(encoded)
	if err != nil || len(ciphertext) == 0 {
		return "", errors.New("invalid DPAPI credential")
	}
	in := dataBlob{size: uint32(len(ciphertext)), data: &ciphertext[0]}
	var out dataBlob
	result, _, callErr := cryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if result == 0 {
		return "", callErr
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.data))))
	if out.data == nil || out.size == 0 {
		return "", errors.New("DPAPI returned an empty credential")
	}
	plaintext := unsafe.Slice(out.data, out.size)
	if len(plaintext)%2 == 0 && len(plaintext) >= 2 && plaintext[1] == 0 {
		units := make([]uint16, len(plaintext)/2)
		for index := range units {
			units[index] = binary.LittleEndian.Uint16(plaintext[index*2:])
		}
		return strings.TrimRight(string(utf16.Decode(units)), "\x00"), nil
	}
	return string(append([]byte(nil), plaintext...)), nil
}
