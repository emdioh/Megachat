package gmail

import "golang.org/x/text/unicode/norm"

func nfkc(s string) string { return norm.NFKC.String(s) }
