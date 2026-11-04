// Package gmail is the Go port of the Gmail integration: server/gmail.py
// (IMAP transport, MIME parsing, HTML sanitising, SMTP send), the gmail.*
// operations of server/ops/gmail.py and the IMAP side of
// server/gmail_unread.py.
//
// Dependency notes: the IMAP client is a small in-package implementation
// (imap.go) because github.com/emersion/go-imap/v2/imapclient needs
// go-message and go-sasl, which are not in go.sum / the module cache; the CSS
// inliner (inline.go) is in-package because go-premailer needs goquery,
// vanng822/css and cascadia, which are missing as well. HTML sanitising uses
// golang.org/x/net/html plus bluemonday.
package gmail
