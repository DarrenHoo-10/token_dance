import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const read = relative => readFile(path.join(root, relative), "utf8");
const readJson = async relative => JSON.parse(await read(relative));
const spec = await readJson("schemas/protocol/v1/spec.json");
const schema = await readJson("schemas/protocol/v1/protocol.schema.json");
const [rust, go, ts, ddl, migration] = await Promise.all([
  read("collector/crates/protocol/src/generated.rs"),
  read("server/internal/protocol/generated.go"),
  read("web/src/protocol/generated.ts"),
  read("docs/ddl/mysql/0001_tokenshow_server.sql"),
  read("server/db/migrations/0001_tokenshow_server.sql")
]);

const generation = spawnSync(process.execPath, ["tools/generate-protocol.mjs", "--check"], { cwd: root, encoding: "utf8" });
assert.equal(generation.status, 0, generation.stderr || generation.stdout);
assert.equal(migration, ddl, "server migration must remain byte-for-byte aligned with documented DDL");
assert.ok(Array.isArray(schema.oneOf) && schema.oneOf.length === spec.roots.length, "top-level schema must validate concrete protocol messages");

const ajv = new Ajv2020({ allErrors: true, strict: true, strictRequired: false });
addFormats(ajv);
const validators = {};
for (const rootName of spec.roots) {
  const fileName = rootName.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase() + ".schema.json";
  validators[rootName] = ajv.compile(await readJson(`schemas/protocol/v1/${fileName}`));
}
const validateProtocol = ajv.compile(schema);

const id = prefix => `${prefix}_${"0".repeat(26)}`;
const hmac = `hmac-sha256:${"A".repeat(43)}`;
const eventId = "B".repeat(43);
const validEnvelope = {
  schemaVersion: "1.0",
  eventId,
  adapterId: "dev.tokenshow.adapter.mock",
  adapterVersion: "1.0.0",
  agentId: "mock-agent",
  agentVersion: "1.2.3",
  installationId: id("ins"),
  occurredAt: "2026-08-30T00:00:00.000Z",
  sessionHash: hmac,
  source: { kind: "jsonl_tail", cursorHmac: hmac, rawFingerprintHmac: hmac },
  accuracy: "exact",
  payload: {
    type: "model_usage_recorded",
    providerId: "mock-provider",
    modelId: "mock-model",
    tokens: { inputTokens: "10", outputTokens: "5", totalTokens: "15" }
  }
};
assert.equal(validators.EventEnvelope(validEnvelope), true, JSON.stringify(validators.EventEnvelope.errors));
assert.equal(validateProtocol(validEnvelope), true, JSON.stringify(validateProtocol.errors));
assert.equal(validators.EventEnvelope({}), false, "empty object must not pass EventEnvelope schema");

const missingTokens = structuredClone(validEnvelope);
delete missingTokens.payload.tokens;
assert.equal(validators.EventEnvelope(missingTokens), false, "model usage requires tokens");
const unknownPayloadField = structuredClone(validEnvelope);
unknownPayloadField.payload.prompt = "TOKSHOW_TEST_PROMPT_SECRET";
assert.equal(validators.EventEnvelope(unknownPayloadField), false, "unknown/sensitive payload field must be rejected");
const nullOptional = structuredClone(validEnvelope);
nullOptional.agentVersion = null;
assert.equal(validators.EventEnvelope(nullOptional), false, "optional fields may be absent but not null");
const rawCursor = structuredClone(validEnvelope);
rawCursor.source.cursorHmac = "C:/Users/private/session.jsonl";
assert.equal(validators.EventEnvelope(rawCursor), false, "raw cursor/path must not pass the HMAC field");
const missingTurnHash = structuredClone(validEnvelope);
missingTurnHash.payload = { type: "turn_completed", success: true };
assert.equal(validators.EventEnvelope(missingTurnHash), false, "turn events require turnHash");
missingTurnHash.turnHash = hmac;
assert.equal(validators.EventEnvelope(missingTurnHash), true, JSON.stringify(validators.EventEnvelope.errors));

