package mailer

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
	"math/big"
	"mime"
	"net"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedCmd struct {
	line string
	tls  bool
}

type fakeSMTPServer struct {
	addr       *net.TCPAddr
	ln         net.Listener
	closeOnce  sync.Once
	wg         sync.WaitGroup
	mu         sync.Mutex
	connected  int
	commands   []recordedCmd
	authPlain  string
	authWasTLS bool
	mailFrom   string
	rcptTo     string
	captured   string
}

type fakeSMTPOptions struct {
	advertiseSTARTTLS bool
	implicitTLS       bool
	serverTLSConfig   *tls.Config
	authRejectLine    string
	dataRejectLine    string
	closeAfterData250 bool
}

func startFakeSMTPServer(t *testing.T, opts fakeSMTPOptions) *fakeSMTPServer {
	t.Helper()

	var (
		ln  net.Listener
		err error
	)
	if opts.implicitTLS {
		ln, err = tls.Listen("tcp", "127.0.0.1:0", opts.serverTLSConfig)
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatalf("listen fake smtp: %v", err)
	}

	s := &fakeSMTPServer{
		addr: ln.Addr().(*net.TCPAddr),
		ln:   ln,
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			s.mu.Lock()
			s.connected++
			s.mu.Unlock()

			s.wg.Add(1)
			go func(c net.Conn) {
				defer s.wg.Done()
				s.serveConn(c, opts)
			}(conn)
		}
	}()

	t.Cleanup(func() {
		s.Close()
	})
	return s
}

func (s *fakeSMTPServer) Close() {
	s.closeOnce.Do(func() {
		_ = s.ln.Close()
	})
	s.wg.Wait()
}

func (s *fakeSMTPServer) serveConn(conn net.Conn, opts fakeSMTPOptions) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	isTLS := opts.implicitTLS
	if tlsConn, ok := conn.(*tls.Conn); ok {
		if err := tlsConn.Handshake(); err != nil {
			return
		}
		isTLS = true
	}

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	writeLine := func(line string) bool {
		if _, err := rw.WriteString(line + "\r\n"); err != nil {
			return false
		}
		return rw.Flush() == nil
	}

	if !writeLine("220 fake.smtp.local ESMTP") {
		return
	}

	inData := false
	var dataBuf strings.Builder
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if strings.TrimRight(line, "\r\n") == "." {
				inData = false
				s.mu.Lock()
				s.captured = dataBuf.String()
				s.mu.Unlock()
				if opts.dataRejectLine != "" {
					if !writeLine(opts.dataRejectLine) {
						return
					}
					continue
				}
				if !writeLine("250 OK queued") {
					return
				}
				if opts.closeAfterData250 {
					return
				}
				continue
			}
			dataBuf.WriteString(line)
			continue
		}

		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		s.mu.Lock()
		s.commands = append(s.commands, recordedCmd{line: trimmed, tls: isTLS})
		s.mu.Unlock()

		switch {
		case strings.HasPrefix(upper, "EHLO"):
			if opts.advertiseSTARTTLS && !isTLS {
				if !writeLine("250-fake.smtp.local") || !writeLine("250-STARTTLS") || !writeLine("250 AUTH PLAIN") {
					return
				}
			} else if isTLS || opts.advertiseSTARTTLS || opts.authRejectLine != "" {
				if !writeLine("250-fake.smtp.local") || !writeLine("250 AUTH PLAIN") {
					return
				}
			} else {
				if !writeLine("250 fake.smtp.local") {
					return
				}
			}
		case strings.HasPrefix(upper, "HELO"):
			if !writeLine("250 fake.smtp.local") {
				return
			}
		case upper == "STARTTLS":
			if !opts.advertiseSTARTTLS || opts.serverTLSConfig == nil {
				if !writeLine("502 Command not implemented") {
					return
				}
				continue
			}
			if !writeLine("220 Ready to start TLS") {
				return
			}
			tlsConn := tls.Server(conn, opts.serverTLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
			isTLS = true
			rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			parts := strings.Fields(trimmed)
			var payload string
			if len(parts) >= 3 {
				if raw, decErr := base64.StdEncoding.DecodeString(parts[2]); decErr == nil {
					payload = string(raw)
				}
			}
			s.mu.Lock()
			s.authPlain = payload
			s.authWasTLS = isTLS
			s.mu.Unlock()
			if opts.authRejectLine != "" {
				if !writeLine(opts.authRejectLine) {
					return
				}
				continue
			}
			if !writeLine("235 2.7.0 Authentication successful") {
				return
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			s.mu.Lock()
			s.mailFrom = trimmed
			s.mu.Unlock()
			if !writeLine("250 OK") {
				return
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			s.mu.Lock()
			s.rcptTo = trimmed
			s.mu.Unlock()
			if !writeLine("250 OK") {
				return
			}
		case upper == "DATA":
			inData = true
			if !writeLine("354 End data with <CR><LF>.<CR><LF>") {
				return
			}
		case upper == "QUIT":
			_ = writeLine("221 Bye")
			return
		default:
			if !writeLine("250 OK") {
				return
			}
		}
	}
}

func generateTestTLSConfigs(t *testing.T) (serverCfg *tls.Config, trustedClientCfg *tls.Config) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	tlsCert := tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  priv,
		Leaf:        cert,
	}
	return &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
			MinVersion:   tls.VersionTLS12,
		}, &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}
}

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

