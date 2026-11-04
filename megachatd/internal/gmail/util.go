package gmail

import (
	"crypto/tls"
	"fmt"
	"github.com/emdioh/megachat/megachatd/internal/api"
	"os"
	"strconv"
	"time"

	"golang.org/x/text/unicode/norm"
)

func nfkc(s string) string { return norm.NFKC.String(s) }

func itoa(n int) string { return strconv.Itoa(n) }

func newTLSConfig() *tls.Config { return &tls.Config{MinVersion: tls.VersionTLS12} }

type durationT = time.Duration

func blobFor(path, contentType string) api.BlobOut {
	out := api.BlobOut{Path: path, ContentType: &contentType}
	if st, err := os.Stat(path); err == nil {
		size := st.Size()
		out.Size = &size
		out.Version = api.Ptr(fmt.Sprintf("%d-%d", st.ModTime().UnixNano(), size))
	}
	return out
}
