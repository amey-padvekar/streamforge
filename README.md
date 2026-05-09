# Streamforge

Streamforge is a low-latency remote display streaming platform focused on real-time desktop capture, transport, browser viewing, and remote interaction.

## Repository layout

- `cmd/agent` — agent entrypoint
- `cmd/server` — streaming server entrypoint
- `internal/agent` — capture, encoding, scheduling, input, and transport internals
- `internal/server` — auth, metrics, routing, sessions, and transport internals
- `internal/protocol` — shared binary protocol definitions and helpers
- `web/viewer` — browser viewer application and future SDK extraction point
- `docs/design` — architecture and implementation documents
- `docs/adr` — architectural decision records
- `docs/explanations` — supporting explanatory docs

## Phase 0 status

Current scaffolding provides:
- initial Go module setup
- minimal agent and server binaries
- frontend viewer workspace scaffold
- ADR/documentation structure
- baseline ignore and editor conventions

## Next steps

Follow [docs/design/streamforge-implementation-plan.md](docs/design/streamforge-implementation-plan.md) starting with Phase 1 to implement session bootstrap, WebSocket streaming, Windows capture, and browser rendering.
