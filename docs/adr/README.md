# Architectural Decision Records

Use this directory to capture high-impact decisions that affect Streamforge architecture, interfaces, operations, or delivery tradeoffs.

## ADR format

Each ADR should include:
- title
- status
- context
- decision
- consequences
- alternatives considered

## Naming convention

Use zero-padded numeric prefixes:
- `ADR-001-<short-name>.md`
- `ADR-002-<short-name>.md`

## Initial ADR candidates

- repository structure and package boundaries
- WebSocket-first transport choice
- JPEG for MVP frame encoding
- in-memory session registry for early phases
- Windows-only GDI capture for Phase 1