const validManifest = {
  $schema: "https://schemas.tokenshow.dev/adapter-manifest/v1.json",
  manifestVersion: "1.0",
  id: "dev.tokenshow.adapter.mock",
  name: "Mock Adapter",
  version: "1.0.0",
  protocolVersion: "1.0",
  agent: { id: "mock-agent", versionRange: ">=1.0.0" },
  platforms: ["windows-x64", "macos-arm64"],
  sources: ["jsonl_tail"],
  permissions: {
    readPaths: [{ template: "AGENT_CONFIG_HOME", relativeGlob: "sessions/**", access: "read" }],
    writePaths: [],
    commands: [{ executableId: "mock", args: ["--version"] }],
    networkDomains: ["api.example.com"]
  },
  capabilities: ["tokens", "sessions"]
};
assert.equal(validators.AdapterManifest(validManifest), true, JSON.stringify(validators.AdapterManifest.errors));
const wildcardManifest = structuredClone(validManifest);
wildcardManifest.permissions.networkDomains = ["*.example.com"];
assert.equal(validators.AdapterManifest(wildcardManifest), false, "wildcard network permission must be rejected");
const traversalManifest = structuredClone(validManifest);
traversalManifest.permissions.readPaths[0].relativeGlob = "../../secrets/**";
assert.equal(validators.AdapterManifest(traversalManifest), false, "path traversal must be rejected");

const validRegistration = {
  devicePublicKey: "D".repeat(43), osType: "windows", architecture: "x86_64", collectorVersion: "1.0.0"
};
assert.equal(validators.InstallationRegisterRequest(validRegistration), true, JSON.stringify(validators.InstallationRegisterRequest.errors));
const oversizedBatch = { batchId: id("bat"), installationId: id("ins"), createdAt: "2026-08-30T00:00:00.000Z", events: Array.from({ length: 501 }, () => validEnvelope) };
assert.equal(validators.UploadBatch(oversizedBatch), false, "upload batch must reject more than 500 events");

for (const [name, values] of Object.entries(spec.enums)) {
  assert.deepEqual(schema.$defs[name].enum, values, `${name} schema enum drifted`);
  for (const value of values) {
    assert.ok(rust.includes(`rename = ${JSON.stringify(value)}`), `Rust missing ${name}.${value}`);
    assert.ok(go.includes(`= ${JSON.stringify(value)}`), `Go missing ${name}.${value}`);
    assert.ok(ts.includes(JSON.stringify(value)), `TypeScript missing ${name}.${value}`);
  }
}
for (const name of Object.keys(spec.objects)) assert.equal(schema.$defs[name].additionalProperties, false, `${name} must reject unknown fields`);
for (const status of spec.enums.AdapterRuntimeStatus) assert.ok(ddl.includes(`'${status}'`), `DDL missing AdapterRuntimeStatus ${status}`);
for (const value of spec.enums.EventType) assert.ok(ddl.includes(`'${value}'`), `DDL missing EventType ${value}`);
for (const value of spec.enums.Accuracy) assert.ok(ddl.includes(`'${value}'`), `DDL missing Accuracy ${value}`);
for (const value of spec.enums.SourceKind) assert.ok(ddl.includes(`'${value}'`), `DDL missing SourceKind ${value}`);
for (const value of spec.enums.CostSource) assert.ok(ddl.includes(`'${value}'`), `DDL missing CostSource ${value}`);
assert.ok(ddl.includes("schema_version           VARCHAR(16)"), "DDL schema_version must preserve wire version 1.0");
assert.ok(ddl.includes("source_cursor_hmac       BINARY(32)"));
assert.ok(ddl.includes("raw_fingerprint_hmac     BINARY(32)"));
assert.ok(ddl.includes("token_tool               BIGINT UNSIGNED"));
assert.ok(ddl.includes("event_count <= 500"));
assert.ok(rust.includes("skip_serializing_if = \"Option::is_none\""), "Rust optional fields must omit None");
assert.ok(rust.includes("#[serde(tag = \"type\")]"), "Rust payload must be a tagged enum");
assert.ok(go.includes("DisallowUnknownFields"), "Go payload decoder must reject unknown fields");
assert.ok(ts.includes("export type EventPayload = SessionStartedPayload |"), "TypeScript payload must be a discriminated union");
assert.ok(ts.includes("inputTokens?: UInt64String"), "TypeScript uint64 wire values must be strings");
assert.ok(!spec.enums.Accuracy.includes("unavailable"), "unavailable belongs to capability availability, not event accuracy");

console.log(`Protocol v${spec.version}: ${Object.keys(spec.enums).length} enums, ${Object.keys(spec.objects).length} objects, ${Object.keys(spec.unions).length} strict union, and ${spec.roots.length} root schemas verified.`);
