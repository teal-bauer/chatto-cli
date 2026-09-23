package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	realtimev1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/realtime/v1"
)

// realtimeProtocolVersion is the only protocol version this client speaks.
// The server rejects any other value.
const realtimeProtocolVersion = 4

// handshakeTimeout bounds how long Watch waits for the first frame after
// subscribing (the server sends caught_up once recovery reaches live).
const handshakeTimeout = 12 * time.Second

// defaultHeartbeatInterval assumes the server's heartbeat cadence for stall
// detection; protocol v4 heartbeats carry no interval. The server currently
// heartbeats every 15s, so this over-estimates generously.
const defaultHeartbeatInterval = 30 * time.Second

// stallMultiplier: no frames at all for this many heartbeat intervals means
// the connection is considered dead.
const stallMultiplier = 3

// healthyConnectionThreshold: a connection that stays up at least this long
// before failing resets the reconnect backoff to its 1s base, rather than
// carrying forward whatever backoff an earlier run of failures grew to.
const healthyConnectionThreshold = 30 * time.Second

// ErrRealtimeStopped indicates the server does not want the client to
// reconnect: a RealtimeClose with reconnect=false whose code is not a
// recognized auth failure (those become *ErrUnauthenticated instead).
// Watch stops (closes its channel) rather than retrying when this occurs;
// callers may start a new Watch to try again.
type ErrRealtimeStopped struct {
	Code    string
	Message string
}

func (e *ErrRealtimeStopped) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// retryError is an internal signal: tear down this connection and reconnect,
// optionally overriding the exponential backoff for exactly one attempt
// (RealtimeClose.retry_after).
type retryError struct {
	delay time.Duration
}

func (e *retryError) Error() string { return "realtime connection closed; retrying" }

// WatchEvent is delivered to callers of Client.Watch. Exactly one of Event
// or Err is set per delivery; a terminal Err (see ErrRealtimeStopped and
// ErrUnauthenticated) is always the last value sent before the channel
// closes.
type WatchEvent struct {
	Event *HydratedEvent
	Err   error
}

