// Package remotehttp provides an HTTP implementation of the remote runtime client.
package remotehttp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jonwraymond/toolexec/runtime/backend/remote"
)

// Config configures the remote HTTP client.
type Config struct {
	// Endpoint is the URL of the remote runtime service.
	Endpoint string

	// AuthToken is the bearer token used for authentication and signing.
	AuthToken string

	// TLSSkipVerify skips TLS certificate verification.
	// WARNING: Only use for development.
	TLSSkipVerify bool

	// MaxRetries is the maximum number of retries on transient failures.
	// Default: 3
	MaxRetries int

	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client

	// Timeout sets request timeout if HTTPClient is not provided.
	Timeout time.Duration

	// Logger is an optional logger for client events.
	Logger remote.Logger
}

// Client executes remote runtime requests over HTTP.
type Client struct {
	endpoint   *url.URL
	authToken  string
	maxRetries int
	httpClient *http.Client
	logger     remote.Logger
}

// NewClient creates a new remote HTTP client using the provided configuration.
func NewClient(cfg Config) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, remote.ErrRemoteNotAvailable
	}
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}

	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.TLSSkipVerify {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicitly opt-in for local/test endpoints
		}
		client = &http.Client{
			Transport: transport,
			Timeout:   timeout,
		}
	}

	return &Client{
		endpoint:   parsed,
		authToken:  cfg.AuthToken,
		maxRetries: maxRetries,
		httpClient: client,
		logger:     cfg.Logger,
	}, nil
}

// Endpoint returns the configured endpoint URL.
func (c *Client) Endpoint() string {
	if c.endpoint == nil {
		return ""
	}
	return c.endpoint.String()
}

// Execute runs the request against the remote runtime service.
func (c *Client) Execute(ctx context.Context, payload remote.RemoteRequest) (remote.RemoteResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return remote.RemoteResponse{}, fmt.Errorf("%w: marshal request: %v", remote.ErrRemoteExecutionFailed, err)
	}
	response, err := c.doRequest(ctx, data, payload.Stream)
	if err != nil {
		return remote.RemoteResponse{}, err
	}
	return response, nil
}

var _ remote.RemoteClient = (*Client)(nil)
var _ remote.EndpointProvider = (*Client)(nil)

func (c *Client) doRequest(ctx context.Context, payload []byte, stream bool) (remote.RemoteResponse, error) {
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		resp, err := c.executeRequest(ctx, payload, stream)
		if err == nil {
			return resp, nil
		}
		if !isRetryable(err) || attempt == c.maxRetries {
			return remote.RemoteResponse{}, err
		}
		if c.logger != nil {
			c.logger.Warn("remote execution retry", "attempt", attempt+1, "error", err)
		}
	}
	return remote.RemoteResponse{}, fmt.Errorf("%w: retries exhausted", remote.ErrRemoteExecutionFailed)
}

func (c *Client) executeRequest(ctx context.Context, payload []byte, stream bool) (remote.RemoteResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), strings.NewReader(string(payload)))
	if err != nil {
		return remote.RemoteResponse{}, fmt.Errorf("%w: build request: %v", remote.ErrConnectionFailed, err)
	}

	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
		signRequest(req, payload, c.authToken)
	}

	if c.logger != nil {
		c.logger.Info("remote execution request", "endpoint", c.endpoint.String(), "stream", stream)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return remote.RemoteResponse{}, fmt.Errorf("%w: %v", remote.ErrConnectionFailed, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return remote.RemoteResponse{}, fmt.Errorf("%w: server error %d: %s", remote.ErrRemoteExecutionFailed, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return remote.RemoteResponse{}, fmt.Errorf("%w: status %d: %s", remote.ErrRemoteExecutionFailed, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if stream && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		payloadResult, err := readStream(resp.Body)
		if err != nil {
			return remote.RemoteResponse{}, err
		}
		return remote.RemoteResponse{Result: &payloadResult}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return remote.RemoteResponse{}, fmt.Errorf("%w: read response: %v", remote.ErrRemoteExecutionFailed, err)
	}

	var response remote.RemoteResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return remote.RemoteResponse{}, fmt.Errorf("%w: decode response: %v", remote.ErrRemoteExecutionFailed, err)
	}

	return response, nil
}

func readStream(body io.Reader) (remote.ExecuteResultPayload, error) {
	decoder := newSSEDecoder(body)
	var result remote.ExecuteResultPayload
	for {
		event, err := decoder.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return remote.ExecuteResultPayload{}, fmt.Errorf("%w: stream decode: %v", remote.ErrRemoteExecutionFailed, err)
		}
		switch event.Name {
		case "stdout":
			result.Stdout += event.Data
		case "stderr":
			result.Stderr += event.Data
		case "result":
			var payload remote.ExecuteResultPayload
			if err := json.Unmarshal([]byte(event.Data), &payload); err == nil {
				if payload.Stdout == "" {
					payload.Stdout = result.Stdout
				}
				if payload.Stderr == "" {
					payload.Stderr = result.Stderr
				}
				if len(payload.ToolCalls) == 0 {
					payload.ToolCalls = result.ToolCalls
				}
				result = payload
			}
		case "error":
			return remote.ExecuteResultPayload{}, fmt.Errorf("%w: %s", remote.ErrRemoteExecutionFailed, event.Data)
		}
	}
	return result, nil
}

func signRequest(req *http.Request, payload []byte, token string) {
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(payload)
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	req.Header.Set("X-Toolruntime-Timestamp", timestamp)
	req.Header.Set("X-Toolruntime-Signature", signature)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	return true
}
