package mailer

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Message represents an outbound plain-text email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender transmits email messages (implemented by SMTPMailer for Mailpit).
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPMailer sends UTF-8 emails via stdlib net/smtp to Mailpit (or any plain SMTP relay).
type SMTPMailer struct {
	host    string
	port    int
	from    string
	timeout time.Duration
}

// NewSMTPMailer creates an SMTPMailer targeting host:port with sender address from.
func NewSMTPMailer(host string, port int, from string) *SMTPMailer {
	if strings.TrimSpace(host) == "" {
		host = "mailpit"
	}
	if port <= 0 {
		port = 1025
	}
	if strings.TrimSpace(from) == "" {
		from = "no-reply@nexusmart.local"
	}
	return &SMTPMailer{
		host:    host,
		port:    port,
		from:    sanitizeHeader(from),
		timeout: 5 * time.Second,
	}
}

// FormatMessage builds a clean RFC 5322 UTF-8 message suitable for Mailpit inspection.
func FormatMessage(from string, msg Message, now time.Time) []byte {
	cleanFrom := sanitizeHeader(from)
	cleanTo := sanitizeHeader(msg.To)
	cleanSubject := sanitizeHeader(msg.Subject)
	msgID := fmt.Sprintf("<%s@nexusmart.local>", uuid.NewString())

	var b strings.Builder
	b.WriteString("From: ")
	b.WriteString(cleanFrom)
	b.WriteString("\r\n")
	b.WriteString("To: ")
	b.WriteString(cleanTo)
	b.WriteString("\r\n")
	b.WriteString("Subject: ")
	b.WriteString(mime.QEncoding.Encode("UTF-8", cleanSubject))
	b.WriteString("\r\n")
	b.WriteString("Date: ")
	b.WriteString(now.UTC().Format(time.RFC1123Z))
	b.WriteString("\r\n")
	b.WriteString("Message-ID: ")
	b.WriteString(msgID)
	b.WriteString("\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(msg.Body, "\r\n", "\n"))
	return []byte(b.String())
}

var vietnamZone = time.FixedZone("GMT+7", 7*3600)

// FormatOTPEmail formats a Vietnamese verification OTP email without leaking the OTP code in the Subject.
func FormatOTPEmail(to, fullName, otp, expiresAt string) Message {
	name := strings.TrimSpace(fullName)
	if name == "" {
		name = "Quý khách"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Xin chào %s,\n\n", name)
	b.WriteString("Cảm ơn bạn đã đăng ký tài khoản tại NexusMart.\n")
	fmt.Fprintf(&b, "Mã xác thực OTP của bạn là: %s\n", strings.TrimSpace(otp))
	if exp := formatOTPExpiry(expiresAt); exp != "" {
		fmt.Fprintf(&b, "Mã có hiệu lực đến: %s\n", exp)
	}
	b.WriteString("\nVui lòng không chia sẻ mã này cho bất kỳ ai. Nếu bạn không yêu cầu tạo tài khoản NexusMart, vui lòng bỏ qua email này.\n")

	return Message{
		To:      to,
		Subject: "Mã xác minh tài khoản NexusMart",
		Body:    b.String(),
	}
}

// FormatPasswordResetEmail formats a Vietnamese password reset email containing resetLink without leaking token in Subject.
func FormatPasswordResetEmail(to, fullName, resetLink, expiresAt string) Message {
	name := strings.TrimSpace(fullName)
	if name == "" {
		name = "Quý khách"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Xin chào %s,\n\n", name)
	b.WriteString("Chúng tôi đã nhận được yêu cầu đặt lại mật khẩu cho tài khoản NexusMart của bạn.\n")
	fmt.Fprintf(&b, "Vui lòng truy cập liên kết sau để đặt lại mật khẩu:\n%s\n", strings.TrimSpace(resetLink))
	if exp := formatOTPExpiry(expiresAt); exp != "" {
		fmt.Fprintf(&b, "\nLiên kết có hiệu lực đến: %s\n", exp)
	}
	b.WriteString("\nVui lòng không chia sẻ liên kết này cho bất kỳ ai. Nếu bạn không gửi yêu cầu này, hãy bỏ qua email và kiểm tra bảo mật tài khoản của bạn.\n")

	return Message{
		To:      to,
		Subject: "Đặt lại mật khẩu tài khoản NexusMart",
		Body:    b.String(),
	}
}

func formatOTPExpiry(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if parsed, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return parsed.In(vietnamZone).Format("15:04 ngày 02/01/2006 (GMT+7)")
	}
	return trimmed
}

// Send dials the SMTP server with context timeout and transmits msg.
func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	to := sanitizeHeader(msg.To)
	if to == "" {
		return errors.New("recipient email is required")
	}

	addr := net.JoinHostPort(m.host, strconv.Itoa(m.port))
	dialer := &net.Dialer{Timeout: m.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(m.timeout))
	}

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err := client.Mail(m.from); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp RCPT TO: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	raw := FormatMessage(m.from, msg, time.Now().UTC())
	if _, err := wc.Write(raw); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp finish DATA: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp QUIT: %w", err)
	}
	return nil
}

func sanitizeHeader(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return strings.TrimSpace(v)
}
