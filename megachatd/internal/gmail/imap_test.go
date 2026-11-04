package gmail

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func cmdsMatching(imap *fakeIMAP, sub string) []string {
	var out []string
	for _, c := range imap.commands() {
		if strings.Contains(c, sub) {
			out = append(out, c)
		}
	}
	return out
}

func TestMessageOpensWithPartialFetch(t *testing.T) {
	s, f, _ := setup(t)
	uid := f.add("INBOX", nil, multipartMsg)
	d, err := s.FetchMessage(context.Background(), uid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Text != "Ciao caffè" || len(d.Attachments) != 3 || d.Attachments[1].Filename != "report.pdf" || d.Attachments[1].Size == 0 {
		t.Fatalf("%+v", d)
	}
	if n := len(cmdsMatching(f, "BODYSTRUCTURE")); n != 1 {
		t.Fatalf("want one BODYSTRUCTURE fetch, got %v", f.commands())
	}
	if full := cmdsMatching(f, "BODY.PEEK[]"); len(full) != 0 {
		t.Fatalf("the whole message was downloaded: %v", full)
	}
	// text parts and the small inline image in one FETCH, never the PDF (part 2)
	parts := cmdsMatching(f, "BODY.PEEK[1.1] ")
	if len(parts) != 1 || !strings.Contains(parts[0], "BODY.PEEK[1.2.1]") || !strings.Contains(parts[0], "BODY.PEEK[1.2.2]") || strings.Contains(parts[0], "BODY.PEEK[2]") {
		t.Fatalf("part fetch: %v", f.commands())
	}
	if len(s.cachedInlineImage("INBOX", int64(uid), 0)) == 0 {
		t.Fatal("inline image not cached from the partial fetch")
	}
}

func TestAttachmentDownloadFetchesOnlyThePart(t *testing.T) {
	s, f, _ := setup(t)
	uid := f.add("INBOX", nil, multipartMsg)
	dl, err := s.FetchAttachment(context.Background(), uid, 1, nil)
	if err != nil || dl.Filename != "report.pdf" || string(dl.Data) != "%PDF-" || dl.ContentType != "application/pdf" {
		t.Fatalf("%+v %v", dl, err)
	}
	if got := cmdsMatching(f, "BODY.PEEK[2]"); len(got) != 1 {
		t.Fatalf("single part fetch expected: %v", f.commands())
	}
	if full := cmdsMatching(f, "BODY.PEEK[]"); len(full) != 0 {
		t.Fatalf("the whole message was downloaded: %v", full)
	}
	if _, err := s.FetchAttachment(context.Background(), uid, 7, nil); err == nil {
		t.Fatal("missing attachment")
	} else if _, ok := err.(*notFoundError); !ok {
		t.Fatalf("%T", err)
	}
}

func TestPartialFetchFallsBackToFullMessage(t *testing.T) {
	s, f, _ := setup(t)
	// More text parts than _MAX_RENDER_PARTS: the fast path is refused.
	var b strings.Builder
	b.WriteString("From: a@x.org\nSubject: many\nContent-Type: multipart/mixed; boundary=B\n\n")
	for i := 0; i < maxRenderParts+1; i++ {
		b.WriteString("--B\nContent-Type: text/plain\n\npart\n")
	}
	b.WriteString("--B--\n")
	uid := f.add("INBOX", nil, b.String())
	d, err := s.FetchMessage(context.Background(), uid, nil)
	if err != nil || d.Subject != "many" || !strings.Contains(d.Text, "part") {
		t.Fatalf("%+v %v", d, err)
	}
	if len(cmdsMatching(f, "BODY.PEEK[]")) != 1 {
		t.Fatalf("expected the legacy full fetch: %v", f.commands())
	}
}

func TestSinglePartMessagePartialFetch(t *testing.T) {
	s, f, _ := setup(t)
	uid := f.add("INBOX", nil, rfc("plain", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "just text"))
	d, err := s.FetchMessage(context.Background(), uid, nil)
	if err != nil || d.Text != "just text" || d.Subject != "plain" {
		t.Fatalf("%+v %v", d, err)
	}
	if len(cmdsMatching(f, "BODY.PEEK[1]")) != 1 || len(cmdsMatching(f, "BODY.PEEK[]")) != 0 {
		t.Fatalf("%v", f.commands())
	}
}

func TestPoolReusesConnections(t *testing.T) {
	s, f, _ := setup(t)
	f.add("INBOX", nil, rfc("a", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "x"))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := s.FetchMessageList(ctx, nil, 0, 10, true); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FetchUnreadStatus(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.connections(); n != 1 {
		t.Fatalf("sequential operations must share one connection, got %d", n)
	}
	// at most poolMaxConns concurrent sessions
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.FetchMessageList(ctx, nil, 0, 10, true)
		}()
	}
	wg.Wait()
	if n := f.connections(); n > poolMaxConns {
		t.Fatalf("pool opened %d connections (max %d)", n, poolMaxConns)
	}
	s.Close()
	if len(s.pool.idle) != 0 {
		t.Fatal("pool not emptied")
	}
}

