package remotehttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonwraymond/toolexec/runtime/backend/remote"
)

func TestClientExecuteSuccess(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer token")
		}
		if sig := r.Header.Get("X-Toolruntime-Signature"); sig == "" {
			t.Error("expected signature header")
		}

		var req remote.RemoteRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Request.Code != "return 42" {
			t.Errorf("request code = %q", req.Request.Code)
		}

		resp := remote.RemoteResponse{
			Result: &remote.ExecuteResultPayload{
				Value:          map[string]any{"answer": 42},
				Stdout:         "ok",
				DurationMillis: 12,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		Endpoint:  srv.URL,
		AuthToken: "token",
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	resp, err := client.Execute(context.Background(), remote.RemoteRequest{
		Request: remote.ExecutePayload{Code: "return 42"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if resp.Result == nil || resp.Result.Stdout != "ok" {
		t.Fatalf("unexpected result: %#v", resp.Result)
	}
}

func TestClientExecuteRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := remote.RemoteResponse{Result: &remote.ExecuteResultPayload{Stdout: "ok"}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		Endpoint:   srv.URL,
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	resp, err := client.Execute(context.Background(), remote.RemoteRequest{
		Request: remote.ExecutePayload{Code: "return"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if resp.Result == nil || resp.Result.Stdout != "ok" {
		t.Fatalf("unexpected result: %#v", resp.Result)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestClientExecuteStreaming(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: stdout\n"))
		_, _ = w.Write([]byte("data: hello\n\n"))
		result := remote.ExecuteResultPayload{
			Value:          "ok",
			Stdout:         "hello",
			DurationMillis: 5,
		}
		data, _ := json.Marshal(result)
		_, _ = w.Write([]byte("event: result\n"))
		_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
	}))
	defer srv.Close()

	client, err := NewClient(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	resp, err := client.Execute(context.Background(), remote.RemoteRequest{
		Request: remote.ExecutePayload{Code: "return"},
		Stream:  true,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if resp.Result == nil || resp.Result.Stdout != "hello" {
		t.Fatalf("unexpected stdout: %#v", resp.Result)
	}
	if resp.Result.Value != "ok" {
		t.Fatalf("unexpected value: %#v", resp.Result.Value)
	}
}
