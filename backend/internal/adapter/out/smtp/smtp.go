package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	netmail "net/mail"
	gosmtp "net/smtp"
	"strconv"
	"strings"
	"time"

	"calendium/backend/internal/config"
	"calendium/backend/internal/port"
)

// sendTimeout bounds one Send end to end (dial, TLS, AUTH, DATA, QUIT).
const sendTimeout = 15 * time.Second

// ErrNoStartTLS is returned when SMTP_SECURE=false and a non-loopback server
// does not offer STARTTLS: credentials and mail are never sent in clear.
var ErrNoStartTLS = errors.New("smtp: server does not offer STARTTLS; refusing to send credentials in clear")

// Client is a port.Mailer over net/smtp. Every Send dials a fresh session;
// the struct holds no connection state, so it is safe for concurrent use.
type Client struct {
	cfg config.SMTP
	// DialContext is the TCP dial seam (tests point it at a fake server).
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// TLSConfig serves implicit TLS and STARTTLS; ServerName defaults to
	// cfg.Host so the server certificate is verified against system roots.
	TLSConfig *tls.Config
	now       func() time.Time
}

var _ port.Mailer = (*Client)(nil)

// New builds a Client for cfg. Call only when cfg.Configured().
func New(cfg config.SMTP) *Client {
	d := &net.Dialer{Timeout: sendTimeout}
	return &Client{
		cfg:         cfg,
		DialContext: d.DialContext,
		TLSConfig:   &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12},
		now:         time.Now,
	}
}

// Send renders msg and delivers it in one SMTP session: implicit TLS when
// Secure, else STARTTLS when advertised (mandatory off loopback); AUTH PLAIN
// (preferred) or LOGIN when the server offers one and SMTP_USER is set; then
// MAIL/RCPT/DATA/QUIT. Honours ctx and caps the whole exchange at 15 s.
func (c *Client) Send(ctx context.Context, msg port.Email) error {
	from, err := netmail.ParseAddress(c.cfg.From)
	if err != nil {
		return fmt.Errorf("smtp: invalid SMTP_FROM %q: %w", c.cfg.From, err)
	}
	msgID, err := newMessageID(from.Address)
	if err != nil {
		return err
	}
	m, err := buildMessage(msg, from, c.now(), msgID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	addr := net.JoinHostPort(c.cfg.Host, strconv.Itoa(c.cfg.Port))
	conn, err := c.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	// Cancellation mid-dialogue closes the socket, which unblocks net/smtp.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if c.cfg.Secure {
		conn = tls.Client(conn, c.TLSConfig)
	}
	cl, err := gosmtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: greeting from %s: %w", addr, err)
	}
	defer func() { _ = cl.Close() }()

	if !c.cfg.Secure {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err := cl.StartTLS(c.TLSConfig); err != nil {
				return fmt.Errorf("smtp: starttls: %w", err)
			}
		} else if !isLoopback(c.cfg.Host) {
			return ErrNoStartTLS
		}
	}
	if c.cfg.User != "" {
		if ok, mechs := cl.Extension("AUTH"); ok {
			auth, err := pickAuth(mechs, c.cfg)
			if err != nil {
				return err
			}
			if err := cl.Auth(auth); err != nil {
				return fmt.Errorf("smtp: auth: %w", err)
			}
		}
	}
	if err := cl.Mail(m.from); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, r := range m.rcpts {
		if err := cl.Rcpt(r); err != nil {
			return fmt.Errorf("smtp: RCPT TO %s: %w", r, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(m.raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: end of data: %w", err)
	}
	if err := cl.Quit(); err != nil {
		return fmt.Errorf("smtp: QUIT: %w", err)
	}
	return nil
}

// pickAuth prefers PLAIN, falls back to LOGIN, and refuses anything else.
// net/smtp's PlainAuth itself refuses an unencrypted non-localhost session.
func pickAuth(mechs string, cfg config.SMTP) (gosmtp.Auth, error) {
	switch {
	case strings.Contains(mechs, "PLAIN"):
		return gosmtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host), nil
	case strings.Contains(mechs, "LOGIN"):
		return loginAuth{user: cfg.User, pass: cfg.Pass}, nil
	}
	return nil, fmt.Errorf("smtp: server offers no supported AUTH mechanism (%q)", mechs)
}

// loginAuth is the AUTH LOGIN mechanism (username/password answered to two
// base64 challenges), still the only mechanism some providers offer. Like
// PlainAuth it refuses to run on an unencrypted connection to a non-loopback
// host.
type loginAuth struct{ user, pass string }

func (a loginAuth) Start(server *gosmtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLoopback(server.Name) {
		return "", nil, errors.New("smtp: LOGIN auth refused on an unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.user), nil
	case "password:":
		return []byte(a.pass), nil
	}
	return nil, fmt.Errorf("smtp: unexpected LOGIN challenge %q", fromServer)
}

// isLoopback reports whether host names the local machine (Mailpit, a dev
// relay): the one case where plaintext SMTP is acceptable.
func isLoopback(host string) bool {
	h := strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
