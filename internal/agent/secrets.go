package agent

import (
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type SecretMetadata struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SecretStore struct {
	mu     sync.RWMutex
	root   string
	values map[string]SecretValue
}

type SecretValue struct {
	Ciphertext string    `json:"ciphertext"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func NewSecretStore(root string) (*SecretStore, error) {
	store := &SecretStore{root: strings.TrimSpace(root), values: map[string]SecretValue{}}
	if store.root != "" {
		if err := os.MkdirAll(store.root, 0o700); err != nil {
			return nil, err
		}
		if err := readJSON(filepathJoin(store.root, "secrets.json"), &store.values); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return store, nil
}

func (s *SecretStore) Set(name, value string) error {
	name = normalizeSecretName(name)
	if name == "" || strings.TrimSpace(value) == "" {
		return errors.New("secret name and value are required")
	}
	ciphertext, err := encryptCredential(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.values[name] = SecretValue{Ciphertext: ciphertext, UpdatedAt: time.Now().UTC()}
	err = s.persistLocked()
	s.mu.Unlock()
	return err
}

func (s *SecretStore) Get(name string) (string, error) {
	name = normalizeSecretName(name)
	s.mu.RLock()
	value, ok := s.values[name]
	s.mu.RUnlock()
	if !ok {
		return "", os.ErrNotExist
	}
	return decryptCredential(value.Ciphertext)
}

func (s *SecretStore) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = normalizeSecretName(name)
	if _, ok := s.values[name]; !ok {
		return os.ErrNotExist
	}
	delete(s.values, name)
	return s.persistLocked()
}

func (s *SecretStore) List() []SecretMetadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]SecretMetadata, 0, len(s.values))
	for name, value := range s.values {
		items = append(items, SecretMetadata{Name: name, UpdatedAt: value.UpdatedAt})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *SecretStore) persistLocked() error {
	if s.root == "" {
		return nil
	}
	return writeJSONAtomic(filepathJoin(s.root, "secrets.json"), s.values)
}

func normalizeSecretName(name string) string {
	name = strings.TrimSpace(name)
	for _, character := range name {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return ""
		}
	}
	return name
}

type DLPFinding struct {
	Kind     string `json:"kind"`
	Redacted string `json:"redacted"`
}

var dlpPatterns = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"private_key", regexp.MustCompile(`-----BEGIN [A-Z ]+PRIVATE KEY-----`)},
	{"github_token", regexp.MustCompile(`(?i)\b(?:ghp|github_pat)_[A-Za-z0-9_]{20,}\b`)},
	{"openai_token", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"bearer_token", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`)},
}

func ScanDLP(text string) []DLPFinding {
	findings := []DLPFinding{}
	for _, item := range dlpPatterns {
		if match := item.pattern.FindString(text); match != "" {
			findings = append(findings, DLPFinding{Kind: item.kind, Redacted: item.pattern.ReplaceAllString(match, "[REDACTED]")})
		}
	}
	return findings
}

func RedactDLP(text string) string {
	for _, item := range dlpPatterns {
		text = item.pattern.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}
