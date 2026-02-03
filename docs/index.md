# toolexec-integrations

This repo hosts **opt‑in runtime clients** for `toolexec` backends. Core `toolexec` stays interface‑only; these packages supply concrete SDK integrations.

## Packages

| Package | Implements | Typical Use |
|---|---|---|
| `kubernetes` | `kubernetes.PodRunner`, `kubernetes.HealthChecker` | Run executions in Kubernetes Jobs/Pods via client‑go |
| `proxmox` | `proxmox.APIClient` | Control Proxmox LXC status/start/stop |
| `remotehttp` | `remote.RemoteClient` | Call remote runtime service over HTTP/SSE |

## Wiring Examples

### Kubernetes

```go
corekube := kubernetescore.New(kubernetescore.Config{
    Namespace: "default",
    Image:     "toolruntime-sandbox:latest",
    Client:    kubeClient,
})
```

### Proxmox

```go
backend := proxmoxcore.New(proxmoxcore.Config{
    Node:                  "pve-1",
    VMID:                  100,
    Client:                proxmoxClient,
    RuntimeClient:         runtimeClient,
    RuntimeGatewayEndpoint:"https://gateway.internal",
})
```

### Remote HTTP

```go
client, _ := remotehttp.NewClient(remotehttp.Config{
    Endpoint:  "https://runtime.example.com/v1/execute",
    AuthToken: "...",
})
backend := remote.New(remote.Config{Client: client})
```

## Compatibility

Use `ai-tools-stack` for the authoritative version matrix. This repo tracks those versions in `VERSIONS.md`.