func TestFormatMessageSubjectEncodingAndMessageIDDomain(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	viSubject := "NexusMart - Shop review update (Cửa hàng Áo Dài): approved"
	viRaw := string(FormatMessage("NexusMart <no-reply@nexusmartai.store>", Message{
		To:      "seller@gmail.com",
		Subject: viSubject,
		Body:    "Xin chào",
	}, now))

	var subjectLine, msgIDLine string
	for _, line := range strings.Split(viRaw, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subjectLine = strings.TrimPrefix(line, "Subject: ")
		}
		if strings.HasPrefix(line, "Message-ID: ") {
			msgIDLine = strings.TrimPrefix(line, "Message-ID: ")
		}
	}
	if !strings.Contains(subjectLine, "=?UTF-8?q?") {
		t.Fatalf("expected Q-encoded UTF-8 subject, got %q", subjectLine)
	}
	decoded, err := (&mime.WordDecoder{}).DecodeHeader(subjectLine)
	if err != nil {
		t.Fatalf("decode Q-encoded subject: %v", err)
	}
	if decoded != viSubject {
		t.Fatalf("decoded subject = %q, want %q", decoded, viSubject)
	}
	if !strings.HasSuffix(msgIDLine, "@nexusmartai.store>") {
		t.Fatalf("expected Message-ID domain nexusmartai.store, got %q", msgIDLine)
	}

	asciiMsg := Message{
		To:      "buyer@gmail.com",
		Subject: "NexusMart Order #12345 Confirmed",
		Body:    "Confirmed",
	}
	asciiRaw := string(FormatMessage("NexusMart <no-reply@nexusmartai.store>", asciiMsg, now))
	if !strings.Contains(asciiRaw, "Subject: NexusMart Order #12345 Confirmed\r\n") {
		t.Fatalf("expected ASCII subject unchanged, got %q", asciiRaw)
	}
}

func TestSMTPMailerSendsToLocalSMTPServer(t *testing.T) {
	srv := startFakeSMTPServer(t, fakeSMTPOptions{})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "no-reply@nexusmart.local", "", "")
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	otpMsg := FormatOTPEmail("buyer@nexusmart.local", "Test Buyer", "654321", "2026-11-20T04:00:00Z")

	if err := m.Send(context.Background(), otpMsg); err != nil {
		t.Fatalf("SMTP Send failed: %v", err)
	}
	srv.Close()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !strings.Contains(srv.rcptTo, "buyer@nexusmart.local") {
		t.Fatalf("unexpected RCPT TO: %q", srv.rcptTo)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(srv.captured))
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
	if !strings.Contains(srv.captured, "654321") {
		t.Fatalf("expected OTP email body captured, got %q", srv.captured)
	}
}

