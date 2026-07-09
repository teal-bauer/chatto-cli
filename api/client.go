// Package api provides a ConnectRPC + realtime client for the Chatto API.
//
// It replaces the removed GraphQL API (POST /api/graphql, a
// graphql-transport-ws subscription, session-cookie auth) with ConnectRPC
// (POST {instance}/api/connect/...) plus a protobuf WebSocket
// ({instance}/api/realtime) and opaque bearer tokens. See client.go for
// transport/auth, rpc.go for the typed RPC surface, watch.go for the
// realtime stream, and hydrate.go for turning realtime invalidation signals
// into renderable data.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"

	apiv1connect "github.com/teal-bauer/chatto-cli/internal/pb/chatto/api/v1/apiv1connect"
)

// Client is an authenticated ConnectRPC client for the Chatto API, plus the
// state (instance URL, auth) needed to open the realtime WebSocket.
type Client struct {
	instance   string
	connectURL string
	wsURL      string
	token      string
	session    string
	http       *http.Client

	viewer        apiv1connect.ViewerServiceClient
	roomDirectory apiv1connect.RoomDirectoryServiceClient
	room          apiv1connect.RoomServiceClient
	message       apiv1connect.MessageServiceClient
	user          apiv1connect.UserServiceClient
	account       apiv1connect.MyAccountServiceClient
}

// New creates a new authenticated Client for instance. Calls prefer bearer
// token auth (Authorization: Bearer <token>) and fall back to the
// chatto_session cookie when token is empty.
func New(instance, token, session string) *Client {
	instance = strings.TrimRight(instance, "/")
	c := &Client{
		instance:   instance,
		connectURL: instance + "/api/connect",
		wsURL:      toWSURL(instance) + "/api/realtime",
		token:      token,
		session:    session,
		http:       &http.Client{Timeout: 30 * time.Second},
	}

	opt := connect.WithInterceptors(authInterceptor(token, session))
	c.viewer = apiv1connect.NewViewerServiceClient(c.http, c.connectURL, opt)
	c.roomDirectory = apiv1connect.NewRoomDirectoryServiceClient(c.http, c.connectURL, opt)
	c.room = apiv1connect.NewRoomServiceClient(c.http, c.connectURL, opt)
	c.message = apiv1connect.NewMessageServiceClient(c.http, c.connectURL, opt)
	c.user = apiv1connect.NewUserServiceClient(c.http, c.connectURL, opt)
	c.account = apiv1connect.NewMyAccountServiceClient(c.http, c.connectURL, opt)
	return c
}

// Instance returns the base instance URL (no trailing slash).
func (c *Client) Instance() string { return c.instance }

// Token returns the bearer token used for auth (may be empty if the client
// relies on the session cookie only).
func (c *Client) Token() string { return c.token }

func toWSURL(instance string) string {
	switch {
	case strings.HasPrefix(instance, "https://"):
		return "wss://" + instance[len("https://"):]
	case strings.HasPrefix(instance, "http://"):
		return "ws://" + instance[len("http://"):]
	default:
		return "wss://" + instance
	}
}

// authInterceptor attaches bearer or cookie auth to every outgoing Connect
// call. Token takes precedence; the session cookie is a fallback for
// deployments/flows that haven't picked up a bearer token yet.
func authInterceptor(token, session string) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			switch {
			case token != "":
				req.Header().Set("Authorization", "Bearer "+token)
			case session != "":
				req.Header().Set("Cookie", "chatto_session="+session)
			}
			return next(ctx, req)
		}
	})
}

// ChattoError is a failed Connect RPC call, preserving the original error
// code so callers can branch on it without importing connectrpc.com/connect
// directly.
type ChattoError struct {
	Code    connect.Code
	Message string
}

func (e *ChattoError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ErrUnauthenticated indicates the bearer token/session was rejected
// (connect.CodeUnauthenticated). Callers should re-login (via Login) and
// retry, rather than treat it as an unrecoverable error.
type ErrUnauthenticated struct {
	*ChattoError
}

// mapError translates a connect.Error into a *ChattoError (or
// *ErrUnauthenticated for CodeUnauthenticated). Errors that aren't
// connect.Errors (e.g. network failures before a response was received)
// pass through unchanged.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var connErr *connect.Error
	if errors.As(err, &connErr) {
		ce := &ChattoError{Code: connErr.Code(), Message: connErr.Message()}
		if connErr.Code() == connect.CodeUnauthenticated {
			return &ErrUnauthenticated{ChattoError: ce}
		}
		return ce
	}
	return err
}

// isNotFound reports whether err is a connect.Error with CodeNotFound.
func isNotFound(err error) bool {
	var connErr *connect.Error
	return errors.As(err, &connErr) && connErr.Code() == connect.CodeNotFound
}

// loginResponse is the JSON body returned by POST /auth/login.
type loginResponse struct {
	Token string `json:"token"`
}

// Login authenticates with identifier+password against instance and returns
// the bearer token plus the chatto_session cookie value (kept as fallback
// auth). The token is required; a missing session cookie is not an error.
func Login(instance, identifier, password string) (token, session string, err error) {
	instance = strings.TrimRight(instance, "/")
	body, err := json.Marshal(map[string]string{"identifier": identifier, "password": password})
	if err != nil {
		return "", "", err
	}

	req, err := http.NewRequest(http.MethodPost, instance+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 30 * time.Second,
		// Don't follow redirects -- we want the Set-Cookie from the login response.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode >= 400 {
		return "", "", fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode, string(raw))
	}

	var parsed loginResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", fmt.Errorf("decoding login response: %w", err)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == "chatto_session" {
			session = ck.Value
		}
	}
	if parsed.Token == "" {
		return "", "", fmt.Errorf("login succeeded but no token in response body")
	}
	return parsed.Token, session, nil
}
