package server

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type authFailureProtector interface {
	IsBlocked(identity string, now time.Time) bool
	RecordFailure(identity string, now time.Time)
	RecordSuccess(identity string)
}

type authFailureState struct {
	windowStart  time.Time
	failures     int
	blockedUntil time.Time
}

type inMemoryAuthFailureProtector struct {
	threshold int
	window    time.Duration
	blockFor  time.Duration
	mu        sync.Mutex
	state     map[string]authFailureState
}

func newAuthFailureProtector(threshold int, window time.Duration, blockFor time.Duration, _ func() time.Time) *inMemoryAuthFailureProtector {
	if threshold <= 0 {
		threshold = 20
	}
	if window <= 0 {
		window = time.Minute
	}
	if blockFor <= 0 {
		blockFor = 5 * time.Minute
	}
	return &inMemoryAuthFailureProtector{threshold: threshold, window: window, blockFor: blockFor, state: map[string]authFailureState{}}
}

func (p *inMemoryAuthFailureProtector) IsBlocked(identity string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.state[identity]
	if !ok {
		return false
	}
	if now.After(st.blockedUntil) {
		st.blockedUntil = time.Time{}
		p.state[identity] = st
		return false
	}
	return !st.blockedUntil.IsZero()
}

func (p *inMemoryAuthFailureProtector) RecordFailure(identity string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.state[identity]
	if st.windowStart.IsZero() || now.Sub(st.windowStart) > p.window {
		st.windowStart = now
		st.failures = 0
	}
	st.failures++
	if st.failures > p.threshold {
		st.blockedUntil = now.Add(p.blockFor)
	}
	p.state[identity] = st
}

func (p *inMemoryAuthFailureProtector) RecordSuccess(identity string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.state, identity)
}

// AuditEvent captures one security-relevant action.
type AuditEvent struct {
	At        time.Time
	Action    string
	Outcome   string
	RequestID string
	Principal string
	Bucket    string
	Key       string
	Reason    string
}

// AuditSink receives audit events.
type AuditSink interface {
	Emit(event AuditEvent)
}

type slogAuditSink struct{}

func (slogAuditSink) Emit(event AuditEvent) {
	slog.Info("audit event",
		"at", event.At.Format(time.RFC3339),
		"action", event.Action,
		"outcome", event.Outcome,
		"request_id", event.RequestID,
		"principal", redactSensitiveString(event.Principal),
		"bucket", event.Bucket,
		"key", event.Key,
		"reason", redactSensitiveString(event.Reason),
	)
}

func emitAuditEvent(opts Options, event AuditEvent) {
	if !opts.AuditEnabled {
		return
	}
	sink := opts.Audit
	if sink == nil {
		sink = slogAuditSink{}
	}
	if event.At.IsZero() {
		event.At = time.Now().UTC()
	}
	sink.Emit(event)
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(signature=)([^,\s]+)`),
	regexp.MustCompile(`(?i)(secret(_key)?=)([^,\s]+)`),
	regexp.MustCompile(`(?i)(authorization:)([^\n]+)`),
	regexp.MustCompile(`(?i)(x-amz-security-token=)([^&\s]+)`),
}

func redactSensitiveString(in string) string {
	out := in
	for _, re := range secretPatterns {
		out = re.ReplaceAllString(out, `${1}[REDACTED]`)
	}
	return out
}

func redactPanicValue(v any) string {
	return redactSensitiveString(fmt.Sprint(v))
}

func clientIdentityKey(r *http.Request) string {
	principal := extractPrincipalID(r)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.TrimSpace(host)
	if principal == "" {
		principal = "anon"
	}
	if host == "" {
		host = "unknown"
	}
	return principal + "@" + host
}
