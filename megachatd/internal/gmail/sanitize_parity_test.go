package gmail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The sanitizer parity tests: tricky e-mail HTML goes through the real Python
// sanitizer (server/gmail.py: nh3 + tinycss2 + css-inline, run by
// testdata/sanitize_parity.py) and through Service.sanitizeHTML. Skipped when
// python3 or its modules are missing (pip install nh3 tinycss2 css-inline).
//
//   - TestSanitizeParityWithPython: the hand-written corpus below; the outputs
//     must be byte-identical.
//   - TestSanitizeParityRandom: seeded random documents (nested markup, <style>
//     blocks with assorted selectors, duplicate and invalid attributes); the
//     outputs must be identical up to the differences listed on normalizeParity.
//
// Differences that are not worth (or not possible) to remove, all cosmetic:
//   - attribute order: golang.org/x/net/html sorts the attributes of formatting
//     elements (a, b, font, i, u, ...) while parsing, css-inline writes the
//     attributes of an element it styled in an order of its own;
//   - order of the properties inside a style attribute when several matching
//     rules declare the same property (the cascade result is the same);
//   - the parse tree of pathological markup (x/net/html is not html5ever).

type parityCase struct {
	Name string            `json:"name"`
	HTML string            `json:"html"`
	CIDs map[string]string `json:"cids,omitempty"`
}

var parityCIDs = map[string]string{"logo@x": "gmail-image?token=part-logo", "a b@x": "gmail-image?token=part-ab"}

