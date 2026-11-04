package gmail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	imageProxyVersion = 2
	partImagePrefix   = "gmail-part:"
	partImageTTL      = 24 * time.Hour
)

var signedImageRE = regexp.MustCompile(`^/?gmail-image\?token=([A-Za-z0-9_-]+)\.([0-9a-f]{32})(?:&v=\d+)?$`)

func b64urlEncode(v string) string { return base64.RawURLEncoding.EncodeToString([]byte(v)) }

func b64urlDecode(v string) (string, bool) {
	v = strings.TrimRight(v, "=")
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

func (s *Service) signature(payload string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// signImageURL returns the opaque signed page-relative URL of a remote image.
func (s *Service) signImageURL(url string) string {
	payload := b64urlEncode(url)
	return fmt.Sprintf("gmail-image?token=%s.%s&v=%d", payload, s.signature(payload), imageProxyVersion)
}

// verifyImageToken returns the signed payload, or "" for an invalid token.
func (s *Service) verifyImageToken(token string) string {
	if token == "" || len(token) > 8192 {
		return ""
	}
	payload, sig, ok := strings.Cut(token, ".")
	if !ok {
		return ""
	}
	if !hmac.Equal([]byte(sig), []byte(s.signature(payload))) {
		return ""
	}
	v, ok := b64urlDecode(payload)
	if !ok {
		return ""
	}
	return v
}

func (s *Service) signPartImageURL(folder string, uid int64, index int, now time.Time) string {
	expires := now.Add(partImageTTL).Unix()
	return s.signImageURL(fmt.Sprintf("%s%d:%d:%d:%s", partImagePrefix, expires, uid, index, folder))
}

// parsePartImage returns (folder, uid, index) of a valid gmail-part payload.
func (s *Service) parsePartImage(payload string, now time.Time) (folder string, uid int64, index int, ok bool) {
	if !strings.HasPrefix(payload, partImagePrefix) {
		return
	}
	f := strings.SplitN(payload[len(partImagePrefix):], ":", 4)
	if len(f) != 4 {
		return
	}
	digits := func(x string) bool {
		if x == "" {
			return false
		}
		for _, c := range x {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	if !digits(f[0]) || !digits(f[1]) || !digits(f[2]) {
		return
	}
	exp, _ := strconv.ParseInt(f[0], 10, 64)
	if exp < now.Unix() {
		return
	}
	fo, err := s.validateFolder(&f[3])
	if err != nil {
		return
	}
	u, _ := strconv.ParseInt(f[1], 10, 64)
	i, _ := strconv.Atoi(f[2])
	return fo, u, i, true
}

// cidImageURLs maps the lowercase Content-ID of every inline image to its
// signed proxy URL.
func (s *Service) cidImageURLs(atts []attachment, folder string, uid int64) map[string]string {
	urls := map[string]string{}
	now := time.Now()
	for _, a := range atts {
		cid := strings.ToLower(a.ContentID)
		if a.Inline && cid != "" {
			urls[cid] = s.signPartImageURL(folder, uid, a.Index, now)
		}
	}
	return urls
}
