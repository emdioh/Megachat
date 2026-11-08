// Package gmail is the Go port of the Gmail integration: server/gmail.py
// (IMAP transport, MIME parsing, HTML sanitising, SMTP send), the gmail.*
// operations of server/ops/gmail.py and the IMAP side of
// server/gmail_unread.py.
//
// Building blocks:
//   - IMAP: github.com/emersion/go-imap/v2/imapclient behind the pool of
//     service.go (imap.go), with the BODYSTRUCTURE partial-fetch path of
//     messages.get and attachment downloads in structure.go;
//   - CSS inlining of <style> blocks: github.com/vanng822/go-premailer with an
//     adaptation layer (inline.go) that keeps css-inline's semantics;
//   - HTML sanitising: golang.org/x/net/html plus bluemonday, with a
//     serialiser that reproduces nh3/ammonia (sanitize.go, ammonia_style.go),
//     covered by sanitize_test.go.
package gmail
