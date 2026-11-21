package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const placeholder = "sk-ant-oat01-hewitt-placeholder"

// gateway starts a server whose upstream connections all reach addr.
func gateway(t *testing.T, cfg Config, addr string, upstreamTLS *tls.Config) string {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s.Dial = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	s.UpstreamTLS, s.poll, s.Log = upstreamTLS, 10*time.Millisecond, log.New(io.Discard, "", 0)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go s.Serve(l)
	return l.Addr().String()
}

func TestInjectionReplacesPlaceholderOnlyForProviderHosts(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureCA(dir); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "token.env")
	if err := os.WriteFile(token, []byte("CLAUDE_CODE_OAUTH_TOKEN=real-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Two flushed events: the response must stream, not buffer.
		fmt.Fprintf(w, "auth=%s host=%s\n", r.Header.Get("Authorization"), r.Host)
		w.(http.Flusher).Flush()
		fmt.Fprint(w, "event=2\n")
	}))
	defer upstream.Close()
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	addr := gateway(t, Config{Open: true, CADir: dir, StateDir: t.TempDir(), Inject: []Injection{{
		Hosts: []string{"api.anthropic.com"}, Placeholder: placeholder, TokenFile: token, Variable: "CLAUDE_CODE_OAUTH_TOKEN",
	}}}, upstream.Listener.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "example.com"})

	caPEM, err := os.ReadFile(CACertPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	proxy, _ := url.Parse("http://" + addr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{RootCAs: roots}}}
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+placeholder)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := string(body); got != "auth=Bearer real-token host=api.anthropic.com\nevent=2\n" {
		t.Fatalf("injected request = %q", got)
	}

	// After a relogin the new token applies without a restart.
	if err := os.WriteFile(token, []byte("CLAUDE_CODE_OAUTH_TOKEN=new-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("GET", "https://api.anthropic.com/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+placeholder)
	if resp, err = client.Do(req); err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.HasPrefix(string(body), "auth=Bearer new-token ") {
		t.Fatalf("token not reloaded: %q", body)
	}
}

// echo is a TCP server that writes back what it reads.
func echo(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return l.Addr().String()
}

// connect sends a CONNECT and returns the status code and the open tunnel.
func connect(t *testing.T, addr, target string) (int, net.Conn) {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(c, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "CONNECT"})
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, c
}

func TestPolicyTunnelsAllowedHostsAndQueuesOthers(t *testing.T) {
	state := t.TempDir()
	addr := gateway(t, Config{Allow: []string{"*.github.com", "pypi.org:8443"}, StateDir: state, Wait: 5}, echo(t), nil)

	code, c := connect(t, addr, "api.github.com:443")
	if code != 200 {
		t.Fatalf("allowed host = %d", code)
	}
	fmt.Fprint(c, "ping")
	buf := make([]byte, 4)
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("tunnel = %q, %v", buf, err)
	}
	c.Close()
	if code, _ := connect(t, addr, "pypi.org:443"); code != 403 {
		t.Fatalf("unlisted port passed waiting = %d", code)
	}

	// An unlisted host waits; approval lets the waiting connection through.
	result := make(chan int, 1)
	go func() { code, _ := connect(t, addr, "attacker.example:443"); result <- code }()
	var pending []Request
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		list, err := Requests(state)
		if err != nil {
			t.Fatal(err)
		}
		pending = nil
		for _, r := range list {
			if r.Target == "attacker.example:443" && r.Decision == "" {
				pending = append(pending, r)
			}
		}
		if len(pending) > 0 {
			break
		}
	}
	if len(pending) != 1 || pending[0].Kind != "network" {
		t.Fatalf("pending = %+v", pending)
	}
	if err := Decide(state, pending[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if code := <-result; code != 200 {
		t.Fatalf("approved host = %d", code)
	}
	if code, _ := connect(t, addr, "attacker.example:443"); code != 200 {
		t.Fatalf("approval not remembered = %d", code)
	}

	// A denied request is refused at once afterwards.
	go func() { code, _ := connect(t, addr, "exfil.example:443"); result <- code }()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		list, _ := Requests(state)
		if len(list) == 3 {
			if err := Decide(state, list[2].ID, false); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if code := <-result; code != 403 {
		t.Fatalf("denied host = %d", code)
	}
	start := time.Now()
	if code, _ := connect(t, addr, "exfil.example:443"); code != 403 || time.Since(start) > time.Second {
		t.Fatalf("denied host retried = %d after %v", code, time.Since(start))
	}
}

func TestPlainHTTPRefused(t *testing.T) {
	addr := gateway(t, Config{Open: true, StateDir: t.TempDir()}, echo(t), nil)
	proxy, _ := url.Parse("http://" + addr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	resp, err := client.Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("plain HTTP = %d", resp.StatusCode)
	}
}

func TestParseRules(t *testing.T) {
	rules, err := ParseRules([]string{"*.example.com", "otel.example.org:4318"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		host, port string
		want       bool
	}{
		{"example.com", "443", true}, {"a.b.example.com", "443", true}, {"badexample.com", "443", false},
		{"example.com", "80", false}, {"otel.example.org", "4318", true}, {"otel.example.org", "443", false},
	} {
		if got := match(rules, tt.host, tt.port); got != tt.want {
			t.Errorf("match(%s:%s) = %v", tt.host, tt.port, got)
		}
	}
	for _, bad := range []string{"Example.com", "http://x.com", "x.com:0", "x.com:99999", "localhost"} {
		if _, err := ParseRules([]string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPendingRequestsArePerLaunch(t *testing.T) {
	state := t.TempDir()
	addr := gateway(t, Config{StateDir: state, Wait: 3}, echo(t), nil)
	runA, runB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	send := func(run, agent string) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Error(err)
			return
		}
		defer c.Close()
		auth := base64.StdEncoding.EncodeToString([]byte(run + ":" + agent))
		fmt.Fprintf(c, "CONNECT x.example:443 HTTP/1.1\r\nHost: x.example:443\r\nProxy-Authorization: Basic %s\r\n\r\n", auth)
		_, _ = http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "CONNECT"})
	}
	done := make(chan struct{}, 2)
	go func() { send(runA, "codex"); done <- struct{}{} }()
	go func() { send(runB, "claude"); done <- struct{}{} }()
	var list []Request
	for deadline := time.Now().Add(2 * time.Second); len(list) < 2 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		list, _ = Requests(state)
	}
	if len(list) != 2 || list[0].Run == list[1].Run {
		t.Fatalf("requests = %+v", list)
	}
	agents := map[string]string{list[0].Run: list[0].Agent, list[1].Run: list[1].Agent}
	if agents[runA] != "codex" || agents[runB] != "claude" {
		t.Fatalf("attribution = %v", agents)
	}
	if err := Decide(state, list[0].ID, true); err != nil {
		t.Fatal(err)
	}
	<-done
	<-done
	// The decision covers later connections of any launch.
	if code, _ := connect(t, addr, "x.example:443"); code != 200 {
		t.Fatalf("decided target = %d", code)
	}
}
