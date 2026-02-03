# Design Notes

## Why Integrations Live Separately

- Keeps `toolexec` core dependency‑light.
- Allows downstream users to opt into only the SDKs they need.
- Enables faster iteration on integrations without destabilizing the core.

## Interface Contracts

Each package implements a minimal interface from `toolexec`:

- `kubernetes.PodRunner`
- `proxmox.APIClient`
- `remote.RemoteClient`

Clients are required to be **concurrency‑safe** and respect `context.Context` cancellation.

## Error Handling

Integrations return errors defined in core when possible (for consistent handling in the stack). Transport‑specific errors should be wrapped with core errors like `remote.ErrRemoteExecutionFailed` or `proxmox.ErrProxmoxNotAvailable`.
