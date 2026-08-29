package email

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"tokendance/internal/config"
)

type SMTPProvider struct {
	host     string
	port     int
	username string
	password string
	from     mail.Address
	tlsMode  string
	ehloName string
	timeout  time.Duration
}

func NewProvider(cfg *config.Config) (Provider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("email provider config is required")
	}
	switch cfg.EmailProvider {
	case "sink":
		if cfg.Environment != "development" && cfg.Environment != "test" {
			return nil, fmt.Errorf("email sink is only allowed in explicit development or test environments")
		}
		return DefaultSink, nil
	case "smtp":
		return NewSMTPProvider(SMTPOptions{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword, From: cfg.SMTPFrom, TLSMode: cfg.SMTPTLSMode,
			EHLOName: cfg.SMTPEHLOName,
		})
	default:
		return nil, fmt.Errorf("unsupported email provider %q", cfg.EmailProvider)
	}
}

type SMTPOptions struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string
	EHLOName string
	Timeout  time.Duration
}

func NewSMTPProvider(opts SMTPOptions) (*SMTPProvider, error) {
	if strings.TrimSpace(opts.Host) == "" || opts.Port < 1 || opts.Port > 65535 {
		return nil, fmt.Errorf("valid SMTP host and port are required")
	}
	if opts.Username == "" || opts.Password == "" {
		return nil, fmt.Errorf("SMTP credentials are required")
	}
	from, err := mail.ParseAddress(opts.From)
	if err != nil {
		return nil, fmt.Errorf("invalid SMTP from address: %w", err)
	}
	if opts.TLSMode != "starttls" && opts.TLSMode != "tls" && opts.TLSMode != "none" {
		return nil, fmt.Errorf("SMTP TLS mode must be starttls, tls, or none")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	return &SMTPProvider{host: opts.Host, port: opts.Port, username: opts.Username, password: opts.Password, from: *from, tlsMode: opts.TLSMode, ehloName: opts.EHLOName, timeout: opts.Timeout}, nil
}

func (p *SMTPProvider) Send(ctx context.Context, msg Message) (string, error) {
	recipient, err := mail.ParseAddress(msg.Recipient)
	if err != nil {
		return "", &ProviderError{Code: "INVALID_RECIPIENT", Message: "recipient address is invalid", Err: err}
	}
	if strings.TrimSpace(msg.EmailID) == "" || hasHeaderInjection(msg.EmailID) || hasHeaderInjection(msg.TemplateKey) || hasHeaderInjection(msg.Locale) {
		return "", &ProviderError{Code: "INVALID_MESSAGE", Message: "message contains invalid header data"}
	}

	conn, err := p.dial(ctx)
	if err != nil {
		return "", transientSMTPError("SMTP_CONNECT_FAILED", "connect to SMTP server", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(p.timeout))
	}

	client, err := smtp.NewClient(conn, p.host)
	if err != nil {
		return "", transientSMTPError("SMTP_HANDSHAKE_FAILED", "start SMTP session", err)
	}
	defer client.Close()
	if p.ehloName != "" {
		if err := client.Hello(p.ehloName); err != nil {
			return "", transientSMTPError("SMTP_EHLO_FAILED", "send SMTP EHLO", err)
		}
	}
	if p.tlsMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return "", transientSMTPError("SMTP_STARTTLS_UNAVAILABLE", "SMTP server does not advertise STARTTLS", nil)
		}
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.host}); err != nil {
			return "", transientSMTPError("SMTP_STARTTLS_FAILED", "secure SMTP connection", err)
		}
	}
	if err := client.Auth(smtp.PlainAuth("", p.username, p.password, p.host)); err != nil {
		return "", &ProviderError{Code: "SMTP_AUTH_FAILED", Message: "authenticate to SMTP server", Err: err}
	}
	if err := client.Mail(p.from.Address); err != nil {
		return "", classifySMTPError("SMTP_SENDER_REJECTED", "SMTP sender rejected", err)
	}
	if err := client.Rcpt(recipient.Address); err != nil {
		return "", classifySMTPError("SMTP_RECIPIENT_REJECTED", "SMTP recipient rejected", err)
	}
	writer, err := client.Data()
	if err != nil {
		return "", classifySMTPError("SMTP_DATA_REJECTED", "SMTP message rejected", err)
	}
	providerID := fmt.Sprintf("smtp_%s", msg.EmailID)
	if err := writeSMTPMessage(writer, p.from, *recipient, msg, providerID); err != nil {
		_ = writer.Close()
		return "", transientSMTPError("SMTP_WRITE_FAILED", "write SMTP message", err)
	}
	if err := writer.Close(); err != nil {
		return "", classifySMTPError("SMTP_DELIVERY_FAILED", "commit SMTP message", err)
	}
	if err := client.Quit(); err != nil {
		return "", transientSMTPError("SMTP_QUIT_FAILED", "finish SMTP session", err)
	}
	return providerID, nil
}

func (p *SMTPProvider) dial(ctx context.Context) (net.Conn, error) {
	address := net.JoinHostPort(p.host, fmt.Sprintf("%d", p.port))
	dialer := &net.Dialer{Timeout: p.timeout}
	if p.tlsMode == "tls" {
		return (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.host}}).DialContext(ctx, "tcp", address)
	}
	return dialer.DialContext(ctx, "tcp", address)
}

func writeSMTPMessage(w io.Writer, from, recipient mail.Address, msg Message, providerID string) error {
	bw := bufio.NewWriter(w)
	headers := []string{
		"From: " + from.String(),
		"To: " + recipient.String(),
		"Subject: " + smtpSubject(msg.TemplateKey),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID: <" + providerID + "@tokendance>",
		"MIME-Version: 1.0",
		"Content-Type: application/json; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	for _, header := range headers {
		if _, err := bw.WriteString(header + "\r\n"); err != nil {
			return err
		}
	}
	if _, err := bw.WriteString("\r\n" + normalizeCRLF(msg.PayloadJSON) + "\r\n"); err != nil {
		return err
	}
	return bw.Flush()
}

func smtpSubject(templateKey string) string {
	if templateKey == "" {
		return "TokenDance notification"
	}
	return "TokenDance: " + strings.ReplaceAll(templateKey, "_", " ")
}

func hasHeaderInjection(value string) bool { return strings.ContainsAny(value, "\r\n") }
func normalizeCRLF(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\n", "\r\n")
}

func transientSMTPError(code, message string, err error) error {
	return &ProviderError{Code: code, Message: message, Transient: true, Err: err}
}

func classifySMTPError(code, message string, err error) error {
	transient := false
	if text, ok := err.(*textproto.Error); ok {
		transient = text.Code >= 400 && text.Code < 500
	}
	return &ProviderError{Code: code, Message: message, Transient: transient, Err: err}
}