var parityCorpus = []parityCase{
	{Name: "plain", HTML: `<p>Ciao <b>mondo</b></p>`},
	{Name: "empty-ish", HTML: ` `},
	{Name: "script-and-handlers", HTML: `<p onclick="steal()">Ciao <script>alert(1)</script><img src="https://t.example/p.gif" onerror="alert(1)"><b>x</b></p>`},
	{Name: "inline-style-allowed", HTML: `<p style="color:#ff0000; font-size: 14px; font-weight:bold; text-align:center">x</p>`},
	{Name: "inline-style-disallowed", HTML: `<p style="position:fixed;top:0;color:red;behavior:url(x.htc);width:expression(alert(1))">x</p>`},
	{Name: "inline-style-url", HTML: `<div style="background:url(https://evil.example/x.png) no-repeat;color:blue">x</div><div style="background-image:url('javascript:alert(1)')">y</div>`},
	{Name: "inline-style-colors", HTML: `<p style="color:rgb(1, 2, 3);background-color:hsl(120,50%,50%);border:1px solid #AbC">x</p><p style="color:RED;background:transparent">y</p>`},
	{Name: "inline-style-important", HTML: `<p style="color:red !important;margin:0 auto">x</p>`},
	{Name: "inline-style-sizes", HTML: `<p style="margin:0 0 10px 0;padding:1em 2em;line-height:1.5;letter-spacing:2px;max-width:600px;width:100%">x</p>`},
	{Name: "inline-style-garbage", HTML: `<p style="color:red;;;:;;{}}} color">x</p><p style="/* c */ color: blue">y</p>`},
	{Name: "style-block-class", HTML: `<html><head><style>.a{color:red} .b{font-weight:bold}</style></head><body><p class="a b">x</p><p class="b">y</p></body></html>`},
	{Name: "style-block-id-type", HTML: `<style>p{margin:0} #x{color:blue} div p{padding:2px}</style><div><p id="x">a</p></div><p>b</p>`},
	{Name: "style-block-specificity", HTML: `<style>p{color:red} .c{color:green} #i{color:blue} p.c{color:black}</style><p class="c" id="i">a</p><p class="c">b</p><p>c</p>`},
	{Name: "style-block-order", HTML: `<style>p{color:red} p{color:green}</style><p>a</p>`},
	{Name: "style-vs-inline", HTML: `<style>p{color:red;margin:0}</style><p style="color:blue">a</p>`},
	{Name: "style-important-vs-inline", HTML: `<style>p{color:red !important}</style><p style="color:blue">a</p>`},
	{Name: "style-important-vs-rule", HTML: `<style>p{color:red !important} .c{color:green}</style><p class="c">a</p>`},
	{Name: "style-child-combinator", HTML: `<style>div > p{color:red} ul li + li{color:blue} h1 ~ p{color:green}</style><div><p>a</p></div><ul><li>1</li><li>2</li></ul><h1>t</h1><p>u</p>`},
	{Name: "style-first-last-child", HTML: `<style>li:first-child{color:red} li:last-child{color:blue}</style><ul><li>1</li><li>2</li><li>3</li></ul>`},
	{Name: "style-nth-child", HTML: `<style>tr:nth-child(2n+1){background-color:#eee} td:nth-of-type(2){color:red}</style><table><tr><td>1</td><td>2</td></tr><tr><td>3</td><td>4</td></tr><tr><td>5</td><td>6</td></tr></table>`},
	{Name: "style-not", HTML: `<style>p:not(.x){color:red}</style><p class="x">a</p><p>b</p>`},
	{Name: "style-attr-selectors", HTML: `<style>a[href]{color:red} a[target=_blank]{color:blue} a[href^="https"]{font-weight:bold} img[width="10"]{margin:0}</style><a href="http://x.example">a</a><a href="https://y.example" target="_blank">b</a>`},
	{Name: "style-attr-substring-star", HTML: `<style>[class*="btn"]{color:red} a[href*="example"]{font-weight:bold}</style><a class="my-btn" href="https://example.org">a</a>`},
	{Name: "style-universal", HTML: `<style>*{margin:0;padding:0}</style><div><p>a</p></div>`},
	{Name: "style-universal-descendant", HTML: `<style>div *{color:red}</style><div><p>a</p><span>b</span></div>`},
	{Name: "style-pseudo-stateful", HTML: `<style>a:hover{color:red} a:visited{color:blue} a{text-decoration:none} p::before{color:red} a:focus, a{color:green}</style><a href="https://x.example">a</a><p>b</p>`},
	{Name: "style-at-rules", HTML: `<style>@import url(https://evil.example/x.css); @font-face{font-family:x;src:url(y)} @media (max-width:600px){p{color:red}} p{color:blue}</style><p>a</p>`},
	{Name: "style-comma-list", HTML: `<style>h1, h2, .t{color:red;margin:0}</style><h1>a</h1><h2>b</h2><p class="t">c</p>`},
	{Name: "style-invalid-selector", HTML: `<style>p{color:red} div[{color:blue} .ok{font-weight:bold} :::{color:green}</style><p class="ok">a</p>`},
	{Name: "style-disallowed-props", HTML: `<style>p{position:absolute;color:red;display:none;z-index:9;font-family:Arial, sans-serif}</style><p>a</p>`},
	{Name: "style-url-in-rule", HTML: `<style>p{background:url(https://evil.example/t.png);color:red}</style><p>a</p>`},
	{Name: "style-in-body", HTML: `<div>a</div><style>div{color:red}</style><div>b</div>`},
	{Name: "style-multiple", HTML: `<style>p{color:red}</style><style>p{margin:0}</style><style media="print">p{display:none}</style><p>a</p>`},
	{Name: "style-no-head", HTML: `<style>b{color:red}</style><b>x</b>`},
	{Name: "style-merge-existing-props", HTML: `<style>td{padding:4px;color:red}</style><table><tr><td style="padding:10px;margin:1px">a</td></tr></table>`},
	{Name: "style-shorthand-case", HTML: `<style>P{COLOR:Red;Margin:0 AUTO}</style><p>a</p>`},
	{Name: "link-stylesheet", HTML: `<link rel="stylesheet" href="https://evil.example/a.css"><p>a</p>`},
	{Name: "tables-legacy", HTML: `<table width="600" cellpadding="0" cellspacing="0" border="0" bgcolor="#ffffff" align="center"><tr><td width="50%" valign="top" bgcolor="red" colspan="2">a</td></tr></table>`},
	{Name: "tables-structure", HTML: `<table><caption>c</caption><colgroup><col width="10"></colgroup><thead><tr><th scope="col">h</th></tr></thead><tbody><tr><td>d</td></tr></tbody><tfoot><tr><td>f</td></tr></tfoot></table>`},
	{Name: "tables-bad-attrs", HTML: `<table width="abc" cellpadding="-1" border="999999" bgcolor="javascript:x" align="evil"><tr><td colspan="x" rowspan="99999999">a</td></tr></table>`},
	{Name: "tables-nested-malformed", HTML: `<table><tr><td><table><tr><td>in</td></tr></table><td>x</tr></table>text`},
	{Name: "img-remote", HTML: `<img src="https://cdn.example/a.png" width="100" height="50" alt="A &amp; B" style="border:0">`},
	{Name: "img-remote-http-and-spaces", HTML: `<img src="  http://cdn.example/a b.png  "><img src="//cdn.example/x.png"><img src="ftp://x/y.png">`},
	{Name: "img-data-uri", HTML: `<img src="data:image/png;base64,iVBORw0KGgo="><img src="data:text/html;base64,PHNjcmlwdD4="><img src="data:image/svg+xml;base64,PHN2Zz4=">`},
	{Name: "img-cid", HTML: `<img src="cid:logo@x" alt="l"><img src="CID:LOGO@X"><img src="cid:%3Clogo@x%3E"><img src="cid:a%20b@x"><img src="cid:unknown@x">`},
	{Name: "img-cid-mapped", HTML: `<img src="cid:logo@x"><img src="cid:a%20b@x"><img src="cid:nope@x">`, CIDs: parityCIDs},
	{Name: "img-srcset-sizes", HTML: `<img src="https://a.example/1.png" srcset="https://a.example/2.png 2x" sizes="100vw" loading="lazy" onload="x()">`},
	{Name: "img-no-src-and-bad", HTML: `<img><img src=""><img src="javascript:alert(1)"><img src="vbscript:x">`},
	{Name: "img-long-url", HTML: `<img src="https://a.example/` + strings.Repeat("x", 3000) + `">`},
	{Name: "img-bg-attr", HTML: `<table background="https://a.example/bg.png"><tr><td background="cid:logo@x">a</td></tr></table><body background="https://a.example/b.png">z</body>`},
	{Name: "picture-source", HTML: `<picture><source srcset="https://a.example/x.webp"><img src="https://a.example/x.png"></picture>`},
	{Name: "js-urls", HTML: `<a href="javascript:alert(1)">1</a><a href="JaVaScRiPt:alert(1)">2</a><a href=" javascript:alert(1)">3</a><a href="java&#x09;script:alert(1)">4</a><a href="java&#115;cript:alert(1)">5</a><a href="&#x6A;avascript:alert(1)">6</a><a href="data:text/html,<script>alert(1)</script>">7</a><a href="vbscript:x">8</a>`},
	{Name: "safe-urls", HTML: `<a href="https://example.com/a?b=1&c=2#f">1</a><a href="http://example.com">2</a><a href="mailto:a@b.it">3</a><a href="tel:+390123">4</a><a href="#top">5</a><a href="/rel">6</a><a href="ftp://x.example/f">7</a>`},
	{Name: "link-attrs", HTML: `<a href="https://x.example" target="_blank" rel="nofollow" title="t" name="n" id="i" class="c" download>a</a>`},
	{Name: "malformed-unclosed", HTML: `<p>one<p>two<b>bold<i>both</b>italic</i><div>d`},
	{Name: "malformed-stray-lt", HTML: `a < b and c > d <3 <p>x</p> <<>> <a<b>`},
	{Name: "malformed-attrs", HTML: `<p class=a id=b title=x"y>z</p><p =x>q</p><p "x">r</p><div a='1' a='2'>dup</div>`},
	{Name: "malformed-tag-names", HTML: `<p/>x<br/><br></br><hr/ >a<b/>c</b><!-- unterminated`},
	{Name: "nested-quotes", HTML: `<p title='he said "hi"' data-x="it's">a</p><a href='https://x.example/?q="a"&r=1'>b</a><p style="font-family:'Helvetica Neue', Arial">c</p>`},
	{Name: "nested-quotes-style", HTML: `<p style='font-family:"Segoe UI", sans-serif;color:red'>a</p><p style="font-family:&quot;Segoe UI&quot;">b</p>`},
	{Name: "attr-entities", HTML: `<a href="https://x.example/?a=1&amp;b=2&lt;" title="&lt;script&gt;alert(1)&lt;/script&gt;">x &amp; y &lt; z &nbsp; &copy; &#8364; &#x20AC;</a>`},
	{Name: "mxss-noscript", HTML: `<noscript><p title="</noscript><img src=x onerror=alert(1)>">`},
	{Name: "mxss-style-comment", HTML: `<style><!--</style><img src=x onerror=alert(1)>--></style><p>a</p>`},
	{Name: "mxss-svg-style", HTML: `<svg><style><img src=x onerror=alert(1)></style></svg><math><mtext><table><mglyph><style><img src=x onerror=alert(1)>`},
	{Name: "mxss-textarea", HTML: `<textarea><img src=x onerror=alert(1)></textarea><title><img src=x onerror=alert(1)></title><p>a</p>`},
	{Name: "dangerous-tags", HTML: `<iframe src="https://x.example"></iframe><object data="x"></object><embed src="x"><form action="x"><input name=a><button>b</button><select><option>o</option></select><textarea>t</textarea></form><base href="https://evil.example/"><meta http-equiv="refresh" content="0;url=https://evil.example"><applet code=x></applet><frame src=x><audio src=x></audio><video src=x></video><canvas></canvas>`},
	{Name: "svg-math", HTML: `<svg onload="alert(1)"><circle r="5"/><script>alert(1)</script></svg><math><mi xlink:href="javascript:alert(1)">x</mi></math><p>after</p>`},
	{Name: "comments-conditional", HTML: `<!--[if mso]><table><tr><td><![endif]--><p>a</p><!--[if mso]></td></tr></table><![endif]--><!-- plain comment --><![CDATA[x]]>`},
	{Name: "uppercase-whitespace", HTML: "<P CLASS=\"X\"\n  STYLE=\"COLOR : RED\"\n>A</P\n><BR><TABLE><TR><TD>b</TD></TR></TABLE>"},
	{Name: "blockquote-quote", HTML: `<blockquote data-quote="block">q</blockquote><div data-quote="after">a</div><div data-quote="evil">b</div><blockquote type="cite" class="gmail_quote">c</blockquote>`},
	{Name: "font-center-legacy", HTML: `<font color="#FF0000" face="Arial, Helvetica" size="3">a</font><font size="+1">b</font><font color="expression(x)" face="a;b" size="99">c</font><center>d</center><u>u</u><s>s</s><strike>k</strike><small>sm</small><big>bg</big>`},
	{Name: "text-formatting", HTML: `<h1>t</h1><h2>t</h2><h6>t</h6><ul><li>a<ol><li>b</li></ol></li></ul><dl><dt>t</dt><dd>d</dd></dl><pre>  keep   spaces\n</pre><code>c</code><kbd>k</kbd><sub>s</sub><sup>p</sup><abbr title="t">a</abbr><hr><br>`},
	{Name: "block-misc-tags", HTML: `<section><article><header>h</header><nav>n</nav><aside>a</aside><footer>f</footer><main>m</main></article></section><details><summary>s</summary>d</details><span lang="it" dir="rtl">x</span><div dir="evil" align="center" hidden>y</div>`},
	{Name: "unicode", HTML: `<p>caffè ☕ — “quoted” 日本語 ‮ rtl \u0000 nul</p><p title="é">ü</p>`},
	{Name: "nbsp-and-zero-width", HTML: "<p>a&nbsp;b\u00a0c\u200bd\ufeffe</p>"},
	{Name: "newsletter", HTML: `<!DOCTYPE html><html><head><meta charset="utf-8"><title>News</title><style>body{margin:0;background-color:#f4f4f4} .container{width:600px;margin:0 auto;background:#fff} .btn{display:inline-block;padding:10px 20px;background-color:#0066cc;color:#fff;text-decoration:none;border-radius:4px} h1{font-family:Georgia, serif;font-size:24px;color:#333} @media only screen and (max-width:600px){.container{width:100% !important}}</style></head><body><table class="container" align="center"><tr><td><h1>Hello</h1><p>Text</p><a class="btn" href="https://example.com/go">Go</a><img src="https://cdn.example/banner.jpg" width="600"></td></tr></table></body></html>`},
	{Name: "deep-nesting", HTML: strings.Repeat("<div>", 200) + "x" + strings.Repeat("</div>", 200)},
	{Name: "template-slot", HTML: `<template><p>t</p></template><slot>s</slot><dialog open>d</dialog><p>a</p>`},
	{Name: "data-attrs-aria", HTML: `<p data-foo="bar" aria-label="l" role="button" tabindex="0" contenteditable="true" draggable="true" accesskey="x" onmouseover="x()">a</p>`},
	{Name: "meta-head-only", HTML: `<html><head><title>t</title><meta name="viewport" content="width=device-width"><style>p{color:red}</style></head></html>`},
	{Name: "style-only", HTML: `<style>p{color:red}</style>`},
	{Name: "script-only", HTML: `<script>alert(1)</script>`},
	{Name: "text-only", HTML: `just text & <not a tag`},
	{Name: "br-hr-img-void", HTML: `<br><br/><hr><hr/><img src="https://a.example/x.png"/><p>a<br>b</p>`},
	{Name: "entities-in-style-attr", HTML: `<p style="font-family:&#39;Arial&#39;;color:&#35;f00">a</p>`},
	{Name: "css-escapes", HTML: `<p style="col\6f r:red;background:\75rl(https://evil.example/x)">a</p><style>p{c\6flor:red}</style>`},
	{Name: "border-radius-shadow", HTML: `<div style="border-radius:4px 8px;box-shadow:0 0 5px #000;border-top:1px solid #ccc;text-shadow:1px 1px red">a</div>`},
	{Name: "inl-important-both-sides", HTML: `<style>p{color:red !important;margin:1px}</style><p style="color:blue !important;margin:2px">a</p>`},
	{Name: "inl-important-order", HTML: `<style>p{margin:1px !important;color:red} p.c{color:green !important;padding:3px}</style><p class="c" style="padding:9px;color:blue;margin:2px">a</p>`},
	{Name: "inl-important-multiple-rules", HTML: `<style>.a{color:red !important} #b{color:green !important} p{color:blue !important}</style><p id="b" class="a">x</p><p class="a">y</p>`},
	{Name: "inl-same-prop-twice", HTML: `<style>p{color:red;margin:0;color:blue}</style><p>a</p>`},
	{Name: "inl-shorthand-longhand", HTML: `<style>p{margin:0} .x{margin-top:5px}</style><p class="x">a</p>`},
	{Name: "inl-pseudo-specificity", HTML: `<style>li:first-child{color:red} .x{color:blue}</style><ul><li class="x">a</li><li>b</li></ul>`},
	{Name: "inl-attr-vs-class-specificity", HTML: `<style>a[href]{color:red} .x{color:blue}</style><a class="x" href="#">a</a>`},
	{Name: "inl-type-vs-universal", HTML: `<style>*{color:red} p{color:blue} .z *{color:green}</style><div class="z"><p>a</p></div>`},
	{Name: "inl-star-in-compound", HTML: `<style>*.k{color:red} *:first-child{margin:0} div>*{padding:1px}</style><div><p class="k">a</p><p>b</p></div>`},
	{Name: "inl-attr-ops", HTML: `<style>[lang|=en]{color:red} [title~=b]{color:blue} [id$=z]{color:green} [data-x^=ab]{margin:0} [title="B" i]{padding:0}</style><p lang="en-US" title="a b" id="xz" data-x="abc">a</p>`},
	{Name: "inl-attr-star-quoted", HTML: `<style>[title="a*b"]{color:red} [title*="*"]{color:blue}</style><p title="a*b">a</p><p title="c">b</p>`},
	{Name: "inl-structural", HTML: `<style>p:only-child{color:red} p:empty{margin:0} span:first-of-type{padding:0} li:nth-last-child(1){color:blue} li:nth-child(odd){margin:1px}</style><div><p></p></div><div><span>a</span><span>b</span></div><ul><li>1</li><li>2</li><li>3</li></ul>`},
	{Name: "inl-body-html", HTML: `<style>html{background-color:#eee} body{margin:0;font-family:Arial} body p{color:red}</style><html><body><p>a</p></body></html>`},
	{Name: "inl-nested-tables", HTML: `<style>td{padding:0} table table td{padding:5px} td.x{color:red}</style><table><tr><td><table><tr><td class="x">a</td></tr></table></td></tr></table>`},
	{Name: "inl-comma-in-not", HTML: `<style>p:not(.a, .b){color:red}</style><p class="a">1</p><p class="c">2</p>`},
	{Name: "inl-is-where", HTML: `<style>:is(h1, h2){color:red} :where(p){margin:0}</style><h1>a</h1><h2>b</h2><p>c</p>`},
	{Name: "inl-selector-whitespace", HTML: `<style>  div   >   p  ,  span  { color : red ; margin : 0 }</style><div><p>a</p></div><span>b</span>`},
	{Name: "inl-selector-case", HTML: `<style>DIV.Foo{color:red} #Bar{margin:0}</style><div class="Foo" id="Bar">a</div><div class="foo" id="bar">b</div>`},
	{Name: "inl-escaped-selector", HTML: `<style>.a\:b{color:red} #x\31 {margin:0}</style><p class="a:b" id="x1">a</p>`},
	{Name: "inl-existing-style-empty", HTML: `<style>p{color:red}</style><p style="">a</p><p style="  ">b</p><p style=";;">c</p>`},
	{Name: "inl-style-attr-no-match", HTML: `<style>.nope{color:red}</style><p style="color:blue;  margin : 0">a</p>`},
	{Name: "inl-style-attr-disallowed-only", HTML: `<style>p{color:red}</style><p style="position:fixed">a</p>`},
	{Name: "inl-many-rules", HTML: `<style>` + strings.Repeat("p{color:red} .q{margin:0}\n", 40) + `</style><p class="q">a</p>`},
	{Name: "inl-huge-selector-list", HTML: `<style>` + strings.Repeat("a, b, i, u, ", 30) + `p{color:red}</style><p>a</p><b>b</b>`},
	{Name: "inl-fallback-too-big-css", HTML: `<style>p{color:red}</style><p>` + strings.Repeat("x", 30) + `</p>`},
	{Name: "inl-style-with-comments", HTML: `<style>/* c */ p /* x */ { /* y */ color /* z */ : red /* w */ ; } <!-- --> .a{margin:0}</style><p class="a">a</p>`},
	{Name: "inl-cdata-style", HTML: `<style><![CDATA[p{color:red}]]></style><p>a</p>`},
	{Name: "inl-style-entities", HTML: `<style>p::after{content:"&lt;"} p{font-family:&quot;A&quot;;color:red}</style><p>a</p>`},
	{Name: "inl-svg-style", HTML: `<svg><style>p{color:red}</style></svg><p>a</p>`},
	{Name: "inl-noscript-style", HTML: `<noscript><style>p{color:red}</style></noscript><p>a</p>`},
	{Name: "inl-style-after-html", HTML: `<html><body><p>a</p></body></html><style>p{color:red}</style>`},
	{Name: "inl-uppercase-style-tag", HTML: `<STYLE>P{COLOR:RED}</STYLE><P>a</P>`},
	{Name: "inl-numbers-in-rule", HTML: `<style>p{width:.5em;margin:1.0px;line-height:1.40;opacity:.50;font-size:1E1px}</style><p>a</p>`},
	{Name: "inl-colors-in-rule", HTML: `<style>p{color:rgba(0,0,0,.5);background-color:RGB(1 , 2 ,3);border-color:#ABC}</style><p>a</p>`},
	{Name: "vertical-align-etc", HTML: `<td style="vertical-align:middle;text-indent:10px;white-space:nowrap;word-break:break-all;overflow:hidden;opacity:.5;list-style-type:none">a</td>`},
}

