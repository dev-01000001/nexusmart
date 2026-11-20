package mailer

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFormatOTPEmailAndHeaderSanitization(t *testing.T) {
	msg := FormatOTPEmail("buyer@example.com\r\nBcc: evil@example.com", "Nguyen Van A", "482910", "2026-11-20T03:00:00Z")
	if !strings.Contains(msg.Subject, "OTP") || !strings.Contains(msg.Subject, "482910") {
		t.Fatalf("expected OTP and code in subject, got %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "482910") {
		t.Fatalf("expected OTP code in body, got %q", msg.Body)
	}

	raw := string(FormatMessage("no-reply@nexusmart.local", msg, time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)))
	if strings.Contains(raw, "\r\nBcc:") {
		t.Fatalf("expected CRLF header injection to be sanitized, got %q", raw)
	}
	if !strings.Contains(raw, "Subject: NexusMart Verification OTP: 482910\r\n") {
		t.Fatalf("expected clean Subject header, got %q", raw)
	}
	if !strings.Contains(raw, "Content-Type: text/plain; charset=UTF-8\r\n") {
		t.Fatalf("expected UTF-8 Content-Type header, got %q", raw)
	}
}

func TestSMTPMailerSendsToLocalSMTPServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake smtp: %v", err)
	}
	defer func() { _ = ln.Close() }()

	var (
		mu       sync.Mutex
		captured string
		rcptTo   string
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		writeLine := func(s string) {
			_, _ = rw.WriteString(s + "\r\n")
			_ = rw.Flush()
		}
		writeLine("220 mailpit.local ESMTP")
		inData := false
		var dataBuf strings.Builder
		for {
			line, readErr := rw.ReadString('\n')
			if readErr != nil {
				return
			}
			if inData {
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					mu.Lock()
					captured = dataBuf.String()
					mu.Unlock()
					writeLine("250 OK queued")
					continue
				}
				dataBuf.WriteString(line)
				continue
			}
			upper := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
				writeLine("250 mailpit.local")
			case strings.HasPrefix(upper, "MAIL FROM:"):
				writeLine("250 OK")
			case strings.HasPrefix(upper, "RCPT TO:"):
				mu.Lock()
				rcptTo = strings.TrimSpace(line)
				mu.Unlock()
				writeLine("250 OK")
			case upper == "DATA":
				inData = true
				writeLine("354 End data with <CR><LF>.<CR><LF>")
			case upper == "QUIT":
				writeLine("221 Bye")
				return
			default:
				writeLine("250 OK")
			}
		}
	}()

	tcpAddr := ln.Addr().(*net.TCPAddr)
	m := NewSMTPMailer("127.0.0.1", tcpAddr.Port, "no-reply@nexusmart.local")
	otpMsg := FormatOTPEmail("buyer@nexusmart.local", "Test Buyer", "654321", "2026-11-20T04:00:00Z")

	if err := m.Send(context.Background(), otpMsg); err != nil {
		t.Fatalf("SMTP Send failed: %v", err)
	}
	<-done

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(rcptTo, "buyer@nexusmart.local") {
		t.Fatalf("unexpected RCPT TO: %q", rcptTo)
	}
	if !strings.Contains(captured, "Subject: NexusMart Verification OTP: 654321") || !strings.Contains(captured, "654321") {
		t.Fatalf("expected OTP email body captured, got %q", captured)
	}
}
