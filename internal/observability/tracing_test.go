package observability

import "testing"

func TestParseTraceparentHeader(t *testing.T) {
	t.Parallel()

	tc, ok := ParseTraceparentHeader("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	if !ok {
		t.Fatal("expected traceparent header to parse")
	}
	if tc.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("unexpected trace id %q", tc.TraceID)
	}
	if tc.SpanID != "00f067aa0ba902b7" {
		t.Fatalf("unexpected span id %q", tc.SpanID)
	}

	if _, ok := ParseTraceparentHeader("bad-header"); ok {
		t.Fatal("expected invalid traceparent to fail parsing")
	}
}
