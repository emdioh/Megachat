package gmail

import (
	"regexp"
	"strings"
	"testing"
)

func testSvc(t testing.TB) *Service {
	s, _ := newTestService(t, nil, nil)
	return s
}

var imgSrcRE = regexp.MustCompile(`<img\b[^>]*\bdata-src="([^"]*)"`)

func TestSanitizeStripsScriptsAndHandlers(t *testing.T) {
	s := testSvc(t)
	raw := `<p onclick="steal()">Ciao <script>alert(1)</script><img src="https://tracker.example/pixel" onerror="alert(1)"><b>grassetto</b></p>`
	got := s.sanitizeHTML(raw, nil)
	for _, bad := range []string{"<script", "onclick", "onerror", "alert"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q present in %s", bad, got)
		}
	}
	m := imgSrcRE.FindAllStringSubmatch(got, -1)
	if len(m) != 1 || !strings.HasPrefix(m[0][1], "gmail-image?token=") {
		t.Fatalf("img: %s", got)
	}
	if !strings.Contains(got, "<b>grassetto</b>") || !strings.Contains(got, "Ciao") {
		t.Fatalf("content lost: %s", got)
	}
}

func TestSanitizeLinks(t *testing.T) {
	s := testSvc(t)
	got := s.sanitizeHTML(`<a href="https://example.com/x" onclick="x()">ok</a><a href="javascript:alert(1)">bad</a><a href="/relative">rel</a>`, nil)
	if !strings.Contains(got, `href="https://example.com/x"`) || !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Fatalf("good link lost: %s", got)
	}
	for _, bad := range []string{"javascript:", "/relative", "onclick"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q in %s", bad, got)
		}
	}
}

func TestSanitizeStylesAndRemoteResources(t *testing.T) {
	s := testSvc(t)
	got := s.sanitizeHTML(`<style>@import url(https://evil.example/x.css)</style><p style="background:url(https://evil.example/pixel)">testo</p><iframe src="https://evil.example/frame"></iframe>`, nil)
	for _, bad := range []string{"<style", "style=", "background", "<iframe"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q in %s", bad, got)
		}
	}
	if !strings.Contains(got, "testo") {
		t.Fatal(got)
	}
}

func TestSanitizeTablesAndLegacyAttrs(t *testing.T) {
	s := testSvc(t)
	got := s.sanitizeHTML(`<table width="600" cellpadding="0" bgcolor="#FFF" border="99"><tr><td colspan="2" align="CENTER" onclick="x">cella</td></tr></table>`, nil)
	for _, want := range []string{`<table`, `width="600"`, `cellpadding="0"`, `bgcolor="#fff"`, `colspan="2"`, `align="center"`, "cella"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "border=") || strings.Contains(got, "onclick") {
		t.Fatalf("unsafe attr kept: %s", got)
	}
}

func TestSanitizeStyleAllowList(t *testing.T) {
	cases := []struct{ in, want string }{
		{"color: red; background: #FFF", "color:red;background-color:#FFF"},
		{"color: red !important", "color:red"},
		{"width: 100%; position: fixed", "width:100%"},
		{"font-family: 'Helvetica Neue', Arial", `font-family:"Helvetica Neue", Arial`},
		{"margin: 0 auto", "margin:0 auto"},
		{"width: 5000px", ""},
		{"color: expression(alert(1))", ""},
		{"background-image: url(http://x/y)", ""},
		{"font-weight: 700", "font-weight:700"},
		{"font-weight: 750.5", ""},
		{"color: rgb(1, 2, 3)", "color:rgb(1, 2, 3)"},
		{"color: rgb(300, 2, 3)", ""},
		{"line-height: 1.5", "line-height:1.5"},
		{"border: 1px solid #ccc", "border:1px solid #ccc"},
		{"border-radius: 4px / 8px", "border-radius:4px / 8px"},
		{"opacity: 0.5", "opacity:0.5"},
		{"color: var(--x)", ""},
		{"color: inherit", ""},
	}
	for _, c := range cases {
		got, ok := sanitizeStyle(c.in)
		if c.want == "" {
			if ok {
				t.Errorf("%q: want dropped, got %q", c.in, got)
			}
			continue
		}
		if !ok || got != c.want {
			t.Errorf("%q: got %q ok=%v want %q", c.in, got, ok, c.want)
		}
	}
}

func TestSanitizeStyleTooManyDeclarations(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 33; i++ {
		b.WriteString("color:red;")
	}
	if _, ok := sanitizeStyle(b.String()); ok {
		t.Fatal("33 declarations must be rejected")
	}
}

func TestSanitizeRejectsOversize(t *testing.T) {
	s := testSvc(t)
	if got := s.sanitizeHTML("<p>"+strings.Repeat("a", maxHTMLChars)+"</p>", nil); got != "" {
		t.Fatal("oversized html accepted")
	}
	if got := s.sanitizeHTML(strings.Repeat("<div>", htmlMaxDepth+2), nil); got != "" {
		t.Fatal("too deep html accepted")
	}
}

func TestSanitizeImageSources(t *testing.T) {
	s := testSvc(t)
	got := s.sanitizeHTML(`<img src="javascript:alert(1)"><img src="ftp://x/y.png"><img src="data:image/png;base64,AAAA"><img src="data:text/html;base64,AAAA"><img src="cid:logo@x">`, map[string]string{"logo@x": "gmail-image?token=ZZ.00"})
	m := imgSrcRE.FindAllStringSubmatch(got, -1)
	var srcs []string
	for _, x := range m {
		srcs = append(srcs, x[1])
	}
	if len(srcs) != 2 || srcs[0] != "data:image/png;base64,AAAA" || srcs[1] != "gmail-image?token=ZZ.00" {
		t.Fatalf("srcs=%v html=%s", srcs, got)
	}
}