func TestSMTPMailerRejectsServerWithoutSTARTTLS(t *testing.T) {
	// Server on 127.0.0.1 advertises AUTH PLAIN but NOT STARTTLS.
	// Go's smtp.PlainAuth allows plaintext auth on 127.0.0.1, so mailer must explicitly enforce STARTTLS.
	srv := startFakeSMTPServer(t, fakeSMTPOptions{
		advertiseSTARTTLS: false,
	})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "NexusMart <no-reply@nexusmartai.store>", "resend", "re_secret_test_key")
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}

	err = m.Send(context.Background(), Message{
		To:      "customer@gmail.com",
		Subject: "Test",
		Body:    "Hello",
	})
	if err == nil {
		t.Fatalf("expected error when server does not advertise STARTTLS")
	}
	srv.Close()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, cmd := range srv.commands {
		upper := strings.ToUpper(cmd.line)
		if strings.HasPrefix(upper, "AUTH") || strings.HasPrefix(upper, "MAIL FROM") {
			t.Fatalf("server must not receive AUTH or MAIL FROM without STARTTLS, got %q", cmd.line)
		}
	}
}

func TestSMTPMailerSTARTTLSAndAuthPlain(t *testing.T) {
	serverTLS, clientTLS := generateTestTLSConfigs(t)
	srv := startFakeSMTPServer(t, fakeSMTPOptions{
		advertiseSTARTTLS: true,
		serverTLSConfig:   serverTLS,
	})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "NexusMart <no-reply@nexusmartai.store>", "resend", "re_starttls_pass")
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	m.tlsConfig = clientTLS

	msg := FormatOTPEmail("buyer@gmail.com", "Nguyen Van A", "123456", "")
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("STARTTLS Send failed: %v", err)
	}
	srv.Close()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.authWasTLS {
		t.Fatalf("expected AUTH PLAIN to be sent only after TLS upgrade")
	}
	if srv.authPlain != "\x00resend\x00re_starttls_pass" {
		t.Fatalf("unexpected AUTH PLAIN payload: %q", srv.authPlain)
	}
	if !strings.Contains(srv.captured, "123456") {
		t.Fatalf("expected DATA payload captured, got %q", srv.captured)
	}
}

func TestSMTPMailerImplicitTLSAndAuthPlain(t *testing.T) {
	serverTLS, clientTLS := generateTestTLSConfigs(t)
	srv := startFakeSMTPServer(t, fakeSMTPOptions{
		implicitTLS:     true,
		serverTLSConfig: serverTLS,
	})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "NexusMart <no-reply@nexusmartai.store>", "resend", "re_implicit_pass")
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	m.tlsConfig = clientTLS
	m.implicitTLS = true

	msg := FormatOTPEmail("buyer@gmail.com", "Nguyen Van B", "789012", "")
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("implicit TLS Send failed: %v", err)
	}
	srv.Close()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !srv.authWasTLS {
		t.Fatalf("expected AUTH PLAIN over implicit TLS")
	}
	if srv.authPlain != "\x00resend\x00re_implicit_pass" {
		t.Fatalf("unexpected AUTH PLAIN payload: %q", srv.authPlain)
	}
	if !strings.Contains(srv.captured, "789012") {
		t.Fatalf("expected DATA payload captured, got %q", srv.captured)
	}

	// Verify ports 465 and 2465 automatically select implicit TLS while 587/2587 do not.
	for _, tc := range []struct {
		port int
		want bool
	}{
		{465, true},
		{2465, true},
		{587, false},
		{2587, false},
		{25, false},
	} {
		pm, pmErr := NewSMTPMailer("smtp.resend.com", tc.port, "no-reply@nexusmartai.store", "resend", "secret")
		if pmErr != nil {
			t.Fatalf("NewSMTPMailer port %d: %v", tc.port, pmErr)
		}
		if got := pm.useImplicitTLS(); got != tc.want {
			t.Fatalf("port %d useImplicitTLS() = %v, want %v", tc.port, got, tc.want)
		}
	}
}

