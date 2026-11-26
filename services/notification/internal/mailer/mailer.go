package mailer

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const defaultSMTPTimeout = 10 * time.Second

// Message represents an outbound plain-text email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender transmits email messages (implemented by SMTPMailer for Mailpit or real SMTP providers).
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPMailer sends UTF-8 emails via stdlib net/smtp (plain for Mailpit when username is empty,
// or mandatory TLS/STARTTLS + AUTH PLAIN when credentials are configured).
type SMTPMailer struct {
	host        string
	port        int
	fromAddr    string
	fromHeader  string
	fromDomain  string
	username    string
	password    string
	timeout     time.Duration
	tlsConfig   *tls.Config
	implicitTLS bool
}

// NewSMTPMailer creates an SMTPMailer targeting host:port with sender address from and optional credentials.
// When username is empty, plain SMTP is used (Mailpit/e2e). When username is set, password is required
// and TLS (implicit on ports 465/2465, mandatory STARTTLS otherwise) + AUTH PLAIN are enforced.
func NewSMTPMailer(host string, port int, from, username, password string) (*SMTPMailer, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "mailpit"
	}
	if port <= 0 {
		port = 1025
	}

	username = strings.TrimSpace(username)
	hasUser := username != ""
	hasPass := strings.TrimSpace(password) != ""
	if hasUser != hasPass {
		return nil, errors.New("smtp credentials require both username and password")
	}

	fromAddr, fromHeader, fromDomain, err := parseFromAddress(from)
	if err != nil {
		return nil, err
	}

	return &SMTPMailer{
		host:       host,
		port:       port,
		fromAddr:   fromAddr,
		fromHeader: fromHeader,
		fromDomain: fromDomain,
		username:   username,
		password:   password,
		timeout:    defaultSMTPTimeout,
	}, nil
}

// FormatMessage builds a clean RFC 5322 UTF-8 message suitable for Mailpit or real SMTP relays.
func FormatMessage(from string, msg Message, now time.Time) []byte {
	fromHeader, fromDomain := formatFromAndDomain(from)
	return formatMessage(fromHeader, fromDomain, msg, now)
}

