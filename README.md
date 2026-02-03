# toolexec-integrations

Concrete runtime backend clients for [`toolexec`](https://github.com/jonwraymond/toolexec). This repo keeps heavy SDKs (client-go, HTTP stacks) out of the core execution library while providing opt‑in integrations.

## Packages

- `kubernetes`: client-go implementation of `kubernetes.PodRunner` and `kubernetes.HealthChecker`
- `proxmox`: HTTP API client implementing `proxmox.APIClient` for LXC status/start/stop
- `remotehttp`: HTTP/SSE client implementing `remote.RemoteClient` for remote runtimes

## Quick Wiring

```go
package main

import (
    corekube "github.com/jonwraymond/toolexec/runtime/backend/kubernetes"
    kubeint "github.com/jonwraymond/toolexec-integrations/kubernetes"
)

func buildKubeBackend() *corekube.Backend {
    client, _ := kubeint.NewClient(kubeint.ClientConfig{
        KubeconfigPath: "~/.kube/config",
        Context:        "prod",
    }, nil)

    return corekube.New(corekube.Config{
        Namespace: "default",
        Image:     "toolruntime-sandbox:latest",
        Client:    client,
    })
}
```

## Versioning

See `VERSIONS.md` for the compatibility matrix (source of truth is `ai-tools-stack`).
