package observer

import (
	"testing"
	"time"

	"github.com/hewitt-iso/hewitt/gateway"
)

func testPolicy(t *testing.T) *policy {
	return &policy{cfg: Config{StateDir: t.TempDir(), Wait: 5, Watches: []Watch{
		{Path: "/hewitt/work"}, {Path: "/hewitt/profiles/claude", Owner: "claude"}, {Path: "/hewitt/profiles/codex", Owner: "codex"},
	}}, poll: 10 * time.Millisecond}
}

func TestCheckHoldsOtherAgentsLoginsAndCredentialFiles(t *testing.T) {
	p := testPolicy(t)
	codex := Opener{Run: "run-codex", Agent: "codex"}
	for _, tt := range []struct {
		path   string
		opener Opener
		held   bool
	}{
		{"/hewitt/profiles/codex/codex/auth.json", codex, false},
		{"/hewitt/profiles/claude/claude/.credentials.json", codex, true},
		{"/hewitt/profiles/claude/home/.claude.json", Opener{Run: "run-shell", Agent: "shell"}, false},
		{"/hewitt/profiles/claude/claude/.credentials.json", Opener{}, true},
		{"/hewitt/profiles/claude/claude/.credentials.json", Opener{Agent: "shell"}, true},
		{"/hewitt/work/repo/main.go", codex, false},
		{"/hewitt/work/repo/.env", codex, true},
		{"/hewitt/work/repo/.env.example", codex, false},
		{"/hewitt/work/repo/deploy/server.key", codex, true},
		{"/hewitt/work/repo/.env", Opener{Run: "run-shell", Agent: "shell"}, false},
		{"/hewitt/profiles/claudette/x", codex, false},
		{"/tmp/x", codex, false},
	} {
		called := false
		held, _ := p.check(tt.path, func() Opener { called = true; return tt.opener })
		if held != tt.held {
			t.Errorf("%s by %+v: held = %v", tt.path, tt.opener, held)
		}
		if tt.path == "/hewitt/work/repo/main.go" && called {
			t.Error("ordinary workspace file was attributed; attribution must stay off the common path")
		}
	}
}

func TestDecideWaitsAndRemembersPerAgent(t *testing.T) {
	p := testPolicy(t)
	codex := Opener{Run: "run-codex", Agent: "codex"}
	const path = "/hewitt/profiles/claude/claude/.credentials.json"
	result := make(chan bool, 1)
	go func() { result <- p.decide(path, codex) }()
	var pending []gateway.Request
	for deadline := time.Now().Add(3 * time.Second); len(pending) == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		pending, _ = gateway.Requests(p.cfg.StateDir)
	}
	if len(pending) != 1 || pending[0].Kind != "file" || pending[0].Agent != "codex" || pending[0].Run != "run-codex" {
		t.Fatalf("pending = %+v", pending)
	}
	if err := gateway.Decide(p.cfg.StateDir, pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if !<-result {
		t.Fatal("approved open refused")
	}
	if !p.decide(path, codex) {
		t.Fatal("approval not remembered")
	}
	// Approval for codex does not extend to opencode.
	p.cfg.Wait = 1
	if p.decide(path, Opener{Run: "run-oc", Agent: "opencode"}) {
		t.Fatal("approval extended to another agent")
	}
	if list, _ := gateway.Requests(p.cfg.StateDir); len(list) != 2 {
		t.Fatalf("requests = %+v", list)
	}
}
