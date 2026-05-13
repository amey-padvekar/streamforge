# Workstream 4 Validation Guide

This guide validates observability requirements from Step 4.5.

## Goals validated

- Metrics endpoint scrapes without errors.
- Logs from agent/server/viewer share consistent keys.
- Histogram output reflects induced load scenarios.

## One-command validation

From repository root:

```powershell
./scripts/validate-workstream4.ps1
```

This executes:

- observability-focused Go tests
- `/metrics` scrape checks
- induced fanout load histogram check
- error category policy scan
- canonical key presence scan
- viewer build validation

## Manual load scenario (optional)

Use this to validate behavior on a live run:

1. Start server:

```powershell
go run ./cmd/server
```

2. Create session:

```powershell
Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/sessions
```

3. Start agent and viewer using returned credentials.
4. Induce pressure by throttling viewer network or opening multiple viewers.
5. Verify `/metrics` includes:

- `streamforge_server_routing_latency_ms_bucket`
- `streamforge_server_routing_latency_ms_count`
- `streamforge_transport_errors_total{role,category}` with only allowed categories

6. Verify logs include canonical keys:

- `sessionId`
- `role`
- `frameId`
- `packetType`
- `queueDepth`
- `framesDropped`
- `errorCategory`

## Allowed error categories

- `auth`
- `protocol`
- `transport`
- `timeout`
- `backpressure`
- `internal`
