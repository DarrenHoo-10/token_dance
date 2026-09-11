import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import {
  ACK_UPLOAD_MAPPING,
  classifyAck,
  computeContentHash,
  contentHashCanonicalJson,
  parseStrictJson
} from "./protocol-v2/canonical.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = relative => readFile(path.join(root, relative), "utf8");
const readJson = async relative => JSON.parse(await read(relative));

const generation = spawnSync(process.execPath, ["tools/generate-protocol.mjs", "--check"], {
  cwd: root,
  encoding: "utf8"
});
assert.equal(generation.status, 0, generation.stderr || generation.stdout);

const spec = await readJson("schemas/protocol/v2/spec.json");
const schema = await readJson("schemas/protocol/v2/protocol.schema.json");
const [rust, go, ts, mappingDoc] = await Promise.all([
  read("collector/crates/protocol/src/v2/generated.rs"),
  read("server/internal/protocol/v2/generated.go"),
  read("web/src/protocol/v2/generated.ts"),
  read("schemas/protocol/v2/ACK_STATUS_MAPPING.md")
]);

assert.equal(spec.protocolVersion, 2);
assert.deepEqual(spec.enums.EventType, [
  "model_usage_recorded",
  "cost_recorded",
  "session_started",
  "session_ended",
  "turn_started",
  "turn_completed",
  "tool_invoked",
  "skill_invoked",
  "code_changed"
]);
assert.deepEqual(spec.enums.AckResult, [
  "accepted", "duplicate", "discarded", "retry", "blocked", "conflict", "invalid"
]);
assert.ok(!spec.enums.EventType.includes("agent_spawned"), "v2 fixed set excludes agent_spawned");

for (const value of spec.enums.AckResult) {
  assert.ok(rust.includes(`rename = ${JSON.stringify(value)}`), `Rust missing AckResult.${value}`);
  assert.ok(go.includes(`= ${JSON.stringify(value)}`), `Go missing AckResult.${value}`);
  assert.ok(ts.includes(JSON.stringify(value)), `TS missing AckResult.${value}`);
  assert.ok(mappingDoc.includes(`\`${value}\``), `ACK mapping doc missing ${value}`);
  assert.ok(ACK_UPLOAD_MAPPING[value], `ACK_UPLOAD_MAPPING missing ${value}`);
}

const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
addFormats(ajv);
const validateEnvelope = ajv.compile(await readJson("schemas/protocol/v2/event-envelope.schema.json"));
const validateCapabilities = ajv.compile(await readJson("schemas/protocol/v2/telemetry-capabilities.schema.json"));
assert.ok(schema.$defs.EventEnvelope.additionalProperties === false);

const fixtures = await readJson("schemas/protocol/v2/fixtures/golden/events.json");
for (const fixture of fixtures) {
  if (fixture.name === "ack_duplicate_vs_conflict") {
    const baseHash = computeContentHash(fixture.base_event);
    assert.equal(baseHash, fixture.base_hash);
    assert.equal(classifyAck(null, baseHash), "accepted");
    assert.equal(classifyAck(baseHash, baseHash), "duplicate");
    const conflictHash = computeContentHash(fixture.conflict_event);
    assert.equal(conflictHash, fixture.conflict_hash);
    assert.equal(classifyAck(baseHash, conflictHash), "conflict");
    continue;
  }
  const event = fixture.event;
  const withHash = { ...event, contentHash: fixture.content_hash };
  assert.equal(validateEnvelope(withHash), true, `${fixture.name}: ${JSON.stringify(validateEnvelope.errors)}`);
  assert.equal(contentHashCanonicalJson(event), fixture.canonical_json, fixture.name);
  assert.equal(computeContentHash(event), fixture.content_hash, fixture.name);
}

const capabilities = {
  protocolVersion: 2,
  supportedSchemaVersions: [2],
  supportedMetricSemanticsVersions: [1],
  maxBatchEvents: spec.defaults.maxBatchEvents,
  maxBatchBytes: spec.defaults.maxBatchBytes,
  serverTimeMs: "1710000000000",
  eventReceiveLowerBoundMs: "1708704000000"
};
assert.equal(validateCapabilities(capabilities), true, JSON.stringify(validateCapabilities.errors));

const dupRaw = await read("schemas/protocol/v2/fixtures/negative/duplicate_keys.json.txt");
assert.throws(() => parseStrictJson(dupRaw.trim()), /duplicate key/);
assert.throws(() => parseStrictJson('{"schemaVersion":NaN}'), /NaN|expected|token/i);

const unknown = await readJson("schemas/protocol/v2/fixtures/negative/unknown_field.json");
assert.throws(() => computeContentHash(unknown), /unknown business field/);
const negative = await readJson("schemas/protocol/v2/fixtures/negative/negative_count.json");
assert.throws(() => computeContentHash(negative), /unsigned decimal|negative/);

assert.ok(ts.includes("export interface TelemetryCapabilities"), "TS capabilities type");
assert.ok(go.includes("type TelemetryCapabilities struct"), "Go capabilities type");
assert.ok(rust.includes("pub struct TelemetryCapabilities"), "Rust capabilities type");
assert.ok(rust.includes('rename = "token_total"'), "Rust must keep snake_case payload wire names");

console.log(`Protocol v2: ${fixtures.length} golden fixtures, ACK mapping, and schema checks passed.`);
