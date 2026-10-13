package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"
)

// EmailConfig berisi pengaturan server SMTP.
type EmailConfig struct {
	Host     string // misalnya localhost (Mailpit) atau smtp.example.com
	Port     int    // 1025 untuk Mailpit, 587 untuk STARTTLS
	Username string // kosong = tanpa login (Mailpit)
	Password string
	From     string // misalnya "Jaga Listing <noreply@example.com>"
}

// EmailSender mengirim Alert lewat SMTP.
//
// Jika server menawarkan STARTTLS, koneksi selalu dienkripsi lebih dulu.
// Port 465 (TLS langsung) belum didukung.
type EmailSender struct {
	cfg  EmailConfig
	from *mail.Address
}

// NewEmailSender memvalidasi konfigurasi dan membuat EmailSender.
func NewEmailSender(cfg EmailConfig) (*EmailSender, error) {
	if cfg.Host == "" || cfg.Port <= 0 {
		return nil, errors.New("notify: smtp host/port is empty")
	}
	from, err := mail.ParseAddress(cfg.From)
	if err != nil {
		return nil, fmt.Errorf("notify: invalid from address: %w", err)
	}
	return &EmailSender{cfg: cfg, from: from}, nil
}

// Send memenuhi interface Sender.
func (s *EmailSender) Send(ctx context.Context, a Alert) error {
	to, err := mail.ParseAddress(a.Email)
	if err != nil {
		// Alamat tidak valid tidak akan valid walau dicoba lagi.
		return fmt.Errorf("%w: invalid recipient: %w", ErrPermanent, err)
	}
	msg, err := BuildEmail(s.from, to, a)
	if err != nil {
		return err
	}
	return s.deliver(ctx, to.Address, msg)
}

