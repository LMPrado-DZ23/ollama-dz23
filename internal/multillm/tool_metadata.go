package multillm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxToolMetadataBytes = 256 << 10
const maxToolMetadataCacheBytes = 32 << 20
const maxToolMetadataRecords = 2048

var toolMetadataMu sync.Mutex

// Opaque provider tool context must survive clients that only preserve standard
// Responses fields. It is scoped to destination, model, credential, call ID,
// function and arguments. No prompts, tool results or API keys are persisted.
// Windows records use the existing user-bound DPAPI codec.
func toolMetadataPath(p Provider, m Model, call openAIToolCall) (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	if call.ID == "" || call.Function.Name == "" {
		return "", errors.New("provider tool context requires a call ID and function name")
	}
	var arguments any
	decoder := json.NewDecoder(strings.NewReader(call.Function.Arguments))
	decoder.UseNumber()
	if decoder.Decode(&arguments) != nil {
		return "", errors.New("invalid provider tool arguments")
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	scope := []string{p.Name, p.BaseURL, m.UpstreamID, credentialValue(p.APIKeyEnv), call.ID, call.Function.Name, string(canonical)}
	encoded, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	defer clear(encoded)
	sum := sha256.Sum256(encoded)
	return filepath.Join(root, "Ollama DZ23", "tool-context", hex.EncodeToString(sum[:])+managedCredentialExtension), nil
}
func ensureToolMetadataDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe provider context directory")
	}
	return nil
}
func pruneToolMetadata(dir string, incoming int64) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	type record struct {
		path     string
		size     int64
		modified time.Time
	}
	records := []record{}
	total := int64(0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), managedCredentialExtension) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		records = append(records, record{filepath.Join(dir, entry.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(records, func(i, j int) bool { return records[i].modified.Before(records[j].modified) })
	count := len(records)
	for _, record := range records {
		if count < maxToolMetadataRecords && total+incoming <= maxToolMetadataCacheBytes && time.Since(record.modified) < 30*24*time.Hour {
			break
		}
		if err := os.Remove(record.path); err != nil {
			return err
		}
		total -= record.size
		count--
	}
	return nil
}

func (g *Gateway) rememberToolMetadata(p Provider, m Model, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var calls []openAIToolCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return err
	}
	toolMetadataMu.Lock()
	defer toolMetadataMu.Unlock()
	for _, call := range calls {
		if len(call.ExtraContent) == 0 || string(call.ExtraContent) == "null" {
			continue
		}
		if len(call.ExtraContent) > maxToolMetadataBytes/8 || !json.Valid(call.ExtraContent) {
			return errors.New("provider tool context exceeds its limit")
		}
		path, err := toolMetadataPath(p, m, call)
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		if err := ensureToolMetadataDir(dir); err != nil {
			return err
		}
		encoded, err := encodeCredentialFile(string(call.ExtraContent))
		if err != nil {
			return err
		}
		if len(encoded) > maxToolMetadataBytes {
			return errors.New("protected tool context exceeds its limit")
		}
		if err := pruneToolMetadata(dir, int64(len(encoded))); err != nil {
			return err
		}
		f, err := os.CreateTemp(dir, ".context-*")
		if err != nil {
			return err
		}
		tmp := f.Name()
		err = f.Chmod(0600)
		if err == nil {
			_, err = f.Write(encoded)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Rename(tmp, path)
		}
		if err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return nil
}

func (g *Gateway) restoreToolMetadata(p Provider, m Model, messages []map[string]any) error {
	toolMetadataMu.Lock()
	defer toolMetadataMu.Unlock()
	for _, message := range messages {
		calls, ok := message["tool_calls"].([]any)
		if !ok {
			continue
		}
		for _, item := range calls {
			callMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			encoded, err := json.Marshal(callMap)
			if err != nil {
				return err
			}
			var call openAIToolCall
			if err := json.Unmarshal(encoded, &call); err != nil {
				return err
			}
			if call.ID == "" || call.Function.Name == "" {
				continue
			}
			path, err := toolMetadataPath(p, m, call)
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > maxToolMetadataBytes || !credentialFilePermissionsSafe(info) {
				return errors.New("unsafe provider tool context")
			}
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			raw, err := io.ReadAll(io.LimitReader(file, maxToolMetadataBytes+1))
			file.Close()
			if err != nil {
				return err
			}
			if len(raw) > maxToolMetadataBytes {
				return errors.New("provider tool context exceeds its limit")
			}
			value, err := decodeCredentialFile(path, raw)
			if err != nil {
				return errors.New("could not restore protected provider tool context")
			}
			if !json.Valid([]byte(value)) {
				return errors.New("invalid provider tool context")
			}
			callMap["extra_content"] = json.RawMessage(value)
		}
	}
	return nil
}
