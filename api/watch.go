package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"

	realtimev1 "github.com/teal-bauer/chatto-cli/internal/pb/chatto/realtime/v1"
)

// realtimeProtocolVersion is the only protocol version this client speaks.
// The server accepts 0 (unspecified) or this exact value.
const realtimeProtocolVersion = 1

// authErrorCodes are RealtimeError.code values the server sends during the
// hello/subscribe handshake that indicate a bad or revoked credential, as
// opposed to a protocol-level problem.
var authErrorCodes = map[string]bool{
	"authentication_required": true,
}

// handshakeTimeout bounds how long Watch waits for each handshake reply.
const handshakeTimeout = 12 * time.Second

// defaultHeartbeatInterval is used if the server's hello reports 0.
const defaultHeartbeatInterval = 30 * time.Second

// stallMultiplier: no frames at all for this many heartbeat intervals means
// the connection is considered dead.
const stallMultiplier = 3

// healthyConnectionThreshold: a connection that stays up at least this long
// before failing resets the reconnect backoff to its 1s base, rather than
// carrying forward whatever backoff an earlier run of failures grew to.
const healthyConnectionThreshold = 30 * time.Second

// ErrRealtimeStopped indicates the server does not want the client to
// reconnect: a RealtimeClose with reconnect=false, or a fatal RealtimeError
// whose code isn't a recognized auth failure (those become
// *ErrUnauthenticated instead). Watch stops (closes its channel) rather than
// retrying when this occurs; callers may start a new Watch to try again.
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
// (RealtimeClose.retry_after_ms).
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

// runRealtimeConnection runs a single WebSocket connection through the
// hello/subscribe handshake and the event loop, until it errors or ctx is
// cancelled.
func (c *Client) runRealtimeConnection(ctx context.Context, hydrator *Hydrator, ch chan<- WatchEvent) error {
	// No Sec-WebSocket-Protocol offered: this is plain binary protobuf
	// frames over a bare WebSocket, not a subprotocol.
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	header := http.Header{}
	if c.token == "" && c.session != "" {
		// Mirrors authInterceptor's cookie fallback for Connect calls: the
		// hello frame only carries a bearer token, so session-only profiles
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
	writeFrame := func(frame *realtimev1.RealtimeClientFrame) error {
		data, err := proto.Marshal(frame)
		if err != nil {
			return err
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.BinaryMessage, data)
	}

	// hello
	hello := &realtimev1.RealtimeClientHello{ProtocolVersion: realtimeProtocolVersion}
	if c.token != "" {
		token := c.token
		hello.BearerToken = &token
	}
	if err := writeFrame(&realtimev1.RealtimeClientFrame{
		Frame: &realtimev1.RealtimeClientFrame_Hello{Hello: hello},
	}); err != nil {
		return err
	}

	conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	helloReply, err := recvFrame(conn)
	if err != nil {
		return err
	}
	serverHello := helloReply.GetHello()
	if serverHello == nil {
		if terminal := terminalFrameError(helloReply); terminal != nil {
			return terminal
		}
		return fmt.Errorf("realtime handshake: expected hello, got %T", helloReply.GetFrame())
	}

	// subscribe
	if err := writeFrame(&realtimev1.RealtimeClientFrame{
		Frame: &realtimev1.RealtimeClientFrame_SubscribeEvents{SubscribeEvents: &realtimev1.RealtimeSubscribeEvents{}},
	}); err != nil {
		return err
	}
	conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	subReply, err := recvFrame(conn)
	if err != nil {
		return err
	}
	if subReply.GetSubscribed() == nil {
		if terminal := terminalFrameError(subReply); terminal != nil {
			return terminal
		}
		return fmt.Errorf("realtime handshake: expected subscribed, got %T", subReply.GetFrame())
	}

	heartbeatInterval := time.Duration(serverHello.GetHeartbeatIntervalSeconds()) * time.Second
	if heartbeatInterval <= 0 {
		heartbeatInterval = defaultHeartbeatInterval
	}
	stallTimeout := heartbeatInterval * stallMultiplier

	// realtime.proto only defines a client-initiated ping (server replies
	// with pong); there is no server-to-client ping to reply to. Send our
	// own keepalive ping on the heartbeat cadence; pong and the server's own
	// periodic heartbeat frame both count as liveness below.
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-ticker.C:
				_ = writeFrame(&realtimev1.RealtimeClientFrame{
					Frame: &realtimev1.RealtimeClientFrame_Ping{Ping: &realtimev1.RealtimePing{}},
				})
			}
		}
	}()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn.SetReadDeadline(time.Now().Add(stallTimeout))
		frame, err := recvFrame(conn)
		if err != nil {
			return err
		}

		switch f := frame.GetFrame().(type) {
		case *realtimev1.RealtimeServerFrame_Event:
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
		case *realtimev1.RealtimeServerFrame_Pong:
			// Liveness only; reply to our own keepalive ping.
		case *realtimev1.RealtimeServerFrame_Error:
			return errorFromFrame(f.Error)
		case *realtimev1.RealtimeServerFrame_Close:
			return closeFromFrame(f.Close)
		default:
			// Unknown/empty frame kind; ignore and keep reading.
		}
	}
}

// terminalFrameError checks a handshake reply for an error/close frame,
// returning the mapped error, or nil if frame is neither.
func terminalFrameError(frame *realtimev1.RealtimeServerFrame) error {
	if e := frame.GetError(); e != nil {
		return errorFromFrame(e)
	}
	if cl := frame.GetClose(); cl != nil {
		return closeFromFrame(cl)
	}
	return nil
}

func errorFromFrame(e *realtimev1.RealtimeError) error {
	if authErrorCodes[e.GetCode()] {
		return &ErrUnauthenticated{ChattoError: &ChattoError{Code: connect.CodeUnauthenticated, Message: e.GetMessage()}}
	}
	if e.GetFatal() {
		return &ErrRealtimeStopped{Code: e.GetCode(), Message: e.GetMessage()}
	}
	// No currently-emitted server error is non-fatal, but the proto allows
	// it in principle; treat it as transient and reconnect.
	return &retryError{}
}

func closeFromFrame(cl *realtimev1.RealtimeClose) error {
	if !cl.GetReconnect() {
		return &ErrRealtimeStopped{Code: cl.GetCode(), Message: cl.GetMessage()}
	}
	var delay time.Duration
	if cl.GetRetryAfterMs() > 0 {
		delay = time.Duration(cl.GetRetryAfterMs()) * time.Millisecond
	}
	return &retryError{delay: delay}
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
