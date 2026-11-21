package terminal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// popupTmux fakes a tmux whose only client is width x height; popups fail
// when noPopup is set, as on tmux before 3.2.
func popupTmux(t *testing.T, clients string, noPopup bool) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	t.Setenv("PATH", dir)
	t.Setenv("TMUX_TEST_LOG", log)
	fail := ""
	if noPopup {
		fail = "exit 1"
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_TEST_LOG"
case "$3" in
display-message) if [ "$4" = -p ]; then printf '$1\n'; fi ;;
list-clients) printf '` + clients + `' ;;
display-popup) ` + fail + ` ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return log
}

func TestPopupFitsClientAndQuotesCommand(t *testing.T) {
	l := Location{Socket: "/tmp/host.sock", Pane: "%3"}
	log := popupTmux(t, `/dev/pts/2\t80\t24\n/dev/pts/5\t200\t60\n`, false)
	if err := Popup(context.Background(), l, " title ", []string{"/opt/hew itt", "it's"}, "message"); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	want := `-S /tmp/host.sock display-popup -c /dev/pts/2 -t %3 -E -w 78 -h 14 -T  title  '/opt/hew itt' 'it'\''s'`
	if !strings.Contains(string(calls), want) {
		t.Fatalf("calls = %s, want %s", calls, want)
	}
}

func TestPopupFallsBackToMessage(t *testing.T) {
	l := Location{Socket: "/tmp/host.sock", Pane: "%3"}
	log := popupTmux(t, `/dev/pts/2\t200\t60\n`, true)
	if err := Popup(context.Background(), l, "t", []string{"x"}, "Hewitt SRNA #1"); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "display-message -c /dev/pts/2 -d 15000 Hewitt SRNA #1") {
		t.Fatalf("calls = %s", calls)
	}
}

func TestPopupWithoutClient(t *testing.T) {
	popupTmux(t, ``, false)
	if err := Popup(context.Background(), Location{Socket: "/tmp/host.sock", Pane: "%3"}, "t", []string{"x"}, "m"); err != ErrNoClient {
		t.Fatalf("err = %v", err)
	}
	if err := Popup(context.Background(), Location{}, "t", []string{"x"}, "m"); err != ErrNoClient {
		t.Fatalf("plain terminal err = %v", err)
	}
}
