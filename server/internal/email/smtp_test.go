package email

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"tokendance/internal/config"
)

type fakeSMTPServer struct {
	listener       net.Listener
	wg             sync.WaitGroup
	mu             sync.Mutex
	messages       []string
	closeAfterData bool
}

func newFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeSMTPServer{listener: listener}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.wg.Add(1)
			go func() { defer server.wg.Done(); server.serve(conn) }()
		}
	}()
	return server
}

func (s *fakeSMTPServer) close()    { _ = s.listener.Close(); s.wg.Wait() }
func (s *fakeSMTPServer) port() int { return s.listener.Addr().(*net.TCPAddr).Port }
func (s *fakeSMTPServer) latest() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.messages) == 0 {
		return ""
	}
	return s.messages[len(s.messages)-1]
}
func (s *fakeSMTPServer) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
}

func (s *fakeSMTPServer) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) { _, _ = writer.WriteString(line + "\r\n"); _ = writer.Flush() }
	writeLine("220 localhost ESMTP fake")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		command := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch command {
		case "EHLO", "HELO":
			_, _ = writer.WriteString("250-localhost\r\n250 AUTH PLAIN\r\n")
			_ = writer.Flush()
		case "AUTH":
			parts := strings.Split(line, " ")
			if len(parts) != 3 {
				writeLine("535 invalid auth")
				continue
			}
			decoded, _ := base64.StdEncoding.DecodeString(parts[2])
			if string(decoded) != "\x00smtp-user\x00smtp-pass" {
				writeLine("535 invalid auth")
				continue
			}
			writeLine("235 authenticated")
		case "MAIL", "RCPT":
			writeLine("250 accepted")
		case "DATA":
			writeLine("354 end with dot")
			var message strings.Builder
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				message.WriteString(dataLine)
			}
			s.mu.Lock()
			s.messages = append(s.messages, message.String())
			closeAfterData := s.closeAfterData
			s.closeAfterData = false
			s.mu.Unlock()
			if closeAfterData {
				return
			}
			writeLine("250 queued")
		case "QUIT":
			writeLine("221 bye")
			return
		default:
			writeLine("250 ok")
		}
	}
}

func TestSMTPProviderIntegration(t *testing.T) {
	server := newFakeSMTPServer(t)
	defer server.close()
	provider, err := NewSMTPProvider(SMTPOptions{Host: "localhost", Port: server.port(), Username: "smtp-user", Password: "smtp-pass", From: "TokenDance <noreply@example.com>", TLSMode: "none", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	messageID, err := provider.Send(context.Background(), Message{EmailID: "em_123", Recipient: "user@example.com", TemplateKey: "verification_code", Locale: "en-US", PayloadJSON: `{"code":"123456"}`, CreatedAt: time.Now()})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if messageID != "smtp_em_123" {
		t.Fatalf("unexpected provider message ID: %s", messageID)
	}
	delivered := server.latest()
	for _, expected := range []string{"From: \"TokenDance\" <noreply@example.com>", "To: <user@example.com>", "Subject: TokenDance: verification code", `{"code":"123456"}`} {
		if !strings.Contains(delivered, expected) {
			t.Fatalf("message missing %q:\n%s", expected, delivered)
		}
	}
}

func TestSMTPProviderCrashAfterAcceptRetryUsesSameMessageID(t *testing.T) {
	server := newFakeSMTPServer(t)
	defer server.close()
	server.mu.Lock()
	server.closeAfterData = true
	server.mu.Unlock()

	provider, err := NewSMTPProvider(SMTPOptions{Host: "localhost", Port: server.port(), Username: "smtp-user", Password: "smtp-pass", From: "TokenDance <noreply@example.com>", TLSMode: "none", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	msg := Message{EmailID: "em_crash_retry", Recipient: "user@example.com", TemplateKey: "verification_code", Locale: "en-US", PayloadJSON: `{"code":"123456"}`, CreatedAt: time.Now()}
	if _, err := provider.Send(context.Background(), msg); err == nil {
		t.Fatal("expected ambiguous delivery error after server accepted DATA and dropped the connection")
	}
	providerID, err := provider.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("retry send: %v", err)
	}
	if providerID != "smtp_em_crash_retry" {
		t.Fatalf("unexpected deterministic provider ID: %s", providerID)
	}
	messages := server.all()
	if len(messages) != 2 {
		t.Fatalf("expected accepted original and retry, got %d messages", len(messages))
	}
	const expectedHeader = "Message-ID: <smtp_em_crash_retry@tokendance>"
	for index, delivered := range messages {
		if !strings.Contains(delivered, expectedHeader) {
			t.Fatalf("delivery %d missing stable Message-ID:\n%s", index, delivered)
		}
	}
}

func TestEmailProviderConstructorRejectsSinkInProduction(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Environment = "production"
	cfg.EmailProvider = "sink"
	if provider, err := NewProvider(cfg); err == nil || provider != nil {
		t.Fatal("production selected delivery sink")
	}
	cfg.EmailProvider = "smtp"
	cfg.SMTPHost = "smtp.example.com"
	cfg.SMTPPort = 587
	cfg.SMTPUsername = "user"
	cfg.SMTPPassword = "secret"
	cfg.SMTPFrom = "noreply@example.com"
	cfg.SMTPTLSMode = "starttls"
	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("production SMTP constructor failed: %v", err)
	}
	if _, ok := provider.(*DeliverySink); ok {
		t.Fatal("production selected delivery sink")
	}
}
