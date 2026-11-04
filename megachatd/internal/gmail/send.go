package gmail

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// SendFile is one attachment of SendEmail.
type SendFile struct {
	Filename string
	Data     []byte
}

// SendParams are send_email's keyword arguments.
type SendParams struct {
	To, Cc, Bcc, Subject, Text string
	ReplyToUID                 *int
	ReplyToFolder              *string
	ForwardUID                 *int
	ForwardFolder              *string
	Attachments                []SendFile
}

var (
	addrSepRE        = regexp.MustCompile(`^[\s,]+|[\s,]+$`)
	addrEmptyItemsRE = regexp.MustCompile(`,(?:\s*,)+`)
)

func parseAddresses(rawv, field string) ([]string, error) {
	v, err := validateHeaderValue(rawv, field)
	if err != nil {
		return nil, err
	}
	v = addrEmptyItemsRE.ReplaceAllString(addrSepRE.ReplaceAllString(v, ""), ",")
	if v == "" {
		return nil, nil
	}
	var out []string
	for _, p := range getAddresses([]string{v}) {
		if a := strings.TrimSpace(p.Email); a != "" {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil, validation("Indirizzo " + field + " non valido")
	}
	for _, a := range out {
		if !strings.Contains(a, "@") {
			return nil, validation("Indirizzo " + field + " non valido")
		}
	}
	return out, nil
}

func validateText(t string) (string, error) {
	if utf8.RuneCountInString(t) > maxTextLength {
		return "", tooLarge("Testo troppo lungo")
	}
	return t, nil
}

func validateSendAttachments(files []SendFile) ([]SendFile, error) {
	var out []SendFile
	total := 0
	for _, f := range files {
		if len(out) >= maxAttachments {
			return nil, validation("Troppi allegati")
		}
		if len(f.Data) > maxAttachmentBytes {
			return nil, tooLarge("Allegato troppo grande")
		}
		total += len(f.Data)
		if total > maxTotalAttachmentBytes {
			return nil, tooLarge("Allegati troppo grandi")
		}
		name := sanitizeFilename(f.Filename)
		if name == "" {
			name = "attachment"
		}
		out = append(out, SendFile{Filename: name, Data: f.Data})
	}
	return out, nil
}

func appendQuote(userText, original string) string {
	original = pyStrip(original)
	if original == "" {
		return userText
	}
	lines := pySplitLines(original)
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	quoted := strings.Join(lines, "\n")
	body := strings.TrimRight(userText, " \t\r\n\f\v")
	if body != "" {
		return body + "\n\n" + quoted
	}
	return quoted
}

func replySubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	if subject != "" {
		return "Re: " + subject
	}
	return "Re:"
}

func forwardSubject(subject string) string {
	l := strings.ToLower(subject)
	if strings.HasPrefix(l, "fwd:") || strings.HasPrefix(l, "fw:") || strings.HasPrefix(l, "i:") {
		return subject
	}
	if subject != "" {
		return "Fwd: " + subject
	}
	return "Fwd:"
}

