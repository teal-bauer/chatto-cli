package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WatchEvent is delivered to callers of Watch.
type WatchEvent struct {
	SpaceEvent SpaceEvent
	Err        error
}

// Watch subscribes to live space events and sends them to the returned channel.
// The channel is closed when ctx is cancelled or a fatal error occurs.
func (c *Client) Watch(ctx context.Context, spaceID string) (<-chan WatchEvent, error) {
	wsURL := toWSURL(c.instance) + "/api/graphql"
	ch := make(chan WatchEvent, 32)
	go func() {
		defer close(ch)
		backoff := time.Second
		for {
			err := c.runSubscription(ctx, wsURL, spaceID, ch)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "warn: %v; reconnecting in %s\n", err, backoff)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
		}
	}()
	return ch, nil
}

func (c *Client) runSubscription(ctx context.Context, wsURL, spaceID string, ch chan<- WatchEvent) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 15 * time.Second,
		Subprotocols:     []string{"graphql-transport-ws"},
	}
	headers := http.Header{
		"Cookie": []string{"chatto_session=" + c.session},
		"Origin": []string{c.instance},
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
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
	writeJSON := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(v)
	}

	// connection_init
	if err := writeJSON(map[string]any{"type": "connection_init"}); err != nil {
		return err
	}
	// wait for connection_ack (skipping any pings the server may send first)
	if err := waitForAck(conn); err != nil {
		return err
	}

	// Send application-level keepalive pings to prevent idle connection drops.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-connCtx.Done():
				return
			case <-ticker.C:
				if err := writeJSON(map[string]any{"type": "ping"}); err != nil {
					return
				}
			}
		}
	}()

	// subscribe
	subQuery := `
		subscription SpaceEvents($spaceId: ID!) {
			mySpaceEvents(spaceId: $spaceId) {` + spaceEventFields + `
			}
		}`
	if err := writeJSON(map[string]any{
		"id":   "1",
		"type": "subscribe",
		"payload": map[string]any{
			"query":     subQuery,
			"variables": map[string]any{"spaceId": spaceID},
		},
	}); err != nil {
		return err
	}

	// read messages
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var msg struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "ping":
			_ = writeJSON(map[string]any{"type": "pong"})
		case "next":
			var payload struct {
				Data struct {
					MySpaceEvents SpaceEvent `json:"mySpaceEvents"`
				} `json:"data"`
			}
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				continue
			}
			// Capture the raw event JSON for debug output.
			var rawData struct {
				Data map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(msg.Payload, &rawData); err == nil {
				payload.Data.MySpaceEvents.RawJSON = rawData.Data["mySpaceEvents"]
			}
			select {
			case ch <- WatchEvent{SpaceEvent: payload.Data.MySpaceEvents}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case "error":
			return fmt.Errorf("subscription error: %s", string(msg.Payload))
		case "complete":
			return fmt.Errorf("subscription completed by server")
		}
	}
}

func waitForAck(conn *websocket.Conn) error {
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			return err
		}
		switch msg.Type {
		case "connection_ack":
			return nil
		case "ping":
			// Server may send ping before ack; skip it (no pong needed during handshake).
			continue
		default:
			return fmt.Errorf("expected connection_ack, got %q", msg.Type)
		}
	}
}

func toWSURL(instance string) string {
	instance = strings.TrimRight(instance, "/")
	if strings.HasPrefix(instance, "https://") {
		return "wss://" + instance[8:]
	}
	if strings.HasPrefix(instance, "http://") {
		return "ws://" + instance[7:]
	}
	return "wss://" + instance
}
