package server

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/onatakduman/rustdesk-api-lite/internal/store"
)

// Sending e-mail is optional: without it invite links are passed on by hand.
// Settings keys of the outgoing mail server.
const (
	keyMailHost     = "smtp_host"
	keyMailPort     = "smtp_port"
	keyMailSecurity = "smtp_security" // starttls (default) | tls | none
	keyMailUser     = "smtp_user"
	keyMailPass     = "smtp_pass"
	keyMailFrom     = "smtp_from"
)

type mailConfig struct{ Host, Port, Security, User, Pass, From string }

// mailConfig loads the mail settings; ok is false while they are incomplete.
func (s *Server) mailConfig() (mailConfig, bool) {
	c := mailConfig{Host: s.st.Setting(keyMailHost), Port: s.st.Setting(keyMailPort), Security: s.st.Setting(keyMailSecurity),
		User: s.st.Setting(keyMailUser), Pass: s.st.Setting(keyMailPass), From: s.st.Setting(keyMailFrom)}
	if c.Security == "" {
		c.Security = "starttls"
	}
	if c.Port == "" {
		c.Port = map[string]string{"tls": "465", "none": "25"}[c.Security]
		if c.Port == "" {
			c.Port = "587"
		}
	}
	return c, c.Host != "" && c.From != ""
}

const mailTimeout = 20 * time.Second

// sendMail delivers one plain-text message.
func sendMail(cfg mailConfig, to, subject, body string) error {
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return errors.New("the sender address is not a valid e-mail address")
	}
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return errors.New("the recipient is not a valid e-mail address")
	}
	if strings.ContainsAny(subject, "\r\n") {
		return errors.New("the subject must be one line")
	}
	addr := net.JoinHostPort(cfg.Host, cfg.Port)
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: mailTimeout}
	var conn net.Conn
	if cfg.Security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("could not reach the mail server: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(mailTimeout))
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("the mail server did not answer as expected: %w", err)
	}
	defer c.Close()
	if cfg.Security == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the mail server does not offer STARTTLS; choose another kind of connection")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("could not secure the connection: %w", err)
		}
	}
	if cfg.User != "" {
		// net/smtp refuses to send a password over an unencrypted connection, except to this machine.
		if err := c.Auth(smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)); err != nil {
			return fmt.Errorf("the mail server refused the sign-in: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("the mail server refused the sender: %w", err)
	}
	if err := c.Rcpt(rcpt.Address); err != nil {
		return fmt.Errorf("the mail server refused the recipient: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("the mail server refused the message: %w", err)
	}
	domain := "localhost"
	if i := strings.LastIndexByte(from.Address, '@'); i >= 0 {
		domain = from.Address[i+1:]
	}
	var msg strings.Builder
	fmt.Fprintf(&msg, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\n", from.String(), rcpt.String(), mime.QEncoding.Encode("utf-8", subject),
		time.Now().Format(time.RFC1123Z), store.RandomToken(16), domain)
	msg.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	enc := base64.StdEncoding.EncodeToString([]byte(body))
	for len(enc) > 76 {
		msg.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	msg.WriteString(enc + "\r\n")
	if _, err := w.Write([]byte(msg.String())); err != nil {
		return fmt.Errorf("could not send the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("the mail server did not accept the message: %w", err)
	}
	return c.Quit()
}

// cMailTest sends a test message with the stored settings.
func (s *Server) cMailTest(c *consoleCtx) {
	var in struct{ To string }
	if !c.body(&in) {
		return
	}
	cfg, ok := s.mailConfig()
	if !ok {
		c.fail(http.StatusBadRequest, "enter the mail server and the sender address first, and save")
		return
	}
	name := s.serverName(c.r)
	if err := sendMail(cfg, strings.TrimSpace(in.To), "Test message from "+name, "This is a test message. "+name+" can send e-mail.\n"); err != nil {
		c.fail(http.StatusBadGateway, err.Error())
		return
	}
	s.change(c, "mail.test", strings.TrimSpace(in.To), "")
	c.done()
}

// mailSettings is what the console shows of the mail settings: never the password.
func (s *Server) mailSettings() map[string]any {
	cfg, ok := s.mailConfig()
	return map[string]any{"host": cfg.Host, "port": s.st.Setting(keyMailPort), "security": cfg.Security, "user": cfg.User, "from": cfg.From,
		"password_set": cfg.Pass != "", "ready": ok}
}

// saveMailSettings stores the mail settings. An empty password keeps the stored
// one; an empty host switches sending off and forgets the password.
func (s *Server) saveMailSettings(in struct {
	Host, Port, Security, User, From string
	Password                         string
}) error {
	in.Host, in.From, in.User, in.Port = strings.TrimSpace(in.Host), strings.TrimSpace(in.From), strings.TrimSpace(in.User), strings.TrimSpace(in.Port)
	if in.Security != "tls" && in.Security != "none" {
		in.Security = "starttls"
	}
	if in.Host != "" {
		if strings.ContainsAny(in.Host, " /\r\n") {
			return errors.New("the mail server is a host name, without a scheme or path")
		}
		if _, err := mail.ParseAddress(in.From); err != nil {
			return errors.New("the sender address is not a valid e-mail address")
		}
		if in.Port != "" {
			if n, err := strconv.Atoi(in.Port); err != nil || n < 1 || n > 65535 {
				return errors.New("the port is a number between 1 and 65535")
			}
		}
	}
	for k, v := range map[string]string{keyMailHost: in.Host, keyMailPort: in.Port, keyMailSecurity: in.Security, keyMailUser: in.User, keyMailFrom: in.From} {
		if err := s.st.SetSetting(k, v); err != nil {
			return err
		}
	}
	if in.Host == "" || in.User == "" {
		return s.st.SetSetting(keyMailPass, "")
	}
	if in.Password != "" {
		return s.st.SetSetting(keyMailPass, in.Password)
	}
	return nil
}
