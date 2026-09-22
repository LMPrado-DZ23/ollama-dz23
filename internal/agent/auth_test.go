package agent

import (
	"encoding/base32"
	"os"
	"testing"
	"time"
)

func TestAuthStorePersistsRBACAndRejectsCrossTenantAccess(t *testing.T) {
	root := t.TempDir()
	store, err := NewAuthStore(root)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("owner@example.com", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	organization, _, err := store.CreateOrganization("Org A", user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(user.ID, organization.ID, "write"); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateUser("other@example.com", "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authorize(other.ID, organization.ID, "read"); err == nil {
		t.Fatal("cross-tenant authorization unexpectedly succeeded")
	}
	raw, _, err := store.IssueToken(user.ID, organization.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, gotOrg, _, err := store.Authenticate(raw); err != nil || gotOrg.ID != organization.ID {
		t.Fatalf("authenticate = %v, org=%+v", err, gotOrg)
	}
	if err := store.RevokeToken(raw); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Authenticate(raw); err == nil {
		t.Fatal("revoked token authenticated")
	}
	reloaded, err := NewAuthStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Users()) != 2 {
		t.Fatalf("users after reload = %d", len(reloaded.Users()))
	}
}

func TestOAuthStateIsSingleUseAndPKCESafe(t *testing.T) {
	store, err := NewAuthStore("")
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := store.CreateOAuthState("github", "https://app.example.test/oauth/callback", "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeOAuthState(state, "github", "https://app.example.test/oauth/callback"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeOAuthState(state, "github", "https://app.example.test/oauth/callback"); err == nil {
		t.Fatal("oauth state was reusable")
	}
}

func TestOAuthProviderRejectsNonHTTPS(t *testing.T) {
	provider := OAuthProvider{Name: "bad", AuthorizeURL: "http://example.test/authorize", TokenURL: "https://example.test/token", ClientIDEnv: "DZ23_TEST_CLIENT", SecretEnv: "DZ23_TEST_SECRET"}
	t.Setenv("DZ23_TEST_CLIENT", "client")
	t.Setenv("DZ23_TEST_SECRET", "secret")
	if err := provider.Validate(); err == nil {
		t.Fatal("non-HTTPS OAuth endpoint accepted")
	}
}

func TestOAuthCredentialIsEncryptedAtRest(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_CREDENTIAL_KEY", "local-development-key-long-enough")
	store, err := NewAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("oauth@example.com", "OAuth User")
	if err != nil {
		t.Fatal(err)
	}
	organization, _, err := store.CreateOrganization("OAuth Org", user)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.StoreOAuthCredential("github", user.ID, organization.ID, map[string]any{"access_token": "access-fixture", "refresh_token": "refresh-fixture", "expires_in": float64(3600)})
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessTokenCiphertext == "access-fixture" || credential.RefreshTokenCiphertext == "refresh-fixture" {
		t.Fatal("oauth secret stored in plaintext")
	}
	if _, err := decryptCredential(credential.RefreshTokenCiphertext); err != nil {
		t.Fatal(err)
	}
}

func TestAuthStoreMFAUsesTOTPAndPersistsEncryptedSecret(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_CREDENTIAL_KEY", "mfa-test-key-long-enough")
	store, err := NewAuthStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("mfa@example.com", "MFA User")
	if err != nil {
		t.Fatal(err)
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("0123456789012345"))
	if _, err := store.EnableMFA(user.ID, secret); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	if err := store.VerifyMFA(user.ID, totpCode(secret, now.Unix()/30), now); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyMFA(user.ID, "000000", now); err == nil {
		t.Fatal("invalid MFA code accepted")
	}
}

func TestRecoveryCodesAreOneTimeAndEncrypted(t *testing.T) {
	t.Setenv("OLLAMA_AGENT_CREDENTIAL_KEY", "recovery-test-key-long-enough")
	root := t.TempDir()
	store, err := NewAuthStore(root)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser("recovery@example.com", "Recovery User")
	if err != nil {
		t.Fatal(err)
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("0123456789012345"))
	if _, err := store.EnableMFA(user.ID, secret); err != nil {
		t.Fatal(err)
	}
	_, codes, err := store.GenerateRecoveryCodes(user.ID)
	if err != nil || len(codes) != 10 {
		t.Fatalf("generate recovery codes = %v, count=%d", err, len(codes))
	}
	if err := store.VerifyRecoveryCode(user.ID, codes[0]); err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyRecoveryCode(user.ID, codes[0]); err == nil {
		t.Fatal("recovery code was reusable")
	}
	reloaded, err := NewAuthStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.VerifyRecoveryCode(user.ID, codes[1]); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(root + "/users.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || string(data) == codes[0] {
		t.Fatal("recovery data was not persisted as a structured record")
	}
}
