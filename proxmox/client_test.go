package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientStatus(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "PVEAPIToken=user@pam!token=secret" {
			t.Fatalf("auth header = %q", got)
		}
		if r.URL.Path != "/api2/json/nodes/node-1/lxc/100/status/current" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"status": "running"},
		})
	}))
	defer srv.Close()

	client, err := NewClient(ClientConfig{
		Endpoint:    srv.URL + "/api2/json",
		TokenID:     "user@pam!token",
		TokenSecret: "secret",
	}, nil)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	status, err := client.Status(context.Background(), "node-1", 100)
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if status.Status != "running" {
		t.Fatalf("status = %q", status.Status)
	}
}

func TestClientRequiresToken(t *testing.T) {
	_, err := NewClient(ClientConfig{Endpoint: "https://example.com/api2/json"}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
