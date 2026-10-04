package smtp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"calendium/backend/internal/config"
	"calendium/backend/internal/port"
)

// fakeServer is a scripted SMTP server on 127.0.0.1:0 that records every
// command and the DATA payload it receives. It serves exactly one session.
type fakeServer struct {
	t         *testing.T
	ln        net.Listener
	tlsConfig *tls.Config // nil: no TLS at all; set: offer STARTTLS (plain listener) or serve implicit TLS
	implicit  bool        // listen with tls.Listen instead of offering STARTTLS
	authMechs string      // "" = no AUTH extension; e.g. "PLAIN LOGIN"
	silent    bool        // never send the 220 greeting (deadline tests)

	mu       sync.Mutex
	commands []string
	data     string
	authUser string
	authPass string
	upgraded bool // STARTTLS completed
}

func (s *fakeServer) addr() string { return s.ln.Addr().String() }

func (s *fakeServer) port() int {
	_, p, _ := net.SplitHostPort(s.addr())
	n, _ := strconv.Atoi(p)
	return n
}

func startFakeServer(t *testing.T, s *fakeServer) *fakeServer {
	t.Helper()
	s.t = t
	var err error
	if s.implicit {
		s.ln, err = tls.Listen("tcp", "127.0.0.1:0", s.tlsConfig)
	} else {
		s.ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = s.ln.Close() })
	go func() {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		s.serve(conn)
	}()
	return s
}

func (s *fakeServer) record(cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, cmd)
}

func (s *fakeServer) serve(conn net.Conn) {
	if s.silent {
		time.Sleep(2 * time.Second)
		return
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	reply := func(lines ...string) {
		for _, l := range lines {
			_, _ = w.WriteString(l + "\r\n")
		}
		_ = w.Flush()
	}
	reply("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.record(line)
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO", "HELO":
			ext := []string{"250-fake"}
			s.mu.Lock()
			upgraded := s.upgraded
			s.mu.Unlock()
			if s.tlsConfig != nil && !s.implicit && !upgraded {
				ext = append(ext, "250-STARTTLS")
			}
			if s.authMechs != "" {
				ext = append(ext, "250-AUTH "+s.authMechs)
			}
			ext = append(ext, "250 8BITMIME")
			reply(ext...)
		case "STARTTLS":
			reply("220 go ahead")
			tc := tls.Server(conn, s.tlsConfig)
			if err := tc.Handshake(); err != nil {
				return
			}
			s.mu.Lock()
			s.upgraded = true
			s.mu.Unlock()
			conn = tc
			r = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
		case "AUTH":
			s.handleAuth(line, r, reply)
		case "MAIL", "RCPT":
			reply("250 OK")
		case "DATA":
			reply("354 go")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(l)
			}
			s.mu.Lock()
			s.data = body.String()
			s.mu.Unlock()
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("500 unknown")
		}
	}
}

func (s *fakeServer) handleAuth(line string, r *bufio.Reader, reply func(...string)) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		reply("501 bad auth")
		return
	}
	switch strings.ToUpper(fields[1]) {
	case "PLAIN":
		var payload string
		if len(fields) >= 3 {
			payload = fields[2]
		} else {
			reply("334 ")
			l, _ := r.ReadString('\n')
			payload = strings.TrimSpace(l)
		}
		raw, _ := base64.StdEncoding.DecodeString(payload)
		parts := strings.Split(string(raw), "\x00")
		if len(parts) == 3 {
			s.mu.Lock()
			s.authUser, s.authPass = parts[1], parts[2]
			s.mu.Unlock()
		}
		reply("235 ok")
	case "LOGIN":
		reply("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
		u, _ := r.ReadString('\n')
		reply("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
		p, _ := r.ReadString('\n')
		ub, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(u))
		pb, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(p))
		s.mu.Lock()
		s.authUser, s.authPass = string(ub), string(pb)
		s.mu.Unlock()
		reply("235 ok")
	default:
		reply("504 unsupported")
	}
}

// localCert mints a self-signed ECDSA certificate for 127.0.0.1 and returns
// the server config that serves it and a client config that trusts it.
func localCert(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	server = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}},
		MinVersion:   tls.VersionTLS12,
	}
	client = &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	return server, client
}

