/**
 * Protocol v2 generated types.
 * Content-hash reference implementation: `tools/protocol-v2/canonical.mjs`
 * (mirrored by Rust `collector/crates/protocol/src/v2` and Go `server/internal/protocol/v2`).
 */
export {
  PROTOCOL_VERSION,
  PROTOCOL_VERSION_NUMBER,
  SCHEMA_VERSION,
  METRIC_SEMANTICS_VERSION,
  DEFAULT_MAX_BATCH_EVENTS,
  DEFAULT_MAX_BATCH_BYTES,
  AckResultValues,
  EventTypeValues,
  AccuracyValues,
  TimeSourceValues,
  CostSourceValues
} from "./generated";

export type {
  AckResult,
  EventAck,
  EventEnvelope,
  EventPayload,
  EventType,
  TelemetryCapabilities,
  TelemetryEventsRequest,
  TelemetryEventsResponse
} from "./generated";
