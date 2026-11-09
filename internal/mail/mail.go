// Package mail sends e-mail through the administrator's own SMTP server
// (STARTTLS or implicit TLS with at least TLS 1.2; PLAIN, LOGIN or XOAUTH2
// authentication for Microsoft 365).
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"
	"golang.org/x/oauth2/clientcredentials"
)

// Security modes.
const (
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"
	SecurityNone     = "none"
)

// Authentication modes.
const (
	AuthNone    = "none"
	AuthPlain   = "plain"
	AuthLogin   = "login"
	AuthXOAuth2 = "xoauth2"
)

// OAuthAuthority is the Microsoft identity platform. Tests may override it.
var OAuthAuthority = "https://login.microsoftonline.com"

// Config is the SMTP configuration. Secrets are plain text here; the server
// encrypts them before storing.
type Config struct {
	Enabled     bool   `json:"enabled"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"`
	Auth        string `json:"auth"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	OAuthTenant string `json:"oauthTenant"`
	OAuthClient string `json:"oauthClientId"`
	OAuthSecret string `json:"oauthSecret,omitempty"`
	FromName    string `json:"fromName"`
	FromAddress string `json:"fromAddress"`
	HeloName    string `json:"heloName"`
}

// Validate checks the configuration.
func (c Config) Validate() error {
	var errs []string
	if strings.TrimSpace(c.Host) == "" || strings.ContainsAny(c.Host, " /:") {
		errs = append(errs, "host")
	}
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, "port")
	}
	switch c.Security {
	case SecuritySTARTTLS, SecurityTLS, SecurityNone:
	default:
		errs = append(errs, "security")
	}
	switch c.Auth {
	case AuthNone:
	case AuthPlain, AuthLogin:
		if c.Username == "" {
			errs = append(errs, "username")
		}
		if c.Security == SecurityNone {
			errs = append(errs, "security") // never send a password in clear text
		}
	case AuthXOAuth2:
		if c.Username == "" || c.OAuthTenant == "" || c.OAuthClient == "" {
			errs = append(errs, "oauth")
		}
	default:
		errs = append(errs, "auth")
	}
	if _, err := mail.ParseAddress(c.FromAddress); err != nil {
		errs = append(errs, "fromAddress")
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid fields: %s", strings.Join(errs, ", "))
	}
	return nil
}

// Message is one e-mail.
type Message struct {
	To          string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
}

// Attachment is a file sent with a message, such as a PDF report.
type Attachment struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Data        []byte `json:"data"`
}

// Sender sends messages. It caches the OAuth2 token source per configuration.
type Sender struct {
	Timeout time.Duration
}

// Send delivers msg over SMTP.
func (s *Sender) Send(ctx context.Context, cfg Config, msg Message) error {
	if !cfg.Enabled {
		return errors.New("mail is not configured")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	opts := []gomail.Option{
		gomail.WithPort(cfg.Port),
		gomail.WithTimeout(timeout),
		gomail.WithTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.Host}),
	}
	switch cfg.Security {
	case SecurityTLS:
		opts = append(opts, gomail.WithSSL())
	case SecuritySTARTTLS:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	}
	if cfg.HeloName != "" {
		opts = append(opts, gomail.WithHELO(cfg.HeloName))
	}
	switch cfg.Auth {
	case AuthPlain, AuthLogin:
		mech := gomail.SMTPAuthPlain
		if cfg.Auth == AuthLogin {
			mech = gomail.SMTPAuthLogin
		}
		opts = append(opts, gomail.WithSMTPAuth(mech), gomail.WithUsername(cfg.Username), gomail.WithPassword(cfg.Password))
	case AuthXOAuth2:
		token, err := oauthToken(ctx, cfg)
		if err != nil {
			return err
		}
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthXOAUTH2), gomail.WithUsername(cfg.Username), gomail.WithPassword(token))
	}
	client, err := gomail.NewClient(cfg.Host, opts...)
	if err != nil {
		return err
	}
	m := gomail.NewMsg()
	if err := m.FromFormat(cfg.FromName, cfg.FromAddress); err != nil {
		return fmt.Errorf("from: %w", err)
	}
	if err := m.To(msg.To); err != nil {
		return fmt.Errorf("to: %w", err)
	}
	m.Subject(msg.Subject)
	m.SetDate()
	m.SetMessageID()
	m.SetBodyString(gomail.TypeTextPlain, msg.Text)
	if msg.HTML != "" {
		m.AddAlternativeString(gomail.TypeTextHTML, msg.HTML)
	}
	for _, a := range msg.Attachments {
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		m.AttachReadSeeker(a.Name, bytes.NewReader(a.Data), gomail.WithFileContentType(gomail.ContentType(ct)))
	}
	return client.DialAndSendWithContext(ctx, m)
}

// oauthToken gets an access token for SMTP AUTH XOAUTH2 with the client
// credentials flow (Exchange Online application permission SMTP.SendAsApp).
func oauthToken(ctx context.Context, cfg Config) (string, error) {
	cc := clientcredentials.Config{
		ClientID:     cfg.OAuthClient,
		ClientSecret: cfg.OAuthSecret,
		TokenURL:     strings.TrimRight(OAuthAuthority, "/") + "/" + cfg.OAuthTenant + "/oauth2/v2.0/token",
		Scopes:       []string{"https://outlook.office365.com/.default"},
	}
	tok, err := cc.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("oauth2 token: %w", err)
	}
	return tok.AccessToken, nil
}

// ValidAddress reports whether s is a single valid e-mail address.
func ValidAddress(s string) bool {
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}
