# Architecture

`toolexec-integrations` provides concrete **client implementations** for runtime backends. The core `toolexec` module defines **interfaces/specs/validation** only; integrations satisfy those interfaces.

## Boundary

- Core: `toolexec/runtime/backend/*` (interfaces + validation)
- Integrations: this repo (SDKs + HTTP clients)

A hard dependency guard in `toolexec` prevents SDKs (client‑go, Docker, etc.) from leaking into core.

## Packages

### Kubernetes

Implements `kubernetes.PodRunner` and `kubernetes.HealthChecker` using client‑go. The client converts `PodSpec` into a Job/Pod, streams logs, and maps results back to `PodResult`.

### Proxmox

Implements `proxmox.APIClient` using the Proxmox HTTP API. Used by the core `proxmox` backend to start/stop LXC containers and check status.

### Remote HTTP

Implements `remote.RemoteClient` using HTTP + optional SSE streaming. The core `remote` backend handles timeouts and request shaping; the integration handles transport, retries, and request signing.
