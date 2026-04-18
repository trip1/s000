package observability

import "context"

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
