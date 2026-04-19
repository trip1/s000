package observability

import (
	"context"
	"strings"
)

// TraceHooks allows optional tracing integration behind feature flags.
type TraceHooks interface {
	Start(ctx context.Context, name string) (context.Context, func())
}

type noopTraceHooks struct{}

// Start begins a no-op span.
func (noopTraceHooks) Start(ctx context.Context, _ string) (context.Context, func()) {
	return ctx, func() {}
}

// NoopTraceHooks returns a no-op tracing implementation.
func NoopTraceHooks() TraceHooks {
	return noopTraceHooks{}
}

type traceContextKey struct{}

type TraceContext struct {
	TraceID     string `json:"trace_id,omitempty"`
	SpanID      string `json:"span_id,omitempty"`
	TraceParent string `json:"traceparent,omitempty"`
}

func WithTraceContext(ctx context.Context, trace TraceContext) context.Context {
	trace.TraceID = strings.ToLower(strings.TrimSpace(trace.TraceID))
	trace.SpanID = strings.ToLower(strings.TrimSpace(trace.SpanID))
	trace.TraceParent = strings.ToLower(strings.TrimSpace(trace.TraceParent))
	if trace.TraceID == "" && trace.SpanID == "" && trace.TraceParent == "" {
		return ctx
	}
	return context.WithValue(ctx, traceContextKey{}, trace)
}

func TraceContextFromContext(ctx context.Context) TraceContext {
	if ctx == nil {
		return TraceContext{}
	}
	trace, _ := ctx.Value(traceContextKey{}).(TraceContext)
	return trace
}

func ParseTraceparentHeader(header string) (TraceContext, bool) {
	v := strings.ToLower(strings.TrimSpace(header))
	parts := strings.Split(v, "-")
	if len(parts) != 4 {
		return TraceContext{}, false
	}
	version, traceID, spanID, flags := parts[0], parts[1], parts[2], parts[3]
	if !isHex(version, 2) || !isHex(traceID, 32) || !isHex(spanID, 16) || !isHex(flags, 2) {
		return TraceContext{}, false
	}
	if traceID == "00000000000000000000000000000000" || spanID == "0000000000000000" {
		return TraceContext{}, false
	}
	return TraceContext{TraceID: traceID, SpanID: spanID, TraceParent: strings.Join(parts, "-")}, true
}

func isHex(v string, length int) bool {
	if len(v) != length {
		return false
	}
	for _, ch := range v {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}