func appendForward(userText, from, to, cc, subject, text string, dateTS *int64) string {
	lines := []string{"---------- Messaggio inoltrato ----------", "Da: " + from}
	if dateTS != nil && *dateTS != 0 {
		lines = append(lines, "Data: "+time.Unix(*dateTS, 0).Local().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	}
	lines = append(lines, "Oggetto: "+subject, "A: "+to)
	if cc != "" {
		lines = append(lines, "Cc: "+cc)
	}
	block := strings.Join(lines, "\n")
	t := pyStrip(text)
	forwarded := block
	if t != "" {
		forwarded = block + "\n\n" + t
	}
	body := strings.TrimRight(userText, " \t\r\n\f\v")
	if body != "" {
		return body + "\n\n" + forwarded
	}
	return forwarded
}

// SendEmail sends a message and returns its Message-ID.
func (s *Service) SendEmail(ctx context.Context, p SendParams) (string, error) {
	st, err := s.loadSettings()
	if err != nil {
		return "", err
	}
	if p.ReplyToUID != nil && p.ForwardUID != nil {
		return "", validation("Risposta e inoltro non possono essere combinati")
	}
	toList, err := parseAddresses(p.To, "to")
	if err != nil {
		return "", err
	}
	ccList, err := parseAddresses(p.Cc, "cc")
	if err != nil {
		return "", err
	}
	bccList, err := parseAddresses(p.Bcc, "bcc")
	if err != nil {
		return "", err
	}
	subject, err := validateHeaderValue(p.Subject, "subject")
	if err != nil {
		return "", err
	}
	body, err := validateText(p.Text)
	if err != nil {
		return "", err
	}
	files, err := validateSendAttachments(p.Attachments)
	if err != nil {
		return "", err
	}

	if p.ForwardUID != nil {
		fv, err := s.folderOr(p.ForwardFolder, st)
		if err != nil {
			return "", err
		}
		orig, err := s.FetchMessage(ctx, *p.ForwardUID, &fv)
		if err != nil {
			return "", err
		}
		if subject == "" {
			subject = unfoldHeader(orig.Subject)
		}
		subject = forwardSubject(subject)
		body = appendForward(body, orig.From, orig.To, orig.Cc, orig.Subject, orig.Text, orig.DateTs)
		all := append([]SendFile(nil), files...)
		for _, a := range orig.Attachments {
			if a.Inline != nil && *a.Inline {
				continue
			}
			dl, err := s.FetchAttachment(ctx, *p.ForwardUID, a.Index, &fv)
			if err != nil {
				return "", err
			}
			all = append(all, SendFile{Filename: dl.Filename, Data: dl.Data})
		}
		if files, err = validateSendAttachments(all); err != nil {
			return "", err
		}
	}

	inReplyTo, references := "", ""
	if p.ReplyToUID != nil {
		folder := p.ReplyToFolder
		if folder == nil || *folder == "" {
			f := st.Folder
			folder = &f
		}
		orig, err := s.FetchMessage(ctx, *p.ReplyToUID, folder)
		if err != nil {
			return "", err
		}
		origID := strings.TrimSpace(unfoldHeader(orig.MessageID))
		if len(toList) == 0 {
			if toList, err = parseAddresses(orig.From, "to"); err != nil {
				return "", err
			}
		}
		if subject == "" {
			subject = unfoldHeader(orig.Subject)
		}
		subject = replySubject(subject)
		body = appendQuote(body, orig.Text)
		if origID != "" {
			inReplyTo = origID
			prev := strings.TrimSpace(unfoldHeader(orig.References))
			references = origID
			if prev != "" {
				references = prev + " " + origID
			}
		}
	}

	if len(toList) == 0 && len(ccList) == 0 && len(bccList) == 0 {
		return "", validation("Destinatario mancante")
	}

	domain := ""
	if i := strings.LastIndex(st.Address, "@"); i >= 0 {
		domain = st.Address[i+1:]
	}
	if domain == "" {
		domain = "localhost"
	}
	msgID := makeMsgID(domain)
	msg := buildMessage(st.Address, toList, ccList, subject, msgID, inReplyTo, references, body, files)
	rcpts := append(append(append([]string{}, toList...), ccList...), bccList...)
	if err := s.smtpSend(ctx, st, rcpts, msg); err != nil {
		return "", err
	}
	s.invalidateInboxAll()
	return msgID, nil
}

func makeMsgID(domain string) string {
	var r [6]byte
	_, _ = rand.Read(r[:])
	return fmt.Sprintf("<%d.%d.%s@%s>", time.Now().UnixNano(), os.Getpid(), hex.EncodeToString(r[:]), domain)
}

func encodeHeader(v string) string {
	if isASCII(v) {
		return v
	}
	return mime.QEncoding.Encode("utf-8", v)
}

func isASCII(v string) bool {
	for i := 0; i < len(v); i++ {
		if v[i] >= 0x80 {
			return false
		}
	}
	return true
}

func qpEncode(b []byte) []byte {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func wrapBase64(b []byte) []byte {
	enc := base64.StdEncoding.EncodeToString(b)
	var out bytes.Buffer
	for len(enc) > 76 {
		out.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	out.WriteString(enc + "\r\n")
	return out.Bytes()
}

// textBody encodes the text part the way EmailMessage.set_content does:
// 7bit when ASCII with short lines, else quoted-printable or base64.
func textBody(text string) (cte string, body []byte) {
	norm := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	if !strings.HasSuffix(norm, "\n") {
		norm += "\n"
	}
	short := true
	for _, l := range strings.Split(norm, "\n") {
		if len(l) > 78 {
			short = false
		}
	}
	crlf := strings.ReplaceAll(norm, "\n", "\r\n")
	if short && isASCII(norm) {
		return "7bit", []byte(crlf)
	}
	qp := qpEncode([]byte(crlf))
	b64 := wrapBase64([]byte(crlf))
	if len(qp) > len(b64) {
		return "base64", b64
	}
	return "quoted-printable", qp
}

func buildMessage(from string, to, cc []string, subject, msgID, inReplyTo, references, text string, files []SendFile) []byte {
	var b bytes.Buffer
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from)
	if len(to) > 0 {
		h("To", strings.Join(to, ", "))
	}
	if len(cc) > 0 {
		h("Cc", strings.Join(cc, ", "))
	}
	h("Subject", encodeHeader(subject))
	h("Date", time.Now().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	h("Message-ID", msgID)
	if inReplyTo != "" {
		h("In-Reply-To", inReplyTo)
		h("References", references)
	}
	h("MIME-Version", "1.0")
	cte, body := textBody(text)
	if len(files) == 0 {
		h("Content-Type", `text/plain; charset="utf-8"`)
		h("Content-Transfer-Encoding", cte)
		b.WriteString("\r\n")
		b.Write(body)
		return b.Bytes()
	}
	var rb [12]byte
	_, _ = rand.Read(rb[:])
	boundary := "===============" + hex.EncodeToString(rb[:]) + "=="
	h("Content-Type", `multipart/mixed; boundary="`+boundary+`"`)
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString(`Content-Type: text/plain; charset="utf-8"` + "\r\n")
	b.WriteString("Content-Transfer-Encoding: " + cte + "\r\n\r\n")
	b.Write(body)
	for _, f := range files {
		ct := mime.TypeByExtension(strings.ToLower(filepath.Ext(f.Filename)))
		if ct == "" || !strings.Contains(ct, "/") {
			ct = "application/octet-stream"
		}
		ct, _, _ = strings.Cut(ct, ";")
		b.WriteString("\r\n--" + boundary + "\r\n")
		b.WriteString("Content-Type: " + ct + "\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("Content-Disposition: attachment; " + dispositionFilename(f.Filename) + "\r\n\r\n")
		b.Write(wrapBase64(f.Data))
	}
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes()
}

// smtpSend opens one SMTP connection: STARTTLS is required before AUTH.
func (s *Service) smtpSend(ctx context.Context, st settings, rcpts []string, msg []byte) error {
	d := net.Dialer{Timeout: st.Timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(st.SMTPHost, itoa(st.SMTPPort)))
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(st.Timeout))
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	c, err := smtp.NewClient(conn, st.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return err
	}
	tc := s.tls.Clone()
	if s.tls == nil {
		tc = nil
	}
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return upstream("STARTTLS non supportato")
	}
	if tc == nil || (tc.ServerName == "" && !tc.InsecureSkipVerify) {
		if tc == nil {
			tc = newTLSConfig()
		}
		tc.ServerName = st.SMTPHost
	}
	if err := c.StartTLS(tc); err != nil {
		return err
	}
	if err := c.Auth(smtp.PlainAuth("", st.Address, st.Password, st.SMTPHost)); err != nil {
		return err
	}
	if err := c.Mail(st.Address); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	_ = c.Quit()
	return nil
}

// dispositionFilename renders the filename parameter: quoted when ASCII,
// RFC 2231 (filename*=utf-8”...) otherwise.
func dispositionFilename(name string) string {
	if isASCII(name) {
		return `filename="` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`
	}
	return mime.FormatMediaType("x", map[string]string{"filename": name})[2:]
}
