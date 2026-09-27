package types

import "testing"

func TestServersReturnsIndependentSnapshot(t *testing.T) {
	state := &GlobalState{}
	state.SetCurrentServer("https://one.example")
	state.SetServers([]string{"https://one.example"})
	current, servers := state.Servers()
	state.SetCurrentServer("https://two.example")
	state.SetServers([]string{"https://two.example"})
	if current != "https://one.example" || len(servers) != 1 || servers[0] != "https://one.example" {
		t.Fatalf("snapshot changed: current=%q servers=%v", current, servers)
	}
}

func TestSaveLoadPersistedState(t *testing.T) {
	base := t.TempDir()
	expected := PersistedState{AutoSwitchPaused: true, LockedServer: "https://u:p@example.com:443"}
	if err := SavePersistedState(base, expected); err != nil {
		t.Fatalf("SavePersistedState error: %v", err)
	}
	got, err := LoadPersistedState(base)
	if err != nil {
		t.Fatalf("LoadPersistedState error: %v", err)
	}
	if got != expected {
		t.Fatalf("unexpected state: %+v", got)
	}
}

func TestLoadPersistedStateMissingFile(t *testing.T) {
	base := t.TempDir()
	got, err := LoadPersistedState(base)
	if err != nil {
		t.Fatalf("LoadPersistedState error: %v", err)
	}
	if got != (PersistedState{}) {
		t.Fatalf("unexpected state: %+v", got)
	}
}
