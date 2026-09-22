package agent

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type DeviceStatus string

const (
	DeviceOnline  DeviceStatus = "online"
	DeviceOffline DeviceStatus = "offline"
	DeviceRevoked DeviceStatus = "revoked"
)

type DeviceCapability struct {
	Name     string            `json:"name"`
	Version  string            `json:"version,omitempty"`
	Scopes   []string          `json:"scopes,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}
type Device struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	Platform       string             `json:"platform"`
	UserID         string             `json:"user_id,omitempty"`
	OrganizationID string             `json:"organization_id,omitempty"`
	Capabilities   []DeviceCapability `json:"capabilities"`
	Status         DeviceStatus       `json:"status"`
	LastSeen       time.Time          `json:"last_seen"`
	CreatedAt      time.Time          `json:"created_at"`
	RevokedAt      *time.Time         `json:"revoked_at,omitempty"`
}
type PairingRequest struct {
	CodeHash       string     `json:"code_hash"`
	UserID         string     `json:"user_id,omitempty"`
	OrganizationID string     `json:"organization_id,omitempty"`
	ExpiresAt      time.Time  `json:"expires_at"`
	UsedAt         *time.Time `json:"used_at,omitempty"`
}
type DeviceStore struct {
	mu       sync.Mutex
	root     string
	devices  map[string]Device
	tokens   map[string]string
	pairings map[string]PairingRequest
}

func NewDeviceStore(root string) (*DeviceStore, error) {
	store := &DeviceStore{root: root, devices: map[string]Device{}, tokens: map[string]string{}, pairings: map[string]PairingRequest{}}
	if strings.TrimSpace(root) == "" {
		return store, nil
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	for name, target := range map[string]any{"devices": &store.devices, "device-tokens": &store.tokens, "pairings": &store.pairings} {
		if err := readJSON(filepath.Join(root, name+".json"), target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return store, nil
}
func (s *DeviceStore) StartPairing(userID, organizationID string, ttl time.Duration) (string, PairingRequest, error) {
	if ttl <= 0 || ttl > 15*time.Minute {
		ttl = 5 * time.Minute
	}
	raw, err := randomDeviceSecret(8)
	if err != nil {
		return "", PairingRequest{}, err
	}
	pairing := PairingRequest{CodeHash: hashDeviceSecret(raw), UserID: userID, OrganizationID: organizationID, ExpiresAt: time.Now().UTC().Add(ttl)}
	s.mu.Lock()
	s.pairings[pairing.CodeHash] = pairing
	err = s.persistLocked()
	s.mu.Unlock()
	return raw, pairing, err
}
func (s *DeviceStore) CompletePairing(code, name, platform, userID, organizationID string, capabilities []DeviceCapability) (Device, string, error) {
	code = strings.TrimSpace(code)
	name = strings.TrimSpace(name)
	platform = strings.TrimSpace(platform)
	if code == "" || name == "" || platform == "" {
		return Device{}, "", errors.New("pairing code, device name and platform are required")
	}
	hash := hashDeviceSecret(code)
	s.mu.Lock()
	defer s.mu.Unlock()
	pairing, ok := s.pairings[hash]
	if !ok || pairing.UsedAt != nil || time.Now().UTC().After(pairing.ExpiresAt) {
		return Device{}, "", errors.New("invalid, expired, or already used pairing code")
	}
	now := time.Now().UTC()
	pairing.UsedAt = &now
	s.pairings[hash] = pairing
	if userID == "" {
		userID = pairing.UserID
	}
	if organizationID == "" {
		organizationID = pairing.OrganizationID
	}
	token, err := randomDeviceSecret(32)
	if err != nil {
		return Device{}, "", err
	}
	device := Device{ID: "dev_" + uuid.NewString(), Name: name, Platform: platform, UserID: userID, OrganizationID: organizationID, Capabilities: normalizeCapabilities(capabilities), Status: DeviceOnline, LastSeen: now, CreatedAt: now}
	s.devices[device.ID] = device
	s.tokens[device.ID] = hashDeviceSecret(token)
	return device, token, s.persistLocked()
}
func (s *DeviceStore) Heartbeat(deviceID, token string, capabilities []DeviceCapability) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return Device{}, os.ErrNotExist
	}
	if device.Status == DeviceRevoked || !secureCompare(s.tokens[deviceID], hashDeviceSecret(token)) {
		return Device{}, errors.New("invalid or revoked device token")
	}
	device.Status = DeviceOnline
	device.LastSeen = time.Now().UTC()
	if len(capabilities) > 0 {
		device.Capabilities = normalizeCapabilities(capabilities)
	}
	s.devices[deviceID] = device
	return device, s.persistLocked()
}
func (s *DeviceStore) Revoke(deviceID string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return Device{}, os.ErrNotExist
	}
	now := time.Now().UTC()
	device.Status = DeviceRevoked
	device.RevokedAt = &now
	s.devices[deviceID] = device
	delete(s.tokens, deviceID)
	return device, s.persistLocked()
}
func (s *DeviceStore) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Device, 0, len(s.devices))
	now := time.Now().UTC()
	for _, device := range s.devices {
		if device.Status == DeviceOnline && now.Sub(device.LastSeen) > 2*time.Minute {
			device.Status = DeviceOffline
		}
		result = append(result, device)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.Before(result[j].CreatedAt) })
	return result
}
func (s *DeviceStore) Get(deviceID string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return Device{}, os.ErrNotExist
	}
	return device, nil
}
func (s *DeviceStore) persistLocked() error {
	if s.root == "" {
		return nil
	}
	if err := writeJSONAtomic(filepath.Join(s.root, "devices.json"), s.devices); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(s.root, "device-tokens.json"), s.tokens); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(s.root, "pairings.json"), s.pairings)
}
func normalizeCapabilities(input []DeviceCapability) []DeviceCapability {
	seen := map[string]bool{}
	result := make([]DeviceCapability, 0, len(input))
	for _, capability := range input {
		name := strings.TrimSpace(capability.Name)
		if name == "" || len(name) > 128 || seen[name] {
			continue
		}
		seen[name] = true
		if len(capability.Scopes) > 32 {
			capability.Scopes = capability.Scopes[:32]
		}
		result = append(result, capability)
	}
	return result
}
func randomDeviceSecret(bytesCount int) (string, error) {
	raw := make([]byte, bytesCount)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func hashDeviceSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func secureCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
