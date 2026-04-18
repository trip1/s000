package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownDrainsInFlightRequest(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	allowFinish := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-allowFinish
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	srv := NewHTTPServer(ln.Addr().String(), h)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	respCh := make(chan *http.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, reqErr := http.Get("http://" + ln.Addr().String())
		if reqErr != nil {
			errCh <- reqErr
			return
		}
		respCh <- resp
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- srv.Shutdown(ctx)
	}()

	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown should wait for in-flight request, got early result: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(allowFinish)

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("shutdown failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not complete")
	}

	select {
	case err := <-errCh:
		t.Fatalf("request failed: %v", err)
	case resp := <-respCh:
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected status 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != "ok" {
			t.Fatalf("expected response body ok, got %q", string(body))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request did not complete")
	}

	if err := <-serveErr; err != nil && err != http.ErrServerClosed {
		t.Fatalf("server exited with unexpected error: %v", err)
	}
}