func TestSMTPMailerRejectsUntrustedCertificate(t *testing.T) {
	serverTLS, _ := generateTestTLSConfigs(t)
	srv := startFakeSMTPServer(t, fakeSMTPOptions{
		advertiseSTARTTLS: true,
		serverTLSConfig:   serverTLS,
	})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "no-reply@nexusmartai.store", "resend", "re_untrusted_secret")
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	// Empty RootCAs pool guarantees the self-signed server cert is untrusted.
	m.tlsConfig = &tls.Config{RootCAs: x509.NewCertPool()}

	err = m.Send(context.Background(), Message{
		To:      "buyer@gmail.com",
		Subject: "Test",
		Body:    "Hello",
	})
	if err == nil {
		t.Fatalf("expected TLS verification error for untrusted certificate")
	}
	srv.Close()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, cmd := range srv.commands {
		if strings.HasPrefix(strings.ToUpper(cmd.line), "AUTH") {
			t.Fatalf("AUTH must not be sent when certificate is untrusted")
		}
	}
}

func TestSMTPMailerAuth535DoesNotLeakPassword(t *testing.T) {
	const secretPass = "re_super_secret_api_key_999"
	serverTLS, clientTLS := generateTestTLSConfigs(t)
	srv := startFakeSMTPServer(t, fakeSMTPOptions{
		advertiseSTARTTLS: true,
		serverTLSConfig:   serverTLS,
		authRejectLine:    "535 5.7.8 Authentication failed: bad password " + secretPass,
	})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "no-reply@nexusmartai.store", "resend", secretPass)
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	m.tlsConfig = clientTLS

	err = m.Send(context.Background(), Message{
		To:      "buyer@gmail.com",
		Subject: "Test",
		Body:    "Hello",
	})
	if err == nil {
		t.Fatalf("expected error on 535 AUTH failure")
	}
	if strings.Contains(err.Error(), secretPass) {
		t.Fatalf("error leaked SMTP password: %v", err)
	}
	encodedCreds := base64.StdEncoding.EncodeToString([]byte("\x00resend\x00" + secretPass))
	if strings.Contains(err.Error(), encodedCreds) {
		t.Fatalf("error leaked base64 SMTP credentials: %v", err)
	}
	if !strings.Contains(err.Error(), "535") {
		t.Fatalf("expected SMTP code 535 in error, got %v", err)
	}
}

func TestSMTPMailerFromDisplayNameUsesBareAddressInEnvelope(t *testing.T) {
	serverTLS, clientTLS := generateTestTLSConfigs(t)
	srv := startFakeSMTPServer(t, fakeSMTPOptions{
		advertiseSTARTTLS: true,
		serverTLSConfig:   serverTLS,
	})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "NexusMart <no-reply@nexusmartai.store>", "resend", "re_test_key")
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	m.tlsConfig = clientTLS

	if err := m.Send(context.Background(), Message{
		To:      "buyer@gmail.com",
		Subject: "Order update",
		Body:    "Body",
	}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	srv.Close()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.mailFrom != "MAIL FROM:<no-reply@nexusmartai.store>" {
		t.Fatalf("expected bare MAIL FROM address, got %q", srv.mailFrom)
	}
	if !strings.Contains(srv.captured, "From: ") ||
		!strings.Contains(srv.captured, "NexusMart") ||
		!strings.Contains(srv.captured, "<no-reply@nexusmartai.store>") {
		t.Fatalf("expected full From header with display name, got %q", srv.captured)
	}
	if !strings.Contains(srv.captured, "@nexusmartai.store>\r\n") {
		t.Fatalf("expected Message-ID with FROM domain, got %q", srv.captured)
	}
}

