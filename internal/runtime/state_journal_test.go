package runtime

import (
	"testing"

	"open-mihomo-gateway/internal/platform"
)

func TestSaveStateJournalsNetworkCleanupIntentBeforeMutation(t *testing.T) {
	path := t.TempDir() + "/state.json"
	snapshot := platform.NewSnapshot(platform.BackendLinuxNFTables)
	snapshot.NetworkNamespace = "net:[test]"
	snapshot.IPv4Forwarding = "0"
	snapshot.NFTablesTable = "opensurge"
	snapshot.NAT = &platform.NATConfig{
		LANInterface: "eth0",
		LANCIDR:      "192.168.2.0/24",
		TableName:    "opensurge",
		FwMark:       0x29,
	}
	snapshot.Routing = &platform.RoutingConfig{
		LANInterface: "eth0",
		LANCIDR:      "192.168.2.0/24",
		TUNDevice:    "tun0",
		TableID:      20241,
		RulePriority: 20241,
		FwMark:       0x29,
	}

	if err := SaveState(path, State{NetworkSnapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := LoadState(path)
	if err != nil || !exists {
		t.Fatalf("LoadState exists=%v err=%v", exists, err)
	}
	if loaded.NetworkSnapshot == nil {
		t.Fatal("saved state lost NetworkSnapshot")
	}
	applied := loaded.NetworkSnapshot.Applied
	if !applied.IPv4Forwarding || !applied.NAT || !applied.PolicyRouting {
		t.Fatalf("write-ahead journal = %#v, want all cleanup intents true", applied)
	}
}