func TestPoolDropsBrokenConnection(t *testing.T) {
	s, f, _ := setup(t)
	ctx := context.Background()
	if _, err := s.FetchUnreadStatus(ctx, nil); err != nil {
		t.Fatal(err)
	}
	// kill the idle connection behind the pool's back
	s.pool.mu.Lock()
	for _, c := range s.pool.idle {
		c.cl.Close()
	}
	s.pool.mu.Unlock()
	if _, err := s.FetchUnreadStatus(ctx, nil); err != nil {
		t.Fatalf("a dead pooled connection must be replaced: %v", err)
	}
	if n := f.connections(); n != 2 {
		t.Fatalf("connections %d", n)
	}
}

func TestArchiveUsesGmailLabelCommand(t *testing.T) {
	s, f, _ := setup(t)
	uid := f.add("INBOX", nil, rfc("a", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "x"))
	if err := s.Archive(context.Background(), uid, nil); err != nil {
		t.Fatal(err)
	}
	got := cmdsMatching(f, "X-GM-LABELS")
	if len(got) != 1 || !strings.Contains(got[0], `UID STORE 1 -X-GM-LABELS`) || !strings.Contains(got[0], `(\Inbox)`) {
		t.Fatalf("%v", f.commands())
	}
	if err := s.Archive(context.Background(), 99, nil); err == nil {
		t.Fatal("archiving a missing message must fail")
	}
	other := "[Gmail]/Trash"
	if err := s.Archive(context.Background(), 1, &other); err == nil {
		t.Fatal("only INBOX can be archived")
	}
}

func TestDeleteUsesSpecialUseTrash(t *testing.T) {
	s, f, _ := setup(t)
	uid := f.add("INBOX", nil, rfc("a", "a@x.org", "Mon, 01 Jan 2024 10:00:00 +0000", "x"))
	if err := s.DeleteMessage(context.Background(), uid, nil); err != nil {
		t.Fatal(err)
	}
	if len(cmdsMatching(f, "UID COPY 1 ")) != 1 || len(f.uids("[Gmail]/Trash")) != 1 {
		t.Fatalf("%v", f.commands())
	}
}

// ---- BODYSTRUCTURE indexing (no server) ---------------------------------------------------

func sp(typ, sub string, params map[string]string, id, enc string, size uint32, disp string, dparams map[string]string) *imap.BodyStructureSinglePart {
	p := &imap.BodyStructureSinglePart{Type: typ, Subtype: sub, Params: params, ID: id, Encoding: enc, Size: size,
		Extended: &imap.BodyStructureSinglePartExt{}}
	if disp != "" {
		p.Extended.Disposition = &imap.BodyStructureDisposition{Value: disp, Params: dparams}
	}
	return p
}