// pythonSanitize runs the reference sanitizer on cases, or skips the test.
func pythonSanitize(t *testing.T, cases []parityCase) map[string]string {
	t.Helper()
	if testing.Short() {
		t.Skip("short mode")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(py, "-c", "import nh3, tinycss2, css_inline").CombinedOutput(); err != nil {
		t.Skipf("python modules missing (pip install nh3 tinycss2 css-inline): %s", out)
	}
	in, _ := json.Marshal(cases)
	cmd := exec.Command(py, filepath.Join("testdata", "sanitize_parity.py"), root)
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python reference failed: %v\n%s", err, stderr.String())
	}
	want := map[string]string{}
	if err := json.Unmarshal(stdout.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	return want
}

var parityTokenRE = regexp.MustCompile(`(data-(?:src|bg)="gmail-image\?token=)([^"&]+)([^"]*")`)

// goSanitize is Service.sanitizeHTML with the image proxy tokens replaced by
// the URL they sign (every process signs with its own key).
func goSanitize(s *Service, c parityCase) string {
	return parityTokenRE.ReplaceAllStringFunc(s.sanitizeHTML(c.HTML, c.CIDs), func(m string) string {
		sub := parityTokenRE.FindStringSubmatch(m)
		url := s.verifyImageToken(sub[2])
		if url == "" {
			url = "<invalid token>"
		}
		return sub[1] + "URL:" + url + sub[3]
	})
}

func TestSanitizeParityWithPython(t *testing.T) {
	want := pythonSanitize(t, parityCorpus)
	s := testSvc(t)
	for _, c := range parityCorpus {
		t.Run(c.Name, func(t *testing.T) {
			got := goSanitize(s, c)
			if w, ok := want[c.Name]; !ok {
				t.Fatal("no reference output")
			} else if got != w {
				t.Errorf("parity mismatch\n html: %s\n  go:  %s\n  py:  %s", c.HTML, got, w)
			}
		})
	}
}

var (
	styleAttrRE = regexp.MustCompile(`style="([^"]*)"`)
	fmtTagRE    = regexp.MustCompile(`<([a-z0-9]+)((?: [a-z-]+="[^"]*")+)( ?/?)>`)
	attrRE      = regexp.MustCompile(` [a-z-]+="[^"]*"`)
)

// normalizeParity removes the cosmetic differences described at the top of the
// file: attribute order and the order of the properties of a style attribute.
func normalizeParity(s string) string {
	s = styleAttrRE.ReplaceAllStringFunc(s, func(m string) string {
		parts := strings.Split(styleAttrRE.FindStringSubmatch(m)[1], ";")
		sort.Strings(parts)
		return `style="` + strings.Join(parts, ";") + `"`
	})
	return fmtTagRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := fmtTagRE.FindStringSubmatch(m)
		attrs := attrRE.FindAllString(sub[2], -1)
		sort.Strings(attrs)
		return "<" + sub[1] + strings.Join(attrs, "") + sub[3] + ">"
	})
}