func TestSMTPMailerReservedDomains(t *testing.T) {
	serverTLS, clientTLS := generateTestTLSConfigs(t)
	srv := startFakeSMTPServer(t, fakeSMTPOptions{
		advertiseSTARTTLS: true,
		serverTLSConfig:   serverTLS,
	})

	m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "no-reply@nexusmartai.store", "resend", "re_test_key")
	if err != nil {
		t.Fatalf("NewSMTPMailer: %v", err)
	}
	m.tlsConfig = clientTLS

	reservedRecipients := []string{
		"seed@nexusmart.local",
		"SEED@NEXUSMART.LOCAL",
		"user@app.test",
		"user@bad.invalid",
		"user@demo.example",
		"user@localhost",
		"user@dev.localhost",
		"user@example.com",
		"user@sub.example.com",
		"user@example.net",
		"user@sub.example.net",
		"user@example.org",
		"user@sub.example.org",
	}
	for _, rcpt := range reservedRecipients {
		if err := m.Send(context.Background(), Message{
			To:      rcpt,
			Subject: "Seed",
			Body:    "Skip me",
		}); err != nil {
			t.Fatalf("expected nil for reserved recipient %q, got %v", rcpt, err)
		}
	}

	srv.mu.Lock()
	connectedCount := srv.connected
	srv.mu.Unlock()
	if connectedCount != 0 {
		t.Fatalf("expected 0 SMTP connections for reserved domains with auth enabled, got %d", connectedCount)
	}

	// Non-reserved domain must NOT be classified as reserved.
	for _, realRcpt := range []string{"buyer@gmail.com", "ops@myexample.com", "shop@nexusmartai.store", "test@local.vn"} {
		if isReservedDomain(realRcpt) {
			t.Fatalf("expected %q not to be treated as reserved domain", realRcpt)
		}
	}

	// Without username (Mailpit/e2e mode), reserved domains MUST still be delivered.
	plainSrv := startFakeSMTPServer(t, fakeSMTPOptions{})
	plainMailer, err := NewSMTPMailer("127.0.0.1", plainSrv.addr.Port, "no-reply@nexusmart.local", "", "")
	if err != nil {
		t.Fatalf("NewSMTPMailer plain: %v", err)
	}
	if err := plainMailer.Send(context.Background(), Message{
		To:      "buyer@nexusmart.local",
		Subject: "E2E",
		Body:    "Deliver to Mailpit",
	}); err != nil {
		t.Fatalf("expected plain mailer to deliver to reserved domain, got %v", err)
	}
	plainSrv.Close()

	plainSrv.mu.Lock()
	defer plainSrv.mu.Unlock()
	if plainSrv.connected != 1 {
		t.Fatalf("expected plain mailer to connect once, got %d", plainSrv.connected)
	}
}

