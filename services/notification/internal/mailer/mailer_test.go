package mailer

import (
	"bufio"
	"context"
	"mime"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFormatOTPEmailAndHeaderSanitization(t *testing.T) {
	msg := FormatOTPEmail("buyer@example.com\r\nBcc: evil@example.com", "Nguyen Van A", "482910", "2026-11-20T03:00:00Z")
	if strings.Contains(msg.Subject, "482910") {
		t.Fatalf("OTP code must not appear in subject, got %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, "482910") {
		t.Fatalf("expected OTP code in body, got %q", msg.Body)
	}

	raw := string(FormatMessage("no-reply@nexusmart.local", msg, time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC)))
	if strings.Contains(raw, "\r\nBcc:") {
		t.Fatalf("expected CRLF header injection to be sanitized, got %q", raw)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse RFC 5322 message: %v", err)
	}
	decodedSubject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode Subject header: %v", err)
	}
	if decodedSubject != "Mã xác minh tài khoản NexusMart" {
		t.Fatalf("expected Vietnamese Subject header, got %q", decodedSubject)
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
	parsed, err := mail.ReadMessage(strings.NewReader(captured))
	if err != nil {
		t.Fatalf("parse captured SMTP message: %v", err)
	}
	decodedSubject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("decode captured Subject: %v", err)
	}
	if decodedSubject != "Mã xác minh tài khoản NexusMart" || strings.Contains(parsed.Header.Get("Subject"), "654321") {
		t.Fatalf("unexpected captured Subject header raw=%q decoded=%q", parsed.Header.Get("Subject"), decodedSubject)
	}
	if !strings.Contains(captured, "654321") {
		t.Fatalf("expected OTP email body captured, got %q", captured)
	}
}

func TestFormatOTPEmailHidesOTPFromSubjectAndUsesVietnamese(t *testing.T) {
	sixDigitsRe := regexp.MustCompile(`\d{6}`)
	e2eOTPRe := regexp.MustCompile(`(?i)(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b`)

	msg := FormatOTPEmail("buyer@nexusmart.local", "Nguyễn Văn A", "482910", "2026-11-20T03:00:00Z")

	if sixDigitsRe.MatchString(msg.Subject) {
		t.Errorf("PRM-B7: OTP subject must not contain any 6-digit sequence, got %q", msg.Subject)
	}
	if strings.Contains(strings.ToLower(msg.Subject), "verification") {
		t.Errorf("PRM-B7: OTP subject must be in Vietnamese, got %q", msg.Subject)
	}
	if strings.Contains(msg.Body, "Hello ") || strings.Contains(msg.Body, "Your NexusMart verification OTP") {
		t.Errorf("PRM-B7: OTP body must be in natural Vietnamese, got %q", msg.Body)
	}
	m := e2eOTPRe.FindStringSubmatch(msg.Body)
	if len(m) != 2 || m[1] != "482910" {
		t.Errorf("PRM-B7: OTP body must allow extracting 6-digit OTP cleanly, got %q", msg.Body)
	}

	raw := FormatMessage("no-reply@nexusmart.local", msg, time.Date(2026, 11, 20, 2, 0, 0, 0, time.UTC))
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse RFC 5322 message: %v", err)
	}
	rawSubject := parsed.Header.Get("Subject")
	if sixDigitsRe.MatchString(rawSubject) {
		t.Errorf("PRM-B7: raw Subject header must not contain 6-digit OTP, got %q", rawSubject)
	}
	decodedSubject, err := new(mime.WordDecoder).DecodeHeader(rawSubject)
	if err != nil {
		t.Fatalf("decode RFC 2047 Subject %q: %v", rawSubject, err)
	}
	if decodedSubject != msg.Subject {
		t.Errorf("decoded Subject mismatch: got %q, want %q", decodedSubject, msg.Subject)
	}
}

func TestFormatPasswordResetEmailVietnameseAndHidesTokenFromSubject(t *testing.T) {
	token := "secret-reset-tok-9988776655"
	link := "https://shop.nexusmart.vn/dat-lai-mat-khau?token=" + token

	msg := FormatPasswordResetEmail("user@nexusmart.local", "Trần Minh Quân", link, "2026-11-23T10:30:00Z")
	if msg.To != "user@nexusmart.local" {
		t.Errorf("To = %q, want user@nexusmart.local", msg.To)
	}
	if msg.Subject == "" || strings.Contains(msg.Subject, token) || strings.Contains(strings.ToLower(msg.Subject), "reset password") {
		t.Errorf("password reset Subject must be Vietnamese and never contain token, got %q", msg.Subject)
	}
	if !strings.Contains(msg.Body, link) {
		t.Errorf("password reset Body must contain reset link %q, got %q", link, msg.Body)
	}
	if !strings.Contains(msg.Body, "Trần Minh Quân") || !strings.Contains(msg.Body, "17:30 ngày 23/11/2026 (GMT+7)") {
		t.Errorf("password reset Body must greet user and format GMT+7 expiry, got %q", msg.Body)
	}

	raw := FormatMessage("no-reply@nexusmart.local", msg, time.Date(2026, 11, 23, 10, 15, 0, 0, time.UTC))
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse RFC 5322 message: %v", err)
	}
	if strings.Contains(parsed.Header.Get("Subject"), token) {
		t.Errorf("raw Subject header must never contain token, got %q", parsed.Header.Get("Subject"))
	}
}
