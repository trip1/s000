package server

import "context"

type requestIDKey struct{}
type principalKey struct{}

// withRequestIDContext attaches a request ID to request context.
func withRequestIDContext(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestIDFromContext returns the request ID bound to request context.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

func withPrincipalContext(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// PrincipalFromContext returns the authenticated principal for one request.
func PrincipalFromContext(ctx context.Context) string {
	v, _ := ctx.Value(principalKey{}).(string)
	return v
}
