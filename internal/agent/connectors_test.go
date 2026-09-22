package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConnectorUsesTenantOAuthCredential(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_CREDENTIAL_KEY", "connector-test-key")
	store, err := NewAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("connector@example.com", "Connector User")
	if err != nil {
		t.Fatal(err)
	}
	organization, _, err := store.CreateOrganization("Connector Org", user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(user.ID, organization.ID, "write"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StoreOAuthCredential("github", user.ID, organization.ID, map[string]any{"access_token": "tenant-token", "expires_in": float64(3600)}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	manager := NewConnectorManager()
	manager.client = server.Client()
	manager.SetOAuthStore(store)
	if err := manager.Register(ConnectorConfig{ID: "github", Provider: "github", BaseURL: server.URL, OAuthProvider: "github", Operations: []ConnectorOperation{{Name: "profile", Methods: []string{"GET"}, PathPrefixes: []string{"/user"}}}}); err != nil {
		t.Fatal(err)
	}
	status, response, err := manager.CallForOrganization(context.Background(), organization.ID, "github", "profile", "GET", "/user", nil)
	if err != nil || status != http.StatusOK || response != `{"ok":true}` {
		t.Fatalf("status=%d response=%q err=%v", status, response, err)
	}
}
