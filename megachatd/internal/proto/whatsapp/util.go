package whatsapp

import (
	"net/url"
	"os"
	"strconv"
	"strings"
)

func urlUnescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// pyBase is PurePath(p).name.
func pyBase(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		p = p[i+1:]
	}
	if p == "." || p == ".." {
		return ""
	}
	return p
}

// pySuffix is PurePath(name).suffix.
func pySuffix(name string) string {
	base := pyBase(name)
	i := strings.LastIndex(base, ".")
	if i <= 0 || i == len(base)-1 {
		return ""
	}
	return base[i:]
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func nowMs() int64 { return nowFunc().UnixMilli() }

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func strOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// norm is _norm: whitespace-collapsed text.
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func getStr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case string:
		return t != ""
	}
	return false
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case float64:
		return int64(t), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// timestampMs is _timestamp_ms: WAHA seconds to the cache's milliseconds.
func timestampMs(v int64) int64 {
	if v > 0 && v < 1_000_000_000_000 {
		return v * 1000
	}
	return v
}
