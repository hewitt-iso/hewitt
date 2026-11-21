package gateway

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// Server is a CONNECT proxy. Tunnels to permitted hosts are passed through
// unread, except to injection hosts, whose TLS it terminates to replace
// placeholder credentials. Plain HTTP requests are refused.
type Server struct {
	cfg   Config
	rules []Rule
	ca    *CA
	// Dial connects to upstream hosts; tests replace it.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// UpstreamTLS verifies upstream servers of injection hosts; nil uses the
	// system roots.
	UpstreamTLS *tls.Config
	Log         *log.Logger
	poll        time.Duration

	once      sync.Once
	transport *http.Transport
	// mu serializes request recording, so concurrent connections to one
	// unlisted host share one pending request.
	mu sync.Mutex
}

// New prepares a server for cfg.
func New(cfg Config) (*Server, error) {
	rules, err := ParseRules(cfg.Allow)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, rules: rules, Dial: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		Log: log.New(os.Stderr, "", log.LstdFlags), poll: 250 * time.Millisecond}
	if s.cfg.Wait <= 0 {
		s.cfg.Wait = 60
	}
	if len(cfg.Inject) > 0 {
		if s.ca, err = LoadCA(cfg.CADir); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// ListenAndServe serves on the configured address until it fails.
func (s *Server) ListenAndServe() error {
	l, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Listen, err)
	}
	return s.Serve(l)
}

// Serve accepts connections on l until it is closed.
func (s *Server) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if req.Method != http.MethodConnect {
		refuse(conn, http.StatusForbidden, "plain HTTP is refused; use HTTPS through the proxy")
		return
	}
	host, port, err := splitTarget(req.Host)
	if err != nil {
		refuse(conn, http.StatusBadRequest, err.Error())
		return
	}
	target := net.JoinHostPort(host, port)
	inject := s.injection(host, port)
	if inject == nil {
		allowed, reason := s.permit(target, host, port)
		s.Log.Printf("%s %s", reason, target)
		if !allowed {
			refuse(conn, http.StatusForbidden, "hewitt gateway: "+reason+" "+target)
			return
		}
	}
	upstream := conn
	if inject == nil {
		up, err := s.Dial(context.Background(), "tcp", target)
		if err != nil {
			refuse(conn, http.StatusBadGateway, "connect "+target+": "+err.Error())
			return
		}
		defer up.Close()
		upstream = up
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	client := &bufferedConn{Conn: conn, r: br}
	if inject != nil {
		s.Log.Printf("inject %s", target)
		s.intercept(client, host, inject)
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); closeWrite(upstream); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, upstream); closeWrite(conn); done <- struct{}{} }()
	<-done
	<-done
}

// permit applies the host policy; an unlisted host waits for a decision.
func (s *Server) permit(target, host, port string) (bool, string) {
	if s.cfg.Open {
		return true, "open"
	}
	if match(s.rules, host, port) {
		return true, "allow"
	}
	id, decision, err := s.request(target)
	if err != nil {
		s.Log.Printf("record request for %s: %v", target, err)
		return false, "deny"
	}
	deadline := time.Now().Add(time.Duration(s.cfg.Wait) * time.Second)
	for decision == "" && time.Now().Before(deadline) {
		time.Sleep(s.poll)
		decision = s.decision(id)
	}
	switch decision {
	case "allow":
		return true, "approved " + id
	case "deny":
		return false, "denied " + id
	}
	return false, "pending approval " + id + " (hewitt approve " + id + ")"
}

// request returns the latest request for target with its decision, or records
// a new pending one.
func (s *Server) request(target string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := Requests(s.cfg.StateDir)
	if err != nil {
		return "", "", err
	}
	for _, r := range slices.Backward(list) {
		if r.Kind == "network" && r.Target == target {
			return r.ID, r.Decision, nil
		}
	}
	r, err := record(s.cfg.StateDir, "network", target)
	return r.ID, "", err
}

func (s *Server) decision(id string) string {
	data, err := os.ReadFile(decisionsDir(s.cfg.StateDir) + "/" + id)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (s *Server) injection(host, port string) *Injection {
	if port != "443" {
		return nil
	}
	for i := range s.cfg.Inject {
		if slices.Contains(s.cfg.Inject[i].Hosts, host) {
			return &s.cfg.Inject[i]
		}
	}
	return nil
}

// intercept terminates the client's TLS with a certificate from the CA and
// forwards each request to the real host with the placeholder replaced.
// Responses stream back unbuffered, as model APIs send server-sent events.
func (s *Server) intercept(conn net.Conn, host string, in *Injection) {
	cert, err := s.ca.leaf(host)
	if err != nil {
		s.Log.Printf("certificate for %s: %v", host, err)
		return
	}
	tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*cert}, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	if err := tlsConn.Handshake(); err != nil {
		s.Log.Printf("TLS with client for %s: %v", host, err)
		return
	}
	s.once.Do(func() {
		s.transport = &http.Transport{DialContext: s.Dial, TLSClientConfig: s.UpstreamTLS, ForceAttemptHTTP2: true,
			MaxIdleConns: 16, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 15 * time.Second}
	})
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "https", host, host
			token := readToken(in.TokenFile, in.Variable)
			if token == "" {
				return
			}
			for _, values := range pr.Out.Header {
				for i, v := range values {
					values[i] = strings.ReplaceAll(v, in.Placeholder, token)
				}
			}
		},
		Transport:     s.transport,
		FlushInterval: -1,
		ErrorLog:      s.Log,
	}
	l := &oneConnListener{conn: tlsConn, closed: make(chan struct{})}
	srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 60 * time.Second, ErrorLog: s.Log,
		ConnState: func(_ net.Conn, state http.ConnState) {
			if state == http.StateClosed || state == http.StateHijacked {
				l.close()
			}
		}}
	_ = srv.Serve(l)
}

// readToken returns variable's value from a KEY=VALUE file, or "".
func readToken(path, variable string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), variable+"="); ok {
			return strings.Trim(value, `"'`)
		}
	}
	return ""
}

func refuse(conn net.Conn, code int, message string) {
	fmt.Fprintf(conn, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s\n",
		code, http.StatusText(code), len(message)+1, message)
}

func closeWrite(conn net.Conn) {
	if c, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	}
}

// bufferedConn reads bytes the request parser buffered before the tunnel.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *bufferedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// oneConnListener serves a single connection to http.Server.
type oneConnListener struct {
	mu     sync.Mutex
	conn   net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	conn := l.conn
	l.conn = nil
	l.mu.Unlock()
	if conn != nil {
		return conn, nil
	}
	<-l.closed
	return nil, errors.New("connection closed")
}

func (l *oneConnListener) close()         { l.once.Do(func() { close(l.closed) }) }
func (l *oneConnListener) Close() error   { l.close(); return nil }
func (l *oneConnListener) Addr() net.Addr { return &net.TCPAddr{} }
