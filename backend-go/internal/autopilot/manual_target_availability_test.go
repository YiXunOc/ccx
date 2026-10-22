package autopilot

import (
	"path/filepath"
	"testing"
)

func TestManualTargetAvailabilityKnownUnknownAndExact(t *testing.T) {
	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !ManualTargetAvailable(store, "ch", "https://example.test", "key", "B") {
		t.Fatal("unknown inventory must allow attempt")
	}
	uid := GenerateEndpointUID("ch", "https://example.test", KeyHashFromAPIKey("key"))
	if err := store.Upsert(&KeyEndpointProfile{EndpointUID: uid, ChannelUID: "ch", AvailableModels: []string{"C", "b"}}); err != nil {
		t.Fatal(err)
	}
	if ManualTargetAvailable(store, "ch", "https://example.test", "key", "B") {
		t.Fatal("known unavailable B must be rejected")
	}
	if !ManualTargetAvailable(store, "ch", "https://example.test", "key", "b") {
		t.Fatal("exact case-sensitive B target missing")
	}
	if !ManualTargetAvailable(store, "ch", "https://example.test", "other-key", "B") {
		t.Fatal("other key unknown inventory must allow attempt")
	}
}
