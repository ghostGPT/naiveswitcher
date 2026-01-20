package types

import "testing"

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
