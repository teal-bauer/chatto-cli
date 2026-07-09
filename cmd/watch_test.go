package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/teal-bauer/chatto-cli/api"
)

// TestWatchTerminalErr covers BLOCKER 1's exit-code fix: runWatch must return
// (rather than log-and-continue) terminal errors delivered on Client.Watch's
// channel, so `chatto watch` exits non-zero when the server rejects auth or
// tells the client to stop. The mapping runWatch uses for this lives in
// watchTerminalErr so it can be tested directly, without a live server.
func TestWatchTerminalErr(t *testing.T) {
	t.Run("unauthenticated is terminal and hints at login", func(t *testing.T) {
		src := &api.ErrUnauthenticated{ChattoError: &api.ChattoError{Code: connect.CodeUnauthenticated, Message: "bad token"}}
		got := watchTerminalErr(src)
		if got == nil {
			t.Fatal("watchTerminalErr() = nil, want non-nil terminal error")
		}
		if !errors.As(got, new(*api.ErrUnauthenticated)) {
			t.Errorf("watchTerminalErr() = %v, want wrapping *api.ErrUnauthenticated", got)
		}
		if !strings.Contains(got.Error(), "chatto login") {
			t.Errorf("watchTerminalErr() = %q, want it to mention `chatto login`", got.Error())
		}
	})

	t.Run("realtime stopped is terminal", func(t *testing.T) {
		src := &api.ErrRealtimeStopped{Code: "server_shutdown", Message: "bye"}
		got := watchTerminalErr(src)
		if got == nil {
			t.Fatal("watchTerminalErr() = nil, want non-nil terminal error")
		}
		if !errors.Is(got, src) {
			t.Errorf("watchTerminalErr() = %v, want it to be (or wrap) %v", got, src)
		}
	})

	t.Run("other errors are not terminal", func(t *testing.T) {
		src := fmt.Errorf("some transient hydration failure")
		if got := watchTerminalErr(src); got != nil {
			t.Errorf("watchTerminalErr(%v) = %v, want nil", src, got)
		}
	})
}