var (
	randTags  = strings.Fields("p div span a b i table tr td th ul li img h1 font center blockquote pre br hr section style script svg form input u tbody small")
	randAttrs = []string{`class="a"`, `class="b c"`, `id="x"`, `id="y"`, `style="color:red"`, `style="margin:0 auto;padding:1px"`, `style="color:blue !important"`,
		`href="https://e.com/?a=1&b=2"`, `href="javascript:alert(1)"`, `src="https://i.example/a.png"`, `src="cid:logo@x"`, `width="100"`, `bgcolor="#fff"`,
		`align="center"`, `title="t&amp;u"`, `onclick="x()"`, `data-x="1"`, `style="font-family: Arial, sans-serif;width:.5em"`, `style="background:url(http://x/y)"`,
		`dir="rtl"`, `colspan="2"`, `target="_blank"`, `title='q"q'`, `style="position:fixed;color:#ABC"`, `lang="en"`}
	randSels = []string{"p", "div", "span", "a", ".a", ".b", "#x", "#y", "div p", "div > p", "ul li + li", "h1 ~ p", "li:first-child", "li:last-child",
		"td:nth-child(2)", "p:not(.a)", "a[href]", `[class*="b"]`, `a[href^="https"]`, "*", "div *", "*.a", "p.a.b", "table td", "tr:nth-child(odd)",
		".a, .b", "body", "html", "td, th", "a:hover", "p::first-line", "div:empty", "span:only-child", "li:nth-of-type(2n+1)", "[id]", `p[class="a"]`,
		"b i", "center > *", ":is(p,div)"}
	randProps = []string{"color:red", "color:#00F", "margin:0", "margin:0 auto", "padding:2px 4px", "font-size:14px", "font-weight:bold", "background-color:#eee",
		"border:1px solid #ccc", "text-align:center", "width:50%", "line-height:1.4", "display:none", "float:left", "font-family:Georgia, serif",
		"color:red !important", "margin-top:5px !important", "opacity:.5", "width:100.0%", "border-radius:4px"}
)

