package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// parseEmail membaca email hasil BuildEmail dan mengembalikan header, isi
// teks, dan isi HTML (sudah di-decode dari quoted-printable).
func parseEmail(t *testing.T, raw []byte) (mail.Header, string, string) {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("content type = %q, %v", mediaType, err)
	}
	parts := map[string]string{}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := mr.NextRawPart() // Raw: decode quoted-printable sendiri di bawah
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(p))
		if err != nil {
			t.Fatal(err)
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		parts[ct] = string(body)
	}
	return msg.Header, parts["text/plain"], parts["text/html"]
}

func TestBuildEmail(t *testing.T) {
	from := &mail.Address{Name: "Jaga Listing", Address: "noreply@example.com"}
	to := &mail.Address{Address: "pemilik@example.com"}

	raw, err := BuildEmail(from, to, sampleAlert())
	if err != nil {
		t.Fatal(err)
	}
	h, text, html := parseEmail(t, raw)

	subject, err := new(mime.WordDecoder).DecodeHeader(h.Get("Subject"))
	if err != nil || subject != sampleAlert().Subject() {
		t.Errorf("subject = %q, %v", subject, err)
	}
	for _, want := range []string{"Toko Maju Depok", "    +6281234567890", "query_place_id=ChIJtokomaju"} {
		if !strings.Contains(text, want) {
			t.Errorf("text part missing %q:\n%s", want, text)
		}
	}
	// Kotak nilai yang mudah disalin. html/template menulis "+" sebagai
	// "&#43;"; klien email menampilkannya kembali sebagai "+".
	if !strings.Contains(html, "user-select:all") || !strings.Contains(stdhtml.UnescapeString(html), ">+6281234567890</div>") {
		t.Errorf("html part has no copy box:\n%s", html)
	}

	t.Run("input pemilik di-escape dan tidak bisa menyisipkan header", func(t *testing.T) {
		a := sampleAlert()
		a.Label = "<script>alert(1)</script>\r\nBcc: korban@example.com"
		raw, err := BuildEmail(from, to, a)
		if err != nil {
			t.Fatal(err)
		}
		h, _, html := parseEmail(t, raw)
		if h.Get("Bcc") != "" {
			t.Error("label injected a Bcc header")
		}
		if strings.Contains(html, "<script>") {
			t.Error("label was not escaped in html part")
		}
	})
}

func TestEmailSenderInvalidRecipientIsPermanent(t *testing.T) {
	s, err := NewEmailSender(EmailConfig{Host: "localhost", Port: 1, From: "a@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	a := sampleAlert()
	a.Email = "bukan email"
	if err := s.Send(context.Background(), a); !isPermanent(err) {
		t.Errorf("err = %v, want permanent", err)
	}
}

// TestEmailSenderMailpit mengirim email sungguhan ke Mailpit di Docker, lalu
// memeriksanya lewat API Mailpit.
func TestEmailSenderMailpit(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test dilewati dengan -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "axllent/mailpit:v1.27",
			ExposedPorts: []string{"1025/tcp", "8025/tcp"},
			WaitingFor:   wait.ForHTTP("/livez").WithPort("8025/tcp"),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, ctr)
	if err != nil {
		t.Fatal(err)
	}
	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	smtpPort, err := ctr.MappedPort(ctx, "1025/tcp")
	if err != nil {
		t.Fatal(err)
	}
	apiPort, err := ctr.MappedPort(ctx, "8025/tcp")
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewEmailSender(EmailConfig{Host: host, Port: int(smtpPort.Num()), From: "Jaga Listing <noreply@example.com>"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(ctx, sampleAlert()); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(fmt.Sprintf("http://%s:%s/api/v1/messages", host, apiPort.Port()))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Messages []struct {
			Subject string
			To      []struct{ Address string }
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Messages) != 1 {
		t.Fatalf("mailpit has %d messages, want 1", len(list.Messages))
	}
	m := list.Messages[0]
	if m.Subject != sampleAlert().Subject() || len(m.To) != 1 || m.To[0].Address != "pemilik@example.com" {
		t.Errorf("message = %+v", m)
	}
}