func TestImageTokenRoundTripAndTamper(t *testing.T) {
	s := testSvc(t)
	signed := s.signImageURL("https://example.com/a.png?x=1&y=2")
	m := signedImageRE.FindStringSubmatch(signed)
	if m == nil {
		t.Fatal(signed)
	}
	token := m[1] + "." + m[2]
	if s.verifyImageToken(token) != "https://example.com/a.png?x=1&y=2" {
		t.Fatal("roundtrip")
	}
	if s.verifyImageToken(m[1]+"."+strings.Repeat("0", 32)) != "" || s.verifyImageToken("garbage") != "" || s.verifyImageToken("") != "" {
		t.Fatal("tamper accepted")
	}
	// a signed proxy URL is kept (page-relative) by the src filter
	got := s.sanitizeHTML(`<img src="/`+signed+`">`, nil)
	if !strings.Contains(got, `data-src="`+strings.ReplaceAll(signed, "&", "&amp;")+`"`) {
		t.Fatal(got)
	}
}

func TestPartImageToken(t *testing.T) {
	s := testSvc(t)
	now := s.now()
	signed := s.signPartImageURL("INBOX", 12, 3, now)
	token := signedImageRE.FindStringSubmatch(signed)
	payload := s.verifyImageToken(token[1] + "." + token[2])
	f, uid, idx, ok := s.parsePartImage(payload, now)
	if !ok || f != "INBOX" || uid != 12 || idx != 3 {
		t.Fatal(payload)
	}
	if _, _, _, ok := s.parsePartImage(payload, now.Add(48*3600e9)); ok {
		t.Fatal("expired token accepted")
	}
}

func TestSanitizeBodyBackgroundAndQuotes(t *testing.T) {
	s := testSvc(t)
	got := s.sanitizeHTML(`<html><body bgcolor="#eeeeee" text="black"><div class="gmail_quote">q</div><td background="https://x.example/bg.png">c</td></body></html>`, nil)
	if !strings.Contains(got, `style="background-color:#eeeeee;color:black"`) {
		t.Fatalf("body colours: %s", got)
	}
	if !strings.Contains(got, `data-quote="block"`) {
		t.Fatalf("quote marker: %s", got)
	}
	if strings.Contains(got, "<body") || strings.Contains(got, `background="`) {
		t.Fatalf("body/background kept: %s", got)
	}
}

func TestSanitizeInlinesStyleBlocks(t *testing.T) {
	s := testSvc(t)
	raw := `<style>p.big{font-size:20px;color:red} #x{color:blue!important} a:hover{color:green} @media print{p{color:pink}}</style><p class="big" style="color:green">a</p><p id="x" style="color:black">b</p>`
	got := s.sanitizeHTML(raw, nil)
	if !strings.Contains(got, `style="font-size:20px;color:green"`) {
		t.Fatalf("inline precedence: %s", got)
	}
	if !strings.Contains(got, `style="color:blue"`) {
		t.Fatalf("important: %s", got)
	}
	if strings.Contains(got, "pink") || strings.Contains(got, "<style") || strings.Contains(got, "class=") {
		t.Fatal(got)
	}
	cfg := &testCfg{addr: "a", pass: "b", noInline: true, cacheDir: t.TempDir()}
	s2 := NewService(cfg, Deps{Config: cfg})
	if g := s2.sanitizeHTML(raw, nil); strings.Contains(g, "font-size") {
		t.Fatalf("inlining not disabled: %s", g)
	}
}

func TestSanitizeStylesDisabled(t *testing.T) {
	cfg := &testCfg{addr: "a", pass: "b", noStyles: true, cacheDir: t.TempDir()}
	s := NewService(cfg, Deps{Config: cfg})
	if g := s.sanitizeHTML(`<p style="color:red">x</p>`, nil); strings.Contains(g, "style=") {
		t.Fatal(g)
	}
}

func TestSanitizeIdempotent(t *testing.T) {
	s := testSvc(t)
	raw := `<table width="100%" style="margin:0 auto;color:red"><tr><td>a &amp; b &lt; c&nbsp;d</td></tr></table><a href="mailto:x@y.z">m</a><br><img src="https://e.example/i.png" alt="x &quot;q&quot;">`
	once := s.sanitizeHTML(raw, nil)
	twice := s.sanitizeHTML(once, nil)
	if once == "" || once != twice {
		t.Fatalf("not idempotent:\n%s\n%s", once, twice)
	}
	if !strings.Contains(once, "&amp;") || !strings.Contains(once, "&nbsp;") || !strings.Contains(once, "&lt;") || !strings.Contains(once, "<br>") {
		t.Fatalf("ammonia serialisation: %s", once)
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText(`<html><head><title>x</title></head><body><p>Hello <b>world</b></p><ul><li>one</li><li>two</li></ul><script>bad()</script><br>end &amp; done</body></html>`)
	want := "Hello world\n\n- one\n\n- two\n\nend & done"
	// collapse: block tags give single blank lines
	if !strings.Contains(got, "Hello world") || !strings.Contains(got, "- one") || !strings.Contains(got, "- two") || !strings.Contains(got, "end & done") || strings.Contains(got, "bad") {
		t.Fatalf("got %q (e.g. %q)", got, want)
	}
}