func randomParityCases(seed uint64, n int) []parityCase {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	pick := func(l []string) string { return l[r.IntN(len(l))] }
	sample := func(l []string, k int) []string {
		out := make([]string, 0, k)
		for _, i := range r.Perm(len(l))[:k] {
			out = append(out, l[i])
		}
		return out
	}
	css := func() string {
		var rules []string
		for i := 1 + r.IntN(6); i > 0; i-- {
			rules = append(rules, pick(randSels)+"{"+strings.Join(sample(randProps, 1+r.IntN(4)), ";")+"}")
		}
		return strings.Join(rules, " ")
	}
	var node func(depth int) string
	node = func(depth int) string {
		tag := pick(randTags)
		switch tag {
		case "style":
			return "<style>" + css() + "</style>"
		case "script":
			return "<script>alert(1)</script>"
		}
		at := strings.Join(sample(randAttrs, r.IntN(4)), " ")
		switch tag {
		case "br", "hr", "img", "input":
			return fmt.Sprintf("<%s %s>", tag, at)
		}
		inner := ""
		if depth < 4 {
			for i := r.IntN(4); i > 0; i-- {
				inner += node(depth + 1)
			}
		}
		inner += pick([]string{"", "text", "x &amp; y &lt; z", " é "})
		if r.IntN(10) == 0 {
			return fmt.Sprintf("<%s %s>%s", tag, at, inner) // never closed
		}
		return fmt.Sprintf("<%s %s>%s</%s>", tag, at, inner, tag)
	}
	cases := make([]parityCase, n)
	for i := range cases {
		body := ""
		for j := 1 + r.IntN(4); j > 0; j-- {
			body += node(0)
		}
		if r.IntN(10) < 6 {
			body = "<style>" + css() + "</style>" + body
		}
		if r.IntN(10) < 3 {
			body = "<html><head><title>t</title></head><body>" + body + "</body></html>"
		}
		cases[i] = parityCase{Name: fmt.Sprintf("r%d-%d", seed, i), HTML: body}
		if r.IntN(2) == 0 {
			cases[i].CIDs = map[string]string{"logo@x": "gmail-image?token=part"}
		}
	}
	return cases
}

func TestSanitizeParityRandom(t *testing.T) {
	var cases []parityCase
	for _, seed := range []uint64{1, 2, 3} {
		cases = append(cases, randomParityCases(seed, 250)...)
	}
	want := pythonSanitize(t, cases)
	s := testSvc(t)
	bad := 0
	for _, c := range cases {
		got, w := normalizeParity(goSanitize(s, c)), normalizeParity(want[c.Name])
		if got == w {
			continue
		}
		bad++
		k := 0
		for k < len(got) && k < len(w) && got[k] == w[k] {
			k++
		}
		lo := max(0, k-100)
		t.Errorf("%s differs at byte %d\n html: %s\n  go:  ...%s\n  py:  ...%s", c.Name, k, c.HTML, got[lo:min(len(got), k+120)], w[lo:min(len(w), k+120)])
	}
	t.Logf("%d random documents, %d differences", len(cases), bad)
}