// Watch subscribes to the realtime event stream and sends hydrated events to
// the returned channel. The channel is closed when ctx is cancelled, or
// after a terminal error (ErrRealtimeStopped / ErrUnauthenticated) is
// delivered. Non-terminal failures reconnect with exponential backoff
// (starting at 1s, doubling, capped at 60s), honoring a server-requested
// retry_after_ms for the very next attempt.
func (c *Client) Watch(ctx context.Context) (<-chan WatchEvent, error) {
	ch := make(chan WatchEvent, 32)
	hydrator := NewHydrator(c)

	go func() {
		defer close(ch)
		backoff := time.Second
		for {
			connectedAt := time.Now()
			err := c.runRealtimeConnection(ctx, hydrator, ch)
			if ctx.Err() != nil {
				return
			}

			var stopped *ErrRealtimeStopped
			var unauth *ErrUnauthenticated
			if errors.As(err, &stopped) || errors.As(err, &unauth) {
				sendWatchEvent(ctx, ch, WatchEvent{Err: err})
				return
			}

			// A connection that stayed up for a while counts as healthy:
			// reset backoff so a later disconnect reconnects fast instead of
			// inheriting a long wait from an earlier outage.
			if time.Since(connectedAt) >= healthyConnectionThreshold {
				backoff = time.Second
			}

			delay := backoff
			var retry *retryError
			isRetry := errors.As(err, &retry)
			if isRetry && retry.delay > 0 {
				delay = retry.delay
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: %v; reconnecting in %s\n", err, delay)
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			if !isRetry && backoff < 60*time.Second {
				backoff *= 2
			}
		}
	}()
	return ch, nil
}

func sendWatchEvent(ctx context.Context, ch chan<- WatchEvent, ev WatchEvent) {
	select {
	case ch <- ev:
	case <-ctx.Done():
	}
}

// runRealtimeConnection runs a single WebSocket connection: one subscribe
// message, then the frame loop, until it errors or ctx is cancelled.
func (c *Client) runRealtimeConnection(ctx context.Context, hydrator *Hydrator, ch chan<- WatchEvent) error {
	// No Sec-WebSocket-Protocol offered: this is plain binary protobuf
	// frames over a bare WebSocket, not a subprotocol.
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	header := http.Header{}
	if c.token == "" && c.session != "" {
		// Mirrors authInterceptor's cookie fallback for Connect calls: the
		// subscribe frame only carries a bearer token, so session-only profiles
		// need the cookie on the upgrade request itself. The server accepts
		// this via the same authctx cookie auth it uses for Connect calls.
		header.Set("Cookie", "chatto_session="+c.session)
	}
	conn, _, err := dialer.DialContext(ctx, c.wsURL, header)
	if err != nil {
		return err
	}

	// Use a per-connection context so deferred cleanup cancels the ping goroutine.
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	// Close the connection exactly once, whether from ctx cancellation or normal return.
	var closeOnce sync.Once
	closeConn := func() { closeOnce.Do(func() { conn.Close() }) }
	defer closeConn()
	go func() {
		<-connCtx.Done()
		closeConn()
	}()

	// gorilla/websocket requires serialized writes; guard all writes with this mutex.
	var writeMu sync.Mutex

	// subscribe: the only application message this client ever sends.
	subscribe := &realtimev1.RealtimeSubscribe{
		ProtocolVersion: realtimeProtocolVersion,
		InitialState:    realtimev1.RealtimeInitialState_REALTIME_INITIAL_STATE_LIVE_ONLY,
	}
	if c.token != "" {
		token := c.token
		subscribe.BearerToken = &token
	}
	data, err := proto.Marshal(subscribe)
	if err != nil {
		return err
	}
	writeMu.Lock()
	err = conn.WriteMessage(websocket.BinaryMessage, data)
	writeMu.Unlock()
	if err != nil {
		return err
	}

	// No client-side keepalive: protocol v4 forbids further client messages,
	// and the server heartbeats on its own cadence.
	stallTimeout := defaultHeartbeatInterval * stallMultiplier

	// The first frame after subscribe is due within the handshake window;
	// once caught_up arrives the looser stall deadline applies.
	firstFrame := true
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if firstFrame {
			conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
		} else {
			conn.SetReadDeadline(time.Now().Add(stallTimeout))
		}
		frame, err := recvFrame(conn)
		if err != nil {
			return err
		}

		switch f := frame.GetFrame().(type) {
		case *realtimev1.RealtimeServerFrame_CaughtUp:
			firstFrame = false
		case *realtimev1.RealtimeServerFrame_Event:
			firstFrame = false
			hydrated, herr := hydrator.Hydrate(ctx, f.Event)
			if herr != nil {
				sendWatchEvent(ctx, ch, WatchEvent{Err: herr})
				continue
			}
			if hydrated == nil {
				continue // dropped, e.g. retracted between signal and fetch
			}
			sendWatchEvent(ctx, ch, WatchEvent{Event: hydrated})
		case *realtimev1.RealtimeServerFrame_Heartbeat:
			// Liveness only; never surfaced to callers.
		case *realtimev1.RealtimeServerFrame_Snapshot:
			// We subscribe live-only, so no snapshot is expected; discard if one arrives.
		case *realtimev1.RealtimeServerFrame_Close:
			return closeFromFrame(f.Close)
		default:
			// Unknown/empty frame kind; ignore and keep reading.
		}
	}
}

// closeFromFrame maps a server close frame to a Watch error: auth-terminal
// codes become *ErrUnauthenticated regardless of the reconnect flag (the
// credential is dead, so reconnecting would only fail the handshake again),
// reconnect=false becomes *ErrRealtimeStopped, and everything else retries
// after the server's suggested delay when present.
func closeFromFrame(cl *realtimev1.RealtimeClose) error {
	switch cl.GetCode() {
	case realtimev1.RealtimeCloseCode_REALTIME_CLOSE_CODE_AUTHENTICATION_REQUIRED,
		realtimev1.RealtimeCloseCode_REALTIME_CLOSE_CODE_SESSION_TERMINATED:
		return &ErrUnauthenticated{ChattoError: &ChattoError{Code: connect.CodeUnauthenticated, Message: cl.GetMessage()}}
	}
	if !cl.GetReconnect() {
		return &ErrRealtimeStopped{Code: closeCodeString(cl.GetCode()), Message: cl.GetMessage()}
	}
	return &retryError{delay: cl.GetRetryAfter().AsDuration()}
}

// closeCodeString renders a RealtimeCloseCode as its snake_case name without
// the enum-name prefix (REALTIME_CLOSE_CODE_INVALID_REQUEST ->
// invalid_request), matching the wire-level close codes older releases sent.
func closeCodeString(code realtimev1.RealtimeCloseCode) string {
	return strings.ToLower(strings.TrimPrefix(code.String(), "REALTIME_CLOSE_CODE_"))
}

func recvFrame(conn *websocket.Conn) (*realtimev1.RealtimeServerFrame, error) {
	msgType, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if msgType != websocket.BinaryMessage {
		return nil, fmt.Errorf("realtime protocol violation: received non-binary frame (type %d)", msgType)
	}
	var frame realtimev1.RealtimeServerFrame
	if err := proto.Unmarshal(raw, &frame); err != nil {
		return nil, fmt.Errorf("decoding realtime frame: %w", err)
	}
	return &frame, nil
}