func TestNewSMTPMailerValidation(t *testing.T) {
	t.Run("username without password fails without leaking username", func(t *testing.T) {
		_, err := NewSMTPMailer("smtp.resend.com", 587, "no-reply@nexusmartai.store", "resend-user-only", "")
		if err == nil {
			t.Fatalf("expected error when password is missing")
		}
		if strings.Contains(err.Error(), "resend-user-only") {
			t.Fatalf("error must not print username value: %v", err)
		}
	})

	t.Run("password without username fails without leaking password", func(t *testing.T) {
		_, err := NewSMTPMailer("smtp.resend.com", 587, "no-reply@nexusmartai.store", "", "re_secret_only")
		if err == nil {
			t.Fatalf("expected error when username is missing")
		}
		if strings.Contains(err.Error(), "re_secret_only") {
			t.Fatalf("error must not print password value: %v", err)
		}
	})

	t.Run("invalid FROM addresses fail", func(t *testing.T) {
		badFroms := []string{
			"",
			"   ",
			"not-an-email",
			"NexusMart <invalid>",
			"no-reply@nexusmart.local\r\nBcc: evil@example.com",
		}
		for _, from := range badFroms {
			if _, err := NewSMTPMailer("mailpit", 1025, from, "", ""); err == nil {
				t.Fatalf("expected error for invalid FROM %q", from)
			}
		}
	})
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

func TestFormatShopBankAccountOTPEmailHidesOTPFromSubjectAndUsesVietnamese(t *testing.T) {
	sixDigitsRe := regexp.MustCompile(`\d{6}`)
	e2eOTPRe := regexp.MustCompile(`(?i)(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b`)

	otp := "918273"
	msg := FormatShopBankAccountOTPEmail(
		"seller@nexusmart.local\r\nBcc: evil@example.com",
		"Nguyễn Thị Hoa",
		otp,
		"2026-11-23T10:45:00Z",
		"VCB",
		"******7890",
	)

	if sixDigitsRe.MatchString(msg.Subject) || strings.Contains(msg.Subject, otp) {
		t.Errorf("SECURITY: shop bank OTP must not appear in Subject, got %q", msg.Subject)
	}
	if strings.Contains(strings.ToLower(msg.Subject), "bank account") {
		t.Errorf("shop bank OTP Subject must be in Vietnamese, got %q", msg.Subject)
	}
	m := e2eOTPRe.FindStringSubmatch(msg.Body)
	if len(m) != 2 || m[1] != otp {
		t.Errorf("shop bank OTP body must allow extracting 6-digit OTP cleanly, got %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "Nguyễn Thị Hoa") || !strings.Contains(msg.Body, "VCB") || !strings.Contains(msg.Body, "******7890") {
		t.Errorf("shop bank OTP body must include greeting, bank code, and masked account, got %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "17:45 ngày 23/11/2026 (GMT+7)") {
		t.Errorf("shop bank OTP body must format GMT+7 expiry, got %q", msg.Body)
	}

	raw := FormatMessage("no-reply@nexusmart.local", msg, time.Date(2026, 11, 23, 10, 30, 0, 0, time.UTC))
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("expected CRLF header injection to be sanitized")
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse RFC 5322 message: %v", err)
	}
	rawSubject := parsed.Header.Get("Subject")
	if sixDigitsRe.MatchString(rawSubject) || strings.Contains(rawSubject, otp) {
		t.Errorf("SECURITY: raw Subject header must not contain OTP, got %q", rawSubject)
	}
	decodedSubject, err := new(mime.WordDecoder).DecodeHeader(rawSubject)
	if err != nil {
		t.Fatalf("decode RFC 2047 Subject %q: %v", rawSubject, err)
	}
	if decodedSubject != msg.Subject {
		t.Errorf("decoded Subject mismatch: got %q, want %q", decodedSubject, msg.Subject)
	}
}

func TestFormatPhoneOTPEmailHidesOTPFromSubjectAndUsesVietnamese(t *testing.T) {
	sixDigitsRe := regexp.MustCompile(`\d{6}`)
	e2eOTPRe := regexp.MustCompile(`(?i)(?:otp|code|verification|mã|xác\s*thực)[^0-9]{0,24}(\d{6})\b`)

	otp := "519283"
	msg := FormatPhoneOTPEmail(
		"buyer@nexusmart.local\r\nBcc: evil@example.com",
		"Nguyễn Văn B",
		"090****789",
		otp,
		"2026-11-24T11:05:00Z",
	)

	if sixDigitsRe.MatchString(msg.Subject) || strings.Contains(msg.Subject, otp) {
		t.Errorf("SECURITY: phone OTP must not appear in Subject, got %q", msg.Subject)
	}
	if !strings.Contains(strings.ToLower(msg.Subject), "số điện thoại") {
		t.Errorf("phone OTP Subject must be in Vietnamese and reference phone verification, got %q", msg.Subject)
	}
	m := e2eOTPRe.FindStringSubmatch(msg.Body)
	if len(m) != 2 || m[1] != otp {
		t.Errorf("phone OTP body must allow extracting 6-digit OTP cleanly, got %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "Nguyễn Văn B") || !strings.Contains(msg.Body, "090****789") {
		t.Errorf("phone OTP body must include greeting and masked phone, got %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "18:05 ngày 24/11/2026 (GMT+7)") {
		t.Errorf("phone OTP body must format GMT+7 expiry, got %q", msg.Body)
	}

	raw := FormatMessage("no-reply@nexusmart.local", msg, time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC))
	if strings.Contains(string(raw), "\r\nBcc:") {
		t.Fatalf("expected CRLF header injection to be sanitized")
	}
	parsed, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse RFC 5322 message: %v", err)
	}
	rawSubject := parsed.Header.Get("Subject")
	if sixDigitsRe.MatchString(rawSubject) || strings.Contains(rawSubject, otp) {
		t.Errorf("SECURITY: raw Subject header must not contain OTP, got %q", rawSubject)
	}
	decodedSubject, err := new(mime.WordDecoder).DecodeHeader(rawSubject)
	if err != nil {
		t.Fatalf("decode RFC 2047 Subject %q: %v", rawSubject, err)
	}
	if decodedSubject != msg.Subject {
		t.Errorf("decoded Subject mismatch: got %q, want %q", decodedSubject, msg.Subject)
	}
}

func TestSMTPMailerIgnoresQuitErrorAfterData250(t *testing.T) {
	t.Run("plain SMTP peer closes immediately after DATA 250", func(t *testing.T) {
		srv := startFakeSMTPServer(t, fakeSMTPOptions{
			closeAfterData250: true,
		})
		m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "no-reply@nexusmart.local", "", "")
		if err != nil {
			t.Fatalf("NewSMTPMailer: %v", err)
		}
		if err := m.Send(context.Background(), Message{
			To:      "buyer@nexusmart.local",
			Subject: "Order confirmed",
			Body:    "Accepted before early close",
		}); err != nil {
			t.Fatalf("Send must succeed after DATA 250 even when QUIT fails: %v", err)
		}
		srv.Close()

		srv.mu.Lock()
		defer srv.mu.Unlock()
		if !strings.Contains(srv.captured, "Accepted before early close") {
			t.Fatalf("expected message body captured before peer close, got %q", srv.captured)
		}
	})

	t.Run("STARTTLS SMTP peer closes immediately after DATA 250", func(t *testing.T) {
		serverTLS, clientTLS := generateTestTLSConfigs(t)
		srv := startFakeSMTPServer(t, fakeSMTPOptions{
			advertiseSTARTTLS: true,
			serverTLSConfig:   serverTLS,
			closeAfterData250: true,
		})
		m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "NexusMart <no-reply@nexusmartai.store>", "resend", "re_test_key")
		if err != nil {
			t.Fatalf("NewSMTPMailer: %v", err)
		}
		m.tlsConfig = clientTLS

		if err := m.Send(context.Background(), Message{
			To:      "buyer@gmail.com",
			Subject: "TLS Order confirmed",
			Body:    "TLS accepted before early close",
		}); err != nil {
			t.Fatalf("STARTTLS Send must succeed after DATA 250 even when QUIT fails: %v", err)
		}
		srv.Close()

		srv.mu.Lock()
		defer srv.mu.Unlock()
		if !strings.Contains(srv.captured, "TLS accepted before early close") {
			t.Fatalf("expected TLS message body captured before peer close, got %q", srv.captured)
		}
	})

	t.Run("DATA 550 rejection still fails Send", func(t *testing.T) {
		srv := startFakeSMTPServer(t, fakeSMTPOptions{
			dataRejectLine: "550 5.7.1 Message rejected",
		})
		m, err := NewSMTPMailer("127.0.0.1", srv.addr.Port, "no-reply@nexusmart.local", "", "")
		if err != nil {
			t.Fatalf("NewSMTPMailer: %v", err)
		}
		err = m.Send(context.Background(), Message{
			To:      "buyer@nexusmart.local",
			Subject: "Should fail",
			Body:    "Rejected DATA",
		})
		if err == nil || !strings.Contains(err.Error(), "550") {
			t.Fatalf("expected 550 error when DATA is rejected, got %v", err)
		}
	})
}