// deliver mengirim satu pesan. net/smtp tidak mengenal context, jadi koneksi
// dibuka dengan DialContext dan diberi deadline; dengan begitu pengiriman
// tidak bisa menggantung selamanya.
func (s *EmailSender) deliver(ctx context.Context, to string, msg []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("notify: smtp dial: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("notify: smtp hello: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host}); err != nil {
			return fmt.Errorf("notify: smtp starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		// PlainAuth menolak mengirim password lewat koneksi tanpa TLS
		// (kecuali ke localhost).
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("notify: smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("notify: smtp mail from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		// Kode 5xx berarti server menolak alamat ini secara permanen
		// (misalnya mailbox tidak ada); mencoba lagi tidak akan membantu.
		var tpErr *textproto.Error
		if errors.As(err, &tpErr) && tpErr.Code >= 500 {
			return fmt.Errorf("%w: smtp rcpt: %w", ErrPermanent, err)
		}
		return fmt.Errorf("notify: smtp rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("notify: smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("notify: smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("notify: smtp send: %w", err)
	}
	return c.Quit()
}

// BuildEmail menyusun email MIME multipart/alternative: versi teks biasa dan
// versi HTML. Klien email memilih versi yang bisa ditampilkannya.
func BuildEmail(from, to *mail.Address, a Alert) ([]byte, error) {
	var text, html bytes.Buffer
	if err := textTmpl.Execute(&text, a); err != nil {
		return nil, fmt.Errorf("notify: render text: %w", err)
	}
	if err := htmlTmpl.Execute(&html, a); err != nil {
		return nil, fmt.Errorf("notify: render html: %w", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Header ditulis manual. Subject di-encode (RFC 2047) supaya karakter
	// non-ASCII aman dan baris baru tidak bisa menyisipkan header lain.
	h := []string{
		"From: " + from.String(),
		"To: " + to.String(),
		"Subject: " + mime.QEncoding.Encode("utf-8", a.Subject()),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"Message-ID: " + messageID(from.Address),
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="` + mw.Boundary() + `"`,
	}
	// NewWriter baru membuat boundary dan belum menulis apa pun, jadi header
	// email masih bisa ditulis lebih dulu.
	buf.WriteString(strings.Join(h, "\r\n") + "\r\n\r\n")

	for _, part := range []struct {
		contentType string
		body        []byte
	}{
		{"text/plain; charset=utf-8", text.Bytes()},
		{"text/html; charset=utf-8", html.Bytes()},
	} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		// quoted-printable menjaga baris tetap pendek (batas SMTP 998 karakter)
		// dan aman untuk karakter non-ASCII.
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write(part.body); err != nil {
			return nil, err
		}
		if err := qp.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func messageID(from string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	domain := "jagalisting.local"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		domain = from[i+1:]
	}
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}

var textTmpl = texttemplate.Must(texttemplate.New("text").Parse(`Halo,

Jaga Listing mendeteksi perubahan pada listing Google Maps "{{.ListingName}}".

{{.FieldName}} {{.Problem}}.
Tingkat bahaya: {{.SeverityName}}
Terdeteksi: {{.DetectedAtText}}
{{if .CorrectValue}}
Data yang benar menurut Anda (salin untuk mengajukan perbaikan):

    {{.CorrectValue}}
{{end}}
Yang perlu dilakukan:
1. Buka listing Anda di Google Maps: {{.MapsURL}}
2. Kalau perubahan ini bukan dari Anda, perbaiki lewat Google Business Profile
   atau "Sarankan perubahan" di Google Maps.
3. Kalau perubahan ini memang dari Anda, perbarui data listing di Jaga Listing.

Notifikasi untuk perubahan ini hanya dikirim sekali.

Jaga Listing
`))

// html/template otomatis meng-escape semua nilai (misalnya label yang berisi
// "<script>"), sehingga input pemilik tidak bisa menyisipkan HTML.
//
// Kotak nilai memakai user-select: all: di klien email yang mendukung CSS ini
// (misalnya Apple Mail dan iOS), satu klik atau ketukan memilih seluruh nilai
// sehingga langsung bisa disalin. Tombol copy berbasis JavaScript tidak
// dipakai karena diblokir hampir semua klien email.
var htmlTmpl = htmltemplate.Must(htmltemplate.New("html").Parse(`<!doctype html>
<html lang="id">
<body style="margin:0">
<!-- Sebagian klien (misalnya Gmail) membuang style di <body>, jadi latar dan font ada di tabel pembungkus. -->
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;font-family:Arial,Helvetica,sans-serif;color:#18181b">
<tr><td style="padding:24px">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:560px;margin:0 auto;background:#ffffff;border-radius:8px">
<tr><td style="padding:24px">
  <p style="margin:0 0 8px;font-size:13px;font-weight:bold;color:{{if eq .Severity "critical"}}#b91c1c{{else if eq .Severity "medium"}}#b45309{{else}}#3f3f46{{end}}">{{.SeverityName}}</p>
  <h1 style="margin:0 0 16px;font-size:20px;line-height:1.3">{{.FieldName}} {{.Problem}}</h1>
  <p style="margin:0 0 16px;font-size:15px;line-height:1.5">Listing: <strong>{{.ListingName}}</strong><br>Terdeteksi: {{.DetectedAtText}}</p>
  {{if .CorrectValue}}
  <p style="margin:0 0 8px;font-size:15px">Data yang benar menurut Anda:</p>
  <div style="margin:0 0 4px;padding:12px 16px;border:1px dashed #a1a1aa;border-radius:6px;background:#fafafa;font-family:'Courier New',monospace;font-size:18px;user-select:all;-webkit-user-select:all">{{.CorrectValue}}</div>
  <p style="margin:0 0 16px;font-size:12px;color:#71717a">Klik atau ketuk kotak di atas, lalu salin untuk mengajukan perbaikan.</p>
  {{end}}
  <p style="margin:0 0 20px"><a href="{{.MapsURL}}" style="display:inline-block;padding:10px 16px;background:#1d4ed8;color:#ffffff;text-decoration:none;border-radius:6px;font-size:15px">Buka di Google Maps</a></p>
  <p style="margin:0 0 8px;font-size:14px;line-height:1.5">Kalau perubahan ini <strong>bukan</strong> dari Anda, perbaiki lewat Google Business Profile atau "Sarankan perubahan" di Google Maps.</p>
  <p style="margin:0 0 16px;font-size:14px;line-height:1.5">Kalau perubahan ini memang dari Anda, perbarui data listing di Jaga Listing.</p>
  <p style="margin:0;font-size:12px;color:#71717a">Notifikasi untuk perubahan ini hanya dikirim sekali.</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>
`))
