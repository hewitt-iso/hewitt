package session

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRunningAgentConnections(t *testing.T) {
	t.Setenv("TMUX", "")
	store := testStore(t)
	empty, err := store.RunningAgents()
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty: %v %v", empty, err)
	}
	if _, err := os.Stat(store.dataDir); !os.IsNotExist(err) {
		t.Fatalf("read created data: %v", err)
	}
	m := testMetadata()
	first, release, err := store.RecordRun(m, "codex")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	second, releaseSecond, err := store.RecordRun(m, "claude")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseSecond()
	runs, err := store.RunningAgents()
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %v, %v", runs, err)
	}
	if first.ID == second.ID || first.WorkspaceID != second.WorkspaceID {
		t.Fatalf("connections do not share workspace: %v %v", first, second)
	}
	if err := Attach(context.Background(), store, first.ID[:12], nil, io.Discard, io.Discard); err == nil {
		t.Fatal("plain exec claimed to be reattachable")
	}
	// A released or crashed connection must never remain selectable.
	releaseSecond()
	runs, err = store.RunningAgents()
	if err != nil || len(runs) != 1 || runs[0].ID != first.ID {
		t.Fatalf("released run remains: %v %v", runs, err)
	}
	path := filepath.Join(store.dataDir, "runs", first.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// An unlocked leftover (as after a crash) is ignored even if its JSON is bad.
	stale := filepath.Join(store.dataDir, "runs", second.ID+".json")
	if err := os.WriteFile(stale, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	runs, err = store.RunningAgents()
	if err != nil || len(runs) != 1 {
		t.Fatalf("stale run considered active: %v %v", runs, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("run permissions: %v %v", info, err)
	}
	if len(data) == 0 {
		t.Fatal("empty record")
	}
}
