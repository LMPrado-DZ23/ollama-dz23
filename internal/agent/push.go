package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type PushSubscription struct {
	ID             string     `json:"id"`
	Token          string     `json:"token"`
	Platform       string     `json:"platform"`
	UserID         string     `json:"user_id"`
	OrganizationID string     `json:"organization_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
}

type PushService struct {
	mu       sync.Mutex
	root     string
	endpoint string
	client   *http.Client
	items    map[string]PushSubscription
}

func NewPushService(root, endpoint string) (*PushService, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, nil
	}
	u := strings.ToLower(endpoint)
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://127.0.0.1") && !strings.HasPrefix(u, "http://localhost") {
		return nil, errors.New("push endpoint must use HTTPS or loopback HTTP")
	}
	service := &PushService{root: strings.TrimSpace(root), endpoint: endpoint, client: &http.Client{Timeout: 15 * time.Second}, items: map[string]PushSubscription{}}
	if service.root != "" {
		if err := os.MkdirAll(service.root, 0o700); err != nil {
			return nil, err
		}
		if err := readJSON(filepath.Join(service.root, "subscriptions.json"), &service.items); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return service, nil
}

func (p *PushService) Register(token, platform, userID, organizationID string) (PushSubscription, error) {
	token = strings.TrimSpace(token)
	platform = strings.ToLower(strings.TrimSpace(platform))
	if token == "" || len(token) > 1024 || organizationID == "" {
		return PushSubscription{}, errors.New("push token and organization are required")
	}
	if platform != "android" && platform != "ios" {
		return PushSubscription{}, errors.New("push platform must be android or ios")
	}
	now := time.Now().UTC()
	id := "push_" + hashSecret(token)[:24]
	item := PushSubscription{ID: id, Token: token, Platform: platform, UserID: userID, OrganizationID: organizationID, CreatedAt: now, UpdatedAt: now}
	p.mu.Lock()
	p.items[id] = item
	err := p.persistLocked()
	p.mu.Unlock()
	return item, err
}

func (p *PushService) ListOrganization(organizationID string) []PushSubscription {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := make([]PushSubscription, 0)
	for _, item := range p.items {
		if item.OrganizationID == organizationID && item.RevokedAt == nil {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.Before(items[j].UpdatedAt) })
	return items
}

func (p *PushService) NotifyOrganization(ctx context.Context, organizationID, title, body string, data map[string]any) error {
	if p == nil || strings.TrimSpace(p.endpoint) == "" {
		return nil
	}
	items := p.ListOrganization(organizationID)
	for _, item := range items {
		payload := map[string]any{"to": item.Token, "title": title, "body": body, "data": data, "sound": "default"}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := p.client.Do(request)
		if err != nil {
			return err
		}
		bodyBytes, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return readErr
		}
		if response.StatusCode/100 != 2 {
			return fmt.Errorf("push provider returned status %d: %s", response.StatusCode, limitError(string(bodyBytes), 500))
		}
	}
	return nil
}

func (p *PushService) persistLocked() error {
	if p.root == "" {
		return nil
	}
	return writeJSONAtomic(filepath.Join(p.root, "subscriptions.json"), p.items)
}
