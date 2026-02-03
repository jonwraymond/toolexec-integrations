package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	coreproxmox "github.com/jonwraymond/toolexec/runtime/backend/proxmox"
)

type APIClient = coreproxmox.APIClient
type LXCStatus = coreproxmox.LXCStatus
type Logger = coreproxmox.Logger

var (
	ErrProxmoxNotAvailable = coreproxmox.ErrProxmoxNotAvailable
	ErrAuthNotConfigured   = coreproxmox.ErrAuthNotConfigured
)

// ClientConfig configures the Proxmox API client.
type ClientConfig struct {
	// Endpoint is the Proxmox API base URL (e.g., https://host:8006/api2/json).
	Endpoint string

	// TokenID is the user@realm!tokenid portion of the API token.
	TokenID string

	// TokenSecret is the API token secret UUID.
	TokenSecret string

	// TLSSkipVerify disables TLS verification (dev only).
	TLSSkipVerify bool

	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client

	// Timeout sets request timeout if HTTPClient is not provided.
	Timeout time.Duration
}

// Client is a Proxmox API client.
type Client struct {
	baseURL     *url.URL
	tokenID     string
	tokenSecret string
	httpClient  *http.Client
	logger      Logger
}

// NewClient creates a new Proxmox API client.
func NewClient(cfg ClientConfig, logger Logger) (*Client, error) {
	if cfg.Endpoint == "" {
		return nil, ErrProxmoxNotAvailable
	}
	parsed, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if cfg.TokenID == "" || cfg.TokenSecret == "" {
		return nil, ErrAuthNotConfigured
	}
	client := cfg.HTTPClient
	if client == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.TLSSkipVerify {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		client = &http.Client{
			Transport: transport,
			Timeout:   timeout,
		}
	}

	return &Client{
		baseURL:     parsed,
		tokenID:     cfg.TokenID,
		tokenSecret: cfg.TokenSecret,
		httpClient:  client,
		logger:      logger,
	}, nil
}

// Status returns current LXC status.
func (c *Client) Status(ctx context.Context, node string, vmid int) (LXCStatus, error) {
	path := fmt.Sprintf("/nodes/%s/lxc/%d/status/current", node, vmid)
	var status LXCStatus
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &status); err != nil {
		return LXCStatus{}, err
	}
	return status, nil
}

// Start boots the LXC container.
func (c *Client) Start(ctx context.Context, node string, vmid int) error {
	path := fmt.Sprintf("/nodes/%s/lxc/%d/status/start", node, vmid)
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// Stop halts the LXC container.
func (c *Client) Stop(ctx context.Context, node string, vmid int) error {
	path := fmt.Sprintf("/nodes/%s/lxc/%d/status/stop", node, vmid)
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body io.Reader, out any) error {
	u := *c.baseURL
	u.Path = strings.TrimSuffix(u.Path, "/") + path

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", c.tokenID, c.tokenSecret))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("proxmox api %s %s: %s", method, path, strings.TrimSpace(string(data)))
	}

	if out == nil {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Data) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

var _ APIClient = (*Client)(nil)
