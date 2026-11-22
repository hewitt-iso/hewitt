package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHerdr installs a herdr CLI that logs its arguments and socket and
// answers pane split with a new pane.
func fakeHerdr(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("HERDR_TEST_LOG", log)
	script := `#!/bin/sh
printf '%s|%s\n' "$HERDR_SOCKET_PATH" "$*" >> "$HERDR_TEST_LOG"
case "$1 $2" in
"pane split") printf '{"id":"cli:pane:split","result":{"type":"pane_split","pane":{"pane_id":"w1:p7"}}}\n' ;;
esac
`
	bin := filepath.Join(dir, "herdr")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", bin)
	return log
}

func inHerdr(t *testing.T, socket, pane string) {
	t.Helper()
	t.Setenv("TMUX", "")
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", pane)
}

func TestCurrentLocatesHerdrPanes(t *testing.T) {
	inHerdr(t, "/run/herdr.sock", "w2:p3")
	l, err := Current()
	if err != nil || l != (Location{HerdrSocket: "/run/herdr.sock", HerdrPane: "w2:p3"}) || !l.Kept() || l.Manager() != "herdr" {
		t.Fatalf("location = %+v, %v", l, err)
	}
	if hint := AgentHint(l, "antigravity"); len(hint) != 1 || hint[0] != "HERDR_AGENT=agy" {
		t.Fatalf("hint = %q", hint)
	}
	if hint := AgentHint(l, "shell"); hint != nil {
		t.Fatalf("shell hint = %q", hint)
	}
	// A tmux started inside the Herdr pane is the closer pane.
	t.Setenv("TMUX", "/tmp/host,1,0")
	t.Setenv("TMUX_PANE", "%4")
	if l, err := Current(); err != nil || l.Socket != "/tmp/host" || l.HerdrPane != "" || AgentHint(l, "claude") != nil {
		t.Fatalf("tmux in Herdr = %+v, %v", l, err)
	}
	// Variables that do not name a pane are ignored.
	inHerdr(t, "relative.sock", "w2:p3")
	if l, err := Current(); err != nil || l.Kept() {
		t.Fatalf("invalid Herdr variables = %+v, %v", l, err)
	}
	if err := (Location{HerdrSocket: "/s", HerdrPane: "w1:p1; rm"}).Validate(); err == nil {
		t.Fatal("accepted an invalid Herdr pane")
	}
}

func TestPopupInHerdrSplitsTheAgentPane(t *testing.T) {
	log := fakeHerdr(t)
	l := Location{HerdrSocket: "/run/herdr.sock", HerdrPane: "w1:p2"}
	if err := Popup(context.Background(), l, " Hewitt ", []string{"/bin/hewitt", "srna-prompt", "--run", "it's"}, "SRNA #1"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	want := "/run/herdr.sock|pane split w1:p2 --direction down --ratio 0.6 --focus\n" +
		"/run/herdr.sock|pane run w1:p7 exec '/bin/hewitt' 'srna-prompt' '--run' 'it'\\''s'\n" +
		"/run/herdr.sock|notification show Hewitt --body SRNA #1 --sound request\n"
	if string(raw) != want {
		t.Fatalf("calls:\n%s\nwant:\n%s", raw, want)
	}
}

func TestFocusHerdrPane(t *testing.T) {
	log := fakeHerdr(t)
	l := Location{HerdrSocket: "/run/herdr.sock", HerdrPane: "w1:p2"}
	inHerdr(t, "/run/other.sock", "w1:p1")
	var other *OtherHerdrError
	if err := Focus(context.Background(), l, nil, nil, nil); !errors.As(err, &other) {
		t.Fatalf("other session: %v", err)
	}
	inHerdr(t, "/run/herdr.sock", "w1:p1")
	if err := Focus(context.Background(), l, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(log); !strings.Contains(string(raw), "/run/herdr.sock|agent focus w1:p2") {
		t.Fatalf("calls:\n%s", raw)
	}
}