// newTestClient builds a Client that always dials the fake server, whatever
// cfg.Host says (so a non-loopback hostname can be exercised).
func newTestClient(t *testing.T, srv *fakeServer, cfg config.SMTP, tlsCfg *tls.Config) *Client {
	t.Helper()
	cfg.Port = srv.port()
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.From == "" {
		cfg.From = "Calendium <noreply@calendium.test>"
	}
	c := New(cfg)
	c.now = func() time.Time { return testNow }
	target := srv.addr()
	c.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	if tlsCfg != nil {
		c.TLSConfig = tlsCfg
	}
	return c
}

var sampleMail = port.Email{
	To:      []string{"ada@example.test"},
	Subject: "Hi",
	Text:    "Hello\n.leading dot line\nbye\n",
	HTML:    "<p>Hello</p>",
}

func indexPrefix(cmds []string, prefix string) int {
	for i, c := range cmds {
		if strings.HasPrefix(strings.ToUpper(c), strings.ToUpper(prefix)) {
			return i
		}
	}
	return -1
}

func TestSendPlaintextToLoopbackWithAuthPlain(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "PLAIN LOGIN"})
	c := newTestClient(t, srv, config.SMTP{User: "apikey", Pass: "s3cret"}, nil)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.authUser != "apikey" || srv.authPass != "s3cret" {
		t.Fatalf("auth = %q/%q, want apikey/s3cret", srv.authUser, srv.authPass)
	}
	if indexPrefix(srv.commands, "AUTH PLAIN") < 0 {
		t.Fatalf("PLAIN must be preferred over LOGIN; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "MAIL FROM:<noreply@calendium.test>") < 0 {
		t.Fatalf("missing MAIL FROM; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "RCPT TO:<ada@example.test>") < 0 {
		t.Fatalf("missing RCPT TO; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "QUIT") < 0 {
		t.Fatalf("session must end with QUIT; commands = %v", srv.commands)
	}
	if !strings.Contains(srv.data, "Subject: Hi\r\n") || !strings.Contains(srv.data, "Content-Type: multipart/alternative") {
		t.Fatalf("DATA payload missing headers:\n%s", srv.data)
	}
	// The body line that starts with "." reached the server dot-stuffed and
	// did not terminate the message early ("bye" is still inside the body).
	if !strings.Contains(srv.data, "\r\n..leading dot line\r\n") || !strings.Contains(srv.data, "\r\nbye\r\n") {
		t.Fatalf("DATA payload is not dot-stuffed as expected:\n%s", srv.data)
	}
	if srv.upgraded {
		t.Fatal("no STARTTLS was offered, so no upgrade should have happened")
	}
}

func TestSendStartTLSUpgradeThenAuth(t *testing.T) {
	serverTLS, clientTLS := localCert(t)
	srv := startFakeServer(t, &fakeServer{tlsConfig: serverTLS, authMechs: "PLAIN"})
	c := newTestClient(t, srv, config.SMTP{User: "u", Pass: "p"}, clientTLS)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.upgraded {
		t.Fatal("STARTTLS was advertised but the client did not upgrade")
	}
	tlsIdx, authIdx := indexPrefix(srv.commands, "STARTTLS"), indexPrefix(srv.commands, "AUTH PLAIN")
	if tlsIdx < 0 || authIdx < 0 || authIdx < tlsIdx {
		t.Fatalf("AUTH must follow STARTTLS; commands = %v", srv.commands)
	}
	if srv.authUser != "u" || srv.authPass != "p" {
		t.Fatalf("auth = %q/%q", srv.authUser, srv.authPass)
	}
	if srv.data == "" {
		t.Fatal("no DATA received over the upgraded connection")
	}
}

func TestSendImplicitTLS(t *testing.T) {
	serverTLS, clientTLS := localCert(t)
	srv := startFakeServer(t, &fakeServer{implicit: true, tlsConfig: serverTLS})
	c := newTestClient(t, srv, config.SMTP{Secure: true}, clientTLS)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if indexPrefix(srv.commands, "STARTTLS") >= 0 {
		t.Fatalf("implicit TLS must not negotiate STARTTLS; commands = %v", srv.commands)
	}
	if indexPrefix(srv.commands, "AUTH") >= 0 {
		t.Fatalf("no SMTP_USER, so no AUTH; commands = %v", srv.commands)
	}
	if !strings.Contains(srv.data, "From: \"Calendium\" <noreply@calendium.test>\r\n") {
		t.Fatalf("DATA payload missing From:\n%s", srv.data)
	}
}

func TestSendImplicitTLSRejectsUntrustedCertificate(t *testing.T) {
	serverTLS, _ := localCert(t)
	srv := startFakeServer(t, &fakeServer{implicit: true, tlsConfig: serverTLS})
	// Default TLSConfig: system roots only, so the in-test cert must fail verification.
	c := newTestClient(t, srv, config.SMTP{Secure: true}, nil)
	err := c.Send(context.Background(), sampleMail)
	if err == nil {
		t.Fatal("Send succeeded against an untrusted certificate")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.data != "" {
		t.Fatal("message must not be delivered over an unverified connection")
	}
}

func TestSendRefusesPlaintextOffLoopback(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "PLAIN"})
	c := newTestClient(t, srv, config.SMTP{Host: "mail.example.test", User: "u", Pass: "p"}, nil)

	err := c.Send(context.Background(), sampleMail)
	if !errors.Is(err, ErrNoStartTLS) {
		t.Fatalf("err = %v, want ErrNoStartTLS", err)
	}
	if err.Error() != "smtp: server does not offer STARTTLS; refusing to send credentials in clear" {
		t.Fatalf("err text = %q", err.Error())
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if indexPrefix(srv.commands, "AUTH") >= 0 || indexPrefix(srv.commands, "MAIL") >= 0 {
		t.Fatalf("nothing may be sent after the refusal; commands = %v", srv.commands)
	}
}

func TestSendAuthLoginFallback(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "LOGIN"})
	c := newTestClient(t, srv, config.SMTP{User: "login-user", Pass: "login-pass"}, nil)

	if err := c.Send(context.Background(), sampleMail); err != nil {
		t.Fatalf("Send: %v", err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if indexPrefix(srv.commands, "AUTH LOGIN") < 0 {
		t.Fatalf("expected AUTH LOGIN; commands = %v", srv.commands)
	}
	if srv.authUser != "login-user" || srv.authPass != "login-pass" {
		t.Fatalf("auth = %q/%q", srv.authUser, srv.authPass)
	}
}

func TestSendFailsOnUnsupportedAuthMechanism(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{authMechs: "CRAM-MD5"})
	c := newTestClient(t, srv, config.SMTP{User: "u", Pass: "p"}, nil)
	err := c.Send(context.Background(), sampleMail)
	if err == nil || !strings.Contains(err.Error(), "no supported AUTH mechanism") {
		t.Fatalf("err = %v, want an unsupported-mechanism error", err)
	}
}

func TestSendHonoursContextDeadline(t *testing.T) {
	srv := startFakeServer(t, &fakeServer{silent: true})
	c := newTestClient(t, srv, config.SMTP{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.Send(ctx, sampleMail)
	if err == nil {
		t.Fatal("Send succeeded against a server that never greeted")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Send ignored the ctx deadline: took %v", elapsed)
	}
}

func TestSendRejectsInvalidFromBeforeDialing(t *testing.T) {
	c := New(config.SMTP{Host: "127.0.0.1", Port: 1, From: "nope"})
	dialed := false
	c.DialContext = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected dial")
	}
	err := c.Send(context.Background(), sampleMail)
	if err == nil || !strings.Contains(err.Error(), "invalid SMTP_FROM") {
		t.Fatalf("err = %v, want an invalid SMTP_FROM error", err)
	}
	if dialed {
		t.Fatal("a bad SMTP_FROM must fail before any network activity")
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "127.1.2.3": true, "::1": true, "[::1]": true,
		"mail.example.test": false, "10.0.0.5": false, "localhost.example": false, "": false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}
