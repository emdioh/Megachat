package core

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

// Port of server/retry.py: pure send-error classification and delay.

const (
	SendRetryMaxAttempts = 3
	SendRetryBaseDelay   = 1.0 // seconds
	SendRetryJitterMax   = 1.0
)

// Send error classes.
const (
	Retryable = "retryable"
	Terminal  = "terminal"
)

var signalRetryable = regexp.MustCompile(`(?i)connection refused|name or service not known|temporary failure in name resolution`)
var statusRe = regexp.MustCompile(`status=(\d+)`)

// HTTPStatusError can be returned (or wrapped) by backends so that
// ClassifySendError knows the remote HTTP status (WAHA / REST failures).
type HTTPStatusError struct {
	Code int
	Msg  string
}

func (e *HTTPStatusError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return "status=" + strconv.Itoa(e.Code)
}

// FloodWaitError is the Telegram flood-wait error (retryable under 5 s).
type FloodWaitError struct{ Seconds int }

func (e *FloodWaitError) Error() string { return "FloodWait " + strconv.Itoa(e.Seconds) + "s" }

// ClassifySendError never panics: anything unexpected degrades to terminal.
func ClassifySendError(protocol string, err error) (class string) {
	defer func() {
		if recover() != nil {
			class = Terminal
		}
	}()
	if err == nil {
		return Terminal
	}
	switch protocol {
	case api.ProtocolSignal, api.ProtocolTelegram, api.ProtocolWhatsApp:
	default:
		return Terminal
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Terminal
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return Terminal
	}
	var flood *FloodWaitError
	if protocol == api.ProtocolTelegram && errors.As(err, &flood) {
		if flood.Seconds < 5 {
			return Retryable
		}
		return Terminal
	}
	var hs *HTTPStatusError
	if errors.As(err, &hs) {
		if hs.Code == 0 {
			return classifyText(err.Error())
		}
		if hs.Code == 429 || (hs.Code >= 500 && hs.Code < 600) {
			return Retryable
		}
		return Terminal
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENETUNREACH) || errors.Is(err, syscall.EHOSTUNREACH) {
		return Retryable
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && (dns.IsTemporary || dns.IsNotFound) {
		return Retryable
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not configured") {
		return Terminal
	}
	if m := statusRe.FindStringSubmatch(msg); m != nil && protocol == api.ProtocolWhatsApp {
		code, _ := strconv.Atoi(m[1])
		if code == 0 {
			return classifyText(msg)
		}
		if code == 429 || (code >= 500 && code < 600) {
			return Retryable
		}
		return Terminal
	}
	if protocol == api.ProtocolSignal || protocol == api.ProtocolWhatsApp {
		return classifyText(msg)
	}
	return Terminal
}

func classifyText(msg string) string {
	if signalRetryable.MatchString(msg) {
		return Retryable
	}
	return Terminal
}

// SafeErrorText stringifies err, cut to limit characters.
func SafeErrorText(err error, limit int) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if r := []rune(s); len(r) > limit {
		return string(r[:limit])
	}
	return s
}

// ComputeDelay is the retry delay: 1.0*2^(attempt-1) + U(0,1) seconds.
func ComputeDelay(attempt int, rng *rand.Rand) time.Duration {
	f := rand.Float64
	if rng != nil {
		f = rng.Float64
	}
	base := SendRetryBaseDelay * float64(int(1)<<(attempt-1))
	return time.Duration((base + f()*SendRetryJitterMax) * float64(time.Second))
}