func TestIndexBodystructure(t *testing.T) {
	inner := &imap.BodyStructureMultiPart{Subtype: "alternative", Children: []imap.BodyStructure{
		sp("text", "plain", map[string]string{"charset": "utf-8"}, "", "7bit", 5, "", nil),
		sp("text", "html", nil, "", "7bit", 9, "", nil),
	}}
	fwd := sp("message", "rfc822", nil, "", "7bit", 100, "", nil)
	fwd.MessageRFC822 = &imap.BodyStructureMessageRFC822{BodyStructure: inner}
	single := sp("message", "rfc822", nil, "", "7bit", 100, "attachment", map[string]string{"filename": "fwd.eml"})
	single.MessageRFC822 = &imap.BodyStructureMessageRFC822{BodyStructure: sp("text", "plain", nil, "", "7bit", 3, "", nil)}
	root := &imap.BodyStructureMultiPart{Subtype: "mixed", Children: []imap.BodyStructure{
		sp("text", "plain", nil, "", "7bit", 4, "", nil), // 1  render
		fwd, // 2  message/rfc822 -> 2.1 plain, 2.2 html
		sp("image", "png", nil, "<c1@x>", "base64", 50, "", nil), // 3  attachment (Content-ID, not text)
		sp("application", "pdf", nil, "", "base64", 70, "attachment", map[string]string{"filename*0*": "utf-8''r%C3%A9", "filename*1*": "sum%C3%A9.pdf"}), // 4
		single, // 5 attachment: the message part is flattened, its text is 5.1 (a render part)
		sp("text", "html", nil, "<h@x>", "7bit", 8, "", nil), // 6 text with a Content-ID stays a render part
	}}
	pm := indexBodystructure(root)
	if pm == nil {
		t.Fatal("nil")
	}
	var render []string
	for _, p := range pm.render {
		render = append(render, pathKey(p.path)+"="+p.contentType())
	}
	want := "1=text/plain 2.1=text/plain 2.2=text/html 5.1=text/plain 6=text/html"
	if strings.Join(render, " ") != want {
		t.Fatalf("render parts %v", render)
	}
	if len(pm.attachments) != 2 || pathKey(pm.attachments[0].path) != "3" || pathKey(pm.attachments[1].path) != "4" {
		t.Fatalf("attachments %+v", pm.attachments)
	}
	if pm.attachments[0].contentID != "c1@x" || pm.attachments[0].octets != 50 || pm.attachments[0].encoding != "base64" {
		t.Fatalf("%+v", pm.attachments[0])
	}
	if got := pm.attachments[1].filename; got != "résumé.pdf" {
		t.Fatalf("rfc2231 continuation filename %q", got)
	}
	if c := inlineImageCandidates(pm); len(c) != 1 || pathKey(c[0].path) != "3" {
		t.Fatalf("inline candidates %+v", c)
	}
	// a message/rfc822 root numbers its parts 1.n
	r2 := sp("message", "rfc822", nil, "", "7bit", 1, "", nil)
	r2.MessageRFC822 = &imap.BodyStructureMessageRFC822{BodyStructure: inner}
	if p2 := indexBodystructure(r2); p2 == nil || pathKey(p2.render[0].path) != "1.1" || pathKey(p2.render[1].path) != "1.2" {
		t.Fatalf("%+v", p2)
	}
	// a plain single part is part 1
	if p3 := indexBodystructure(sp("text", "plain", nil, "", "7bit", 1, "", nil)); p3 == nil || pathKey(p3.render[0].path) != "1" {
		t.Fatalf("%+v", p3)
	}
}

func TestIndexBodystructureLimits(t *testing.T) {
	var deep imap.BodyStructure = sp("text", "plain", nil, "", "7bit", 1, "", nil)
	for i := 0; i < maxBodystructureDepth+5; i++ {
		deep = &imap.BodyStructureMultiPart{Subtype: "mixed", Children: []imap.BodyStructure{deep}}
	}
	if indexBodystructure(deep) != nil {
		t.Fatal("a structure deeper than the cap must be refused (full fetch fallback)")
	}
	wide := &imap.BodyStructureMultiPart{Subtype: "mixed"}
	for i := 0; i < maxBodystructureNodes+1; i++ {
		wide.Children = append(wide.Children, sp("text", "plain", nil, "", "7bit", 1, "", nil))
	}
	if indexBodystructure(wide) != nil {
		t.Fatal("too many parts must be refused")
	}
}

func TestParamsGet(t *testing.T) {
	cases := []struct {
		params map[string]string
		want   string
	}{
		{map[string]string{"filename": "plain.txt"}, "plain.txt"},
		{map[string]string{"filename": "=?utf-8?b?w6g=?="}, "è"},
		{map[string]string{"filename*": "iso-8859-1''caff%E8.txt"}, "caffè.txt"},
		{map[string]string{"filename*0": "ab", "filename*1": "cd"}, "abcd"},
		{map[string]string{"filename*1*": "%C3%A9", "filename*0*": "utf-8''x"}, "xé"},
		{map[string]string{"filename": "O'Brien's report.pdf"}, "O'Brien's report.pdf"},
		{map[string]string{}, ""},
	}
	for _, c := range cases {
		if got := paramsGet(c.params, "filename"); got != c.want {
			t.Errorf("%v: got %q want %q", c.params, got, c.want)
		}
	}
}

func TestMiniPartDecoding(t *testing.T) {
	p := &partInfo{maintype: "text", subtype: "plain", encoding: "quoted-printable", params: map[string]string{"charset": "iso-8859-1"}}
	got, err := decodeFetchedText(p, []byte("caff=E8"))
	if err != nil || got != "caffè" {
		t.Fatalf("%q %v", got, err)
	}
	bad := &partInfo{maintype: "text/x", subtype: "plain"}
	if _, err := decodeFetchedText(bad, []byte("x")); err == nil {
		t.Fatal("an invalid media type must trigger the fallback")
	}
	img := &partInfo{maintype: "image", subtype: "png", encoding: "base64"}
	if b, err := decodeFetchedBytes(img, []byte("iVBORw0KGgo=\r\n")); err != nil || string(b[1:4]) != "PNG" {
		t.Fatalf("%q %v", b, err)
	}
}
