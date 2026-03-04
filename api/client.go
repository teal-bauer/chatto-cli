// Package api provides a GraphQL client for the Chatto API.
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is an authenticated HTTP client for the Chatto GraphQL API.
type Client struct {
	instance string
	session  string
	http     *http.Client
}

// New creates a new authenticated Client.
func New(instance, session string) *Client {
	return &Client{
		instance: strings.TrimRight(instance, "/"),
		session:  session,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

// Instance returns the base URL.
func (c *Client) Instance() string { return c.instance }

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// GraphQLError is returned when the API responds with errors.
type GraphQLError struct {
	Messages []string
}

func (e *GraphQLError) Error() string {
	return "GraphQL error: " + strings.Join(e.Messages, "; ")
}

// ExecuteRaw posts a GraphQL query and returns the raw data field bytes.
func (c *Client) ExecuteRaw(query string, variables map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.instance+"/api/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/graphql-response+json, application/json")
	req.Header.Set("Cookie", "chatto_session="+c.session)
	req.Header.Set("Origin", c.instance)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var gql gqlResponse
	if err := json.Unmarshal(raw, &gql); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	if len(gql.Errors) > 0 {
		msgs := make([]string, len(gql.Errors))
		for i, e := range gql.Errors {
			msgs[i] = e.Message
		}
		return nil, &GraphQLError{Messages: msgs}
	}
	return gql.Data, nil
}

// Execute posts a GraphQL query and decodes the data field into out.
func (c *Client) Execute(query string, variables map[string]any, out any) error {
	body, err := json.Marshal(gqlRequest{Query: query, Variables: variables})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.instance+"/api/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/graphql-response+json, application/json")
	req.Header.Set("Cookie", "chatto_session="+c.session)
	req.Header.Set("Origin", c.instance)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var gql gqlResponse
	if err := json.Unmarshal(raw, &gql); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}

	if len(gql.Errors) > 0 {
		msgs := make([]string, len(gql.Errors))
		for i, e := range gql.Errors {
			msgs[i] = e.Message
		}
		return &GraphQLError{Messages: msgs}
	}

	if out != nil && gql.Data != nil {
		return json.Unmarshal(gql.Data, out)
	}
	return nil
}

// Login authenticates with email+password and returns the session cookie value.
func Login(instance, identifier, password string) (string, error) {
	instance = strings.TrimRight(instance, "/")
	body, err := json.Marshal(map[string]string{"identifier": identifier, "password": password})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", instance+"/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 30 * time.Second,
		// Don't follow redirects — we want the Set-Cookie from the login response
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login failed (HTTP %d): %s", resp.StatusCode, string(raw))
	}

	for _, c := range resp.Cookies() {
		if c.Name == "chatto_session" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("login succeeded but no chatto_session cookie returned")
}