func formatMessage(fromHeader, fromDomain string, msg Message, now time.Time) []byte {
	cleanFrom := sanitizeHeader(fromHeader)
	cleanTo := sanitizeHeader(msg.To)
	cleanSubject := sanitizeHeader(msg.Subject)
	if fromDomain == "" {
		fromDomain = "nexusmart.local"
	}
	msgID := fmt.Sprintf("<%s@%s>", uuid.NewString(), fromDomain)

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

// FormatShopBankAccountOTPEmail formats a Vietnamese bank-account OTP email for sellers without leaking OTP in Subject.
func FormatShopBankAccountOTPEmail(to, fullName, otp, expiresAt, bankCode, accountNumberMasked string) Message {
	name := strings.TrimSpace(fullName)
	if name == "" {
		name = "Quý khách"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Xin chào %s,\n\n", name)
	b.WriteString("Bạn vừa yêu cầu thêm hoặc cập nhật tài khoản ngân hàng nhận tiền cho gian hàng trên NexusMart.\n")
	if bc := strings.TrimSpace(bankCode); bc != "" {
		fmt.Fprintf(&b, "Ngân hàng: %s\n", bc)
	}
	if masked := strings.TrimSpace(accountNumberMasked); masked != "" {
		fmt.Fprintf(&b, "Số tài khoản: %s\n", masked)
	}
	fmt.Fprintf(&b, "Mã xác thực OTP của bạn là: %s\n", strings.TrimSpace(otp))
	if exp := formatOTPExpiry(expiresAt); exp != "" {
		fmt.Fprintf(&b, "Mã có hiệu lực đến: %s\n", exp)
	}
	b.WriteString("\nVui lòng không chia sẻ mã này cho bất kỳ ai. Nếu bạn không thực hiện yêu cầu này, hãy kiểm tra bảo mật tài khoản ngay lập tức.\n")

	return Message{
		To:      to,
		Subject: "Mã xác thực tài khoản ngân hàng gian hàng NexusMart",
		Body:    b.String(),
	}
}

// FormatPhoneOTPEmail formats a Vietnamese SMS-mock phone verification OTP email without leaking OTP in Subject.
func FormatPhoneOTPEmail(to, fullName, phoneMasked, otp, expiresAt string) Message {
	name := strings.TrimSpace(fullName)
	if name == "" {
		name = "Quý khách"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Xin chào %s,\n\n", name)
	b.WriteString("Bạn vừa yêu cầu xác minh số điện thoại cho tài khoản NexusMart (tin nhắn SMS mô phỏng).\n")
	if masked := strings.TrimSpace(phoneMasked); masked != "" {
		fmt.Fprintf(&b, "Số điện thoại: %s\n", masked)
	}
	fmt.Fprintf(&b, "Mã xác thực OTP của bạn là: %s\n", strings.TrimSpace(otp))
	if exp := formatOTPExpiry(expiresAt); exp != "" {
		fmt.Fprintf(&b, "Mã có hiệu lực đến: %s\n", exp)
	}
	b.WriteString("\nVui lòng không chia sẻ mã này cho bất kỳ ai. Nếu bạn không thực hiện yêu cầu này, hãy bỏ qua thông báo này.\n")

	return Message{
		To:      to,
		Subject: "Mã xác minh số điện thoại NexusMart (SMS mô phỏng)",
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
	rcptAddr := to
	if parsedTo, err := mail.ParseAddress(to); err == nil {
		rcptAddr = parsedTo.Address
	}

	if m.username != "" && isReservedDomain(rcptAddr) {
		return nil
	}

	sendCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	deadline, _ := sendCtx.Deadline()

	addr := net.JoinHostPort(m.host, strconv.Itoa(m.port))
	dialer := &net.Dialer{Timeout: m.timeout}

	var (
		conn net.Conn
		err  error
	)
	if m.username != "" && m.useImplicitTLS() {
		tlsDialer := &tls.Dialer{
			NetDialer: dialer,
			Config:    m.buildTLSConfig(),
		}
		conn, err = tlsDialer.DialContext(sendCtx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dial smtp tls %s: %w", addr, err)
		}
	} else {
		conn, err = dialer.DialContext(sendCtx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dial smtp %s: %w", addr, err)
		}
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(deadline)
	stopCancel := context.AfterFunc(sendCtx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopCancel()

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if m.username != "" {
		if !m.useImplicitTLS() {
			if ok, _ := client.Extension("STARTTLS"); !ok {
				return errors.New("smtp server does not advertise STARTTLS")
			}
			if err := client.StartTLS(m.buildTLSConfig()); err != nil {
				return fmt.Errorf("smtp STARTTLS: %w", err)
			}
		}
		if state, ok := client.TLSConnectionState(); !ok || !state.HandshakeComplete {
			return errors.New("smtp TLS handshake required before AUTH")
		}
		auth := smtp.PlainAuth("", m.username, m.password, m.host)
		if err := client.Auth(auth); err != nil {
			return sanitizeAuthError(err, m.username, m.password)
		}
	}

	if err := client.Mail(m.fromAddr); err != nil {
		return sanitizeSendError("smtp MAIL FROM", err, m.password, to, rcptAddr, msg.Subject)
	}
	if err := client.Rcpt(rcptAddr); err != nil {
		return sanitizeSendError("smtp RCPT TO", err, m.password, to, rcptAddr, msg.Subject)
	}
	wc, err := client.Data()
	if err != nil {
		return sanitizeSendError("smtp DATA", err, m.password, to, rcptAddr, msg.Subject)
	}
	raw := formatMessage(m.fromHeader, m.fromDomain, msg, time.Now().UTC())
	if _, err := wc.Write(raw); err != nil {
		_ = wc.Close()
		return sanitizeSendError("smtp write body", err, m.password, to, rcptAddr, msg.Subject)
	}
	if err := wc.Close(); err != nil {
		return sanitizeSendError("smtp finish DATA", err, m.password, to, rcptAddr, msg.Subject)
	}
	// DATA already succeeded with 250 OK; ignore best-effort QUIT errors so early peer close or
	// post-DATA timeouts do not fail Send and trigger duplicate email retries.
	_ = client.Quit()
	return nil
}

func (m *SMTPMailer) useImplicitTLS() bool {
	return m.implicitTLS || m.port == 465 || m.port == 2465
}

func (m *SMTPMailer) buildTLSConfig() *tls.Config {
	var cfg *tls.Config
	if m.tlsConfig != nil {
		cfg = m.tlsConfig.Clone()
	} else {
		cfg = &tls.Config{}
	}
	cfg.ServerName = m.host
	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}
	cfg.InsecureSkipVerify = false
	return cfg
}

func parseFromAddress(raw string) (addr, header, domain string, err error) {
	if strings.ContainsAny(raw, "\r\n") {
		return "", "", "", errors.New("invalid SMTP_FROM address: contains newline")
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", "", errors.New("invalid SMTP_FROM address: empty")
	}
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", "", "", fmt.Errorf("invalid SMTP_FROM address: %w", err)
	}
	domain = extractEmailDomain(parsed.Address)
	if domain == "" {
		return "", "", "", errors.New("invalid SMTP_FROM address: missing domain")
	}
	header = parsed.Address
	if parsed.Name != "" {
		header = parsed.String()
	}
	return parsed.Address, header, domain, nil
}

func formatFromAndDomain(from string) (string, string) {
	cleanFrom := sanitizeHeader(from)
	addr, err := mail.ParseAddress(cleanFrom)
	if err != nil {
		return cleanFrom, "nexusmart.local"
	}
	header := addr.Address
	if addr.Name != "" {
		header = addr.String()
	}
	domain := extractEmailDomain(addr.Address)
	if domain == "" {
		domain = "nexusmart.local"
	}
	return header, domain
}

func extractEmailDomain(addr string) string {
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 || at >= len(addr)-1 {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(addr[at+1:]), "."))
}

var reservedDomains = []string{
	"local",
	"test",
	"invalid",
	"example",
	"localhost",
	"example.com",
	"example.net",
	"example.org",
}

func isReservedDomain(recipient string) bool {
	addr := recipient
	if parsed, err := mail.ParseAddress(recipient); err == nil {
		addr = parsed.Address
	}
	domain := extractEmailDomain(addr)
	if domain == "" {
		return false
	}
	for _, reserved := range reservedDomains {
		if domain == reserved || strings.HasSuffix(domain, "."+reserved) {
			return true
		}
	}
	return false
}

func sanitizeAuthError(err error, username, password string) error {
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		return fmt.Errorf("smtp AUTH failed (code %d)", tpErr.Code)
	}
	encodedPlain := base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password))
	encodedPass := base64.StdEncoding.EncodeToString([]byte(password))
	return fmt.Errorf("smtp AUTH: %s", scrubValues(err.Error(), password, encodedPlain, encodedPass))
}

func sanitizeSendError(op string, err error, secrets ...string) error {
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		return fmt.Errorf("%s failed (code %d)", op, tpErr.Code)
	}
	return fmt.Errorf("%s: %s", op, scrubValues(err.Error(), secrets...))
}

func scrubValues(s string, values ...string) string {
	for _, v := range values {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
	}
	return s
}

func sanitizeHeader(v string) string {
	v = strings.ReplaceAll(v, "\r", "")
	v = strings.ReplaceAll(v, "\n", "")
	return strings.TrimSpace(v)
}
