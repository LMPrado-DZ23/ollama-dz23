package agent

import (
	"testing"
	"time"
)

func TestDevicePairingHeartbeatAndRevocation(t *testing.T) {
	root := t.TempDir()
	store, err := NewDeviceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := store.StartPairing("user-1", "org-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := store.CompletePairing(code, "Office Mac", "darwin", "", "", []DeviceCapability{{Name: "screen", Scopes: []string{"desktop:screen"}}})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || device.OrganizationID != "org-1" || device.Status != DeviceOnline {
		t.Fatalf("device=%+v token=%q", device, token)
	}
	if _, _, err := store.CompletePairing(code, "Again", "linux", "", "", nil); err == nil {
		t.Fatal("pairing code reused")
	}
	updated, err := store.Heartbeat(device.ID, token, []DeviceCapability{{Name: "keyboard", Scopes: []string{"desktop:input"}}})
	if err != nil || len(updated.Capabilities) != 1 {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	if _, err := store.Revoke(device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Heartbeat(device.ID, token, nil); err == nil {
		t.Fatal("revoked device accepted heartbeat")
	}
	reloaded, err := NewDeviceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := reloaded.Get(device.ID)
	if err != nil || stored.Status != DeviceRevoked {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}
