package gmail

import (
	"crypto/tls"
	"strconv"

	"golang.org/x/text/unicode/norm"
)

func nfkc(s string) string { return norm.NFKC.String(s) }

func itoa(n int) string { return strconv.Itoa(n) }

func newTLSConfig() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }
