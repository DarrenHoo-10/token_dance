import { readFile, mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const specPath = path.join(root, "schemas", "protocol", "v1", "spec.json");
const spec = JSON.parse(await readFile(specPath, "utf8"));
const unionMembers = new Set(Object.values(spec.unions ?? {}).flat());

const primitiveSchemas = {
  string: { type: "string", maxLength: 1024 },
  shortString: { type: "string", minLength: 1, maxLength: 160 },
  boolean: { type: "boolean" },
  uint32: { type: "integer", minimum: 0, maximum: 4294967295 },
  uint64String: { type: "string", pattern: "^(0|[1-9][0-9]*)$", maxLength: 20 },
  decimal: { type: "string", pattern: "^(0|[1-9][0-9]*)(?:\\.[0-9]{1,8})?$", maxLength: 40 },
  datetime: { type: "string", format: "date-time" },
  version: { type: "string", pattern: "^[0-9]+(?:\\.[0-9]+){0,2}(?:[-+][0-9A-Za-z.-]+)?$", maxLength: 32 },
  identifier: { type: "string", pattern: "^[A-Za-z0-9][A-Za-z0-9._:-]*$", minLength: 1, maxLength: 160 },
  prefixedId: { type: "string", pattern: "^[a-z]{3}_[0-9A-HJKMNP-TV-Z]{26}$", minLength: 30, maxLength: 30 },
  hmac: { type: "string", pattern: "^hmac-sha256:[A-Za-z0-9_-]{43}$", minLength: 55, maxLength: 55 },
  base64url32: { type: "string", pattern: "^[A-Za-z0-9_-]{43}$", minLength: 43, maxLength: 43 },
  currency: { type: "string", pattern: "^[A-Z]{3}$", minLength: 3, maxLength: 3 },
  domain: { type: "string", pattern: "^(?=.{1,253}$)(?!.*\\*)(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?\\.)+[A-Za-z]{2,63}$" },
  relativeGlob: { type: "string", pattern: "^(?![A-Za-z]:)(?!/)(?!.*(?:^|/)\\.\\.(?:/|$))[^\\u0000]{1,512}$" },
  schemaUri: { type: "string", const: "https://schemas.tokenshow.dev/adapter-manifest/v1.json" }
};

const tsPrimitive = {
  string: "string", shortString: "string", boolean: "boolean", uint32: "number",
  uint64String: "UInt64String", decimal: "DecimalString", datetime: "string",
  version: "string", identifier: "string", prefixedId: "string", hmac: "HmacSha256",
  base64url32: "Base64Url32", currency: "string", domain: "string", relativeGlob: "string", schemaUri: "string"
};
const rustPrimitive = {
  string: "String", shortString: "String", boolean: "bool", uint32: "u32",
  uint64String: "UInt64String", decimal: "DecimalString", datetime: "String",
  version: "String", identifier: "String", prefixedId: "String", hmac: "HmacSha256",
  base64url32: "Base64Url32", currency: "String", domain: "String", relativeGlob: "String", schemaUri: "String"
};
const goPrimitive = {
  string: "string", shortString: "string", boolean: "bool", uint32: "uint32",
  uint64String: "UInt64String", decimal: "DecimalString", datetime: "string",
  version: "string", identifier: "string", prefixedId: "string", hmac: "HmacSha256",
  base64url32: "Base64Url32", currency: "string", domain: "string", relativeGlob: "string", schemaUri: "string"
};

const toSnake = value => value.replace(/([a-z0-9])([A-Z])/g, "$1_$2").replace(/^\$/, "").toLowerCase();
const toPascal = value => value.replace(/^\$/, "").replace(/([a-z0-9])([A-Z])/g, "$1_$2").split(/[_-]/).map(part => {
  const normalized = part.toLowerCase();
  return normalized ? normalized[0].toUpperCase() + normalized.slice(1) : "";
}).join("").replace(/Id$/g, "ID").replace(/Hmac$/g, "HMAC");
const rustReserved = new Set(["type", "match", "ref", "self", "super", "crate", "mod", "enum", "struct", "trait", "use", "fn"]);
const rustFieldName = value => {
  const name = toSnake(value);
  return rustReserved.has(name) ? `r#${name}` : name;
};

function parseType(raw) {
  const array = raw.endsWith("[]");
  const value = array ? raw.slice(0, -2) : raw;
  if (value.startsWith("const:")) return { array, base: "const", constValue: value.slice(6) };
  return { array, base: value, constValue: null };
}

function schemaFor(raw) {
  const { array, base, constValue } = parseType(raw);
  let value;
  if (base === "const") value = { type: "string", const: constValue };
  else value = primitiveSchemas[base] ? structuredClone(primitiveSchemas[base]) : { $ref: `#/$defs/${base}` };
  return array ? { type: "array", items: value, maxItems: 256 } : value;
}

const defs = {};
for (const [name, values] of Object.entries(spec.enums)) defs[name] = { type: "string", enum: values };
for (const [name, fields] of Object.entries(spec.objects)) {
  const properties = {};
  const required = [];
  for (const [rawName, rawType] of Object.entries(fields)) {
    const optional = rawName.endsWith("?");
    const fieldName = optional ? rawName.slice(0, -1) : rawName;
    properties[fieldName] = schemaFor(rawType);
    if (!optional) required.push(fieldName);
  }
  if (name === "UploadBatch") properties.events.maxItems = 500;
  if (name === "UploadPolicy") properties.maxBatchEvents.maximum = 500;
  if (name === "AdapterManifest") {
    properties.platforms.uniqueItems = true;
    properties.sources.uniqueItems = true;
    properties.capabilities.uniqueItems = true;
  }
  defs[name] = { type: "object", additionalProperties: false, properties, required };
}
for (const [name, members] of Object.entries(spec.unions ?? {})) {
  defs[name] = { oneOf: members.map(member => ({ $ref: `#/$defs/${member}` })) };
}

const sessionEventTypes = ["session_started", "session_ended", "turn_started", "turn_completed", "model_usage_recorded", "tool_invoked", "skill_invoked", "code_changed", "agent_spawned"];
defs.EventEnvelope.allOf = [
  {
    if: { type: "object", properties: { payload: { type: "object", properties: { type: { enum: sessionEventTypes } }, required: ["type"] } }, required: ["payload"] },
    then: { required: ["sessionHash"] }
  },
  {
    if: { type: "object", properties: { payload: { type: "object", properties: { type: { enum: ["turn_started", "turn_completed"] } }, required: ["type"] } }, required: ["payload"] },
    then: { required: ["turnHash"] }
  }
];

const baseSchema = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: "https://schemas.tokenshow.dev/protocol/v1.json",
  title: "TokenShow Collector Protocol v1",
  oneOf: spec.roots.map(name => ({ $ref: `#/$defs/${name}` })),
  $defs: defs
};

function fieldType(raw, primitives, arrayPrefix, arraySuffix) {
  const { array, base, constValue } = parseType(raw);
  let mapped = base === "const" ? (primitives === tsPrimitive ? JSON.stringify(constValue) : primitives === goPrimitive ? "EventType" : "EventType") : (primitives[base] ?? base);
  if (array) mapped = `${arrayPrefix}${mapped}${arraySuffix}`;
  return mapped;
}

let ts = `// Generated by tools/generate-protocol.mjs from schemas/protocol/v1/spec.json.\n// Do not edit manually.\n\nexport const PROTOCOL_VERSION = ${JSON.stringify(spec.version)} as const;\nexport type UInt64String = string;\nexport type DecimalString = string;\nexport type HmacSha256 = string;\nexport type Base64Url32 = string;\n\n`;
for (const [name, values] of Object.entries(spec.enums)) {
  ts += `export const ${name}Values = ${JSON.stringify(values)} as const;\nexport type ${name} = (typeof ${name}Values)[number];\n\n`;
}
for (const [name, fields] of Object.entries(spec.objects)) {
  ts += `export interface ${name} {\n`;
  for (const [rawName, rawType] of Object.entries(fields)) {
    const optional = rawName.endsWith("?");
    const fieldName = optional ? rawName.slice(0, -1) : rawName;
    const renderedName = /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(fieldName) ? fieldName : JSON.stringify(fieldName);
    ts += `  ${renderedName}${optional ? "?" : ""}: ${fieldType(rawType, tsPrimitive, "Array<", ">")}\n`;
  }
  ts += `}\n\n`;
}
for (const [name, members] of Object.entries(spec.unions ?? {})) ts += `export type ${name} = ${members.join(" | ")}\n\n`;

let rust = `// Generated by tools/generate-protocol.mjs from schemas/protocol/v1/spec.json.\n// Do not edit manually.\n\nuse serde::{Deserialize, Serialize};\n\npub const PROTOCOL_VERSION: &str = ${JSON.stringify(spec.version)};\npub type UInt64String = String;\npub type DecimalString = String;\npub type HmacSha256 = String;\npub type Base64Url32 = String;\n\n`;
for (const [name, values] of Object.entries(spec.enums)) {
  rust += `#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]\npub enum ${name} {\n`;
  for (const value of values) rust += `    #[serde(rename = ${JSON.stringify(value)})]\n    ${toPascal(value)},\n`;
  rust += `}\n\n`;
}
for (const [name, fields] of Object.entries(spec.objects)) {
  rust += `#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]\n#[serde(rename_all = "camelCase", deny_unknown_fields)]\npub struct ${name} {\n`;
  for (const [rawName, rawType] of Object.entries(fields)) {
    const optional = rawName.endsWith("?");
    const fieldName = optional ? rawName.slice(0, -1) : rawName;
    const parsed = parseType(rawType);
    if (unionMembers.has(name) && parsed.base === "const") continue;
    let mapped = fieldType(rawType, rustPrimitive, "Vec<", ">");
    if (optional) mapped = `Option<${mapped}>`;
    const needsRename = fieldName.startsWith("$") || rustFieldName(fieldName).replace(/^r#/, "") !== toSnake(fieldName);
    if (fieldName.startsWith("$")) rust += `    #[serde(rename = ${JSON.stringify(fieldName)})]\n`;
    if (optional) rust += `    #[serde(skip_serializing_if = "Option::is_none")]\n`;
    rust += `    pub ${rustFieldName(fieldName)}: ${mapped},\n`;
  }
  rust += `}\n\n`;
}
for (const [name, members] of Object.entries(spec.unions ?? {})) {
  rust += `#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]\n#[serde(tag = "type")]\npub enum ${name} {\n`;
  for (const member of members) {
    const typeField = Object.entries(spec.objects[member]).find(([, value]) => String(value).startsWith("const:"));
    const eventType = typeField[1].slice(6);
    rust += `    #[serde(rename = ${JSON.stringify(eventType)})]\n    ${member.replace(/Payload$/, "")}(${member}),\n`;
  }
  rust += `}\n\n`;
}

let go = `// Code generated by tools/generate-protocol.mjs; DO NOT EDIT.\n\npackage protocol\n\nimport (\n\t"bytes"\n\t"encoding/json"\n\t"fmt"\n\t"io"\n)\n\nconst ProtocolVersion = ${JSON.stringify(spec.version)}\n\ntype UInt64String string\ntype DecimalString string\ntype HmacSha256 string\ntype Base64Url32 string\n\n`;
for (const [name, values] of Object.entries(spec.enums)) {
  go += `type ${name} string\n\nconst (\n`;
  for (const value of values) go += `\t${name}${toPascal(value)} ${name} = ${JSON.stringify(value)}\n`;
  go += `)\n\n`;
}
for (const [name, fields] of Object.entries(spec.objects)) {
  go += `type ${name} struct {\n`;
  for (const [rawName, rawType] of Object.entries(fields)) {
    const optional = rawName.endsWith("?");
    const fieldName = optional ? rawName.slice(0, -1) : rawName;
    let mapped = fieldType(rawType, goPrimitive, "[]", "");
    if (optional && !mapped.startsWith("[]")) mapped = `*${mapped}`;
    go += `\t${toPascal(fieldName)} ${mapped} \`json:${JSON.stringify(fieldName + (optional ? ",omitempty" : ""))}\`\n`;
  }
  go += `}\n\n`;
}
for (const [name, members] of Object.entries(spec.unions ?? {})) {
  go += `type ${name} struct { Value any }\n\n`;
  go += `func (p *${name}) UnmarshalJSON(data []byte) error {\n\tvar discriminator struct { Type EventType \`json:"type"\` }\n\tif err := json.Unmarshal(data, &discriminator); err != nil { return err }\n\tdecode := func(target any) error { d := json.NewDecoder(bytes.NewReader(data)); d.DisallowUnknownFields(); if err := d.Decode(target); err != nil { return err }; if err := d.Decode(&struct{}{}); err != io.EOF { return fmt.Errorf("trailing JSON") }; return nil }\n\tswitch discriminator.Type {\n`;
  for (const member of members) {
    const typeField = Object.entries(spec.objects[member]).find(([, value]) => String(value).startsWith("const:"));
    const eventType = typeField[1].slice(6);
    go += `\tcase EventType${toPascal(eventType)}:\n\t\tvar value ${member}; if err := decode(&value); err != nil { return err }; p.Value = value\n`;
  }
  go += `\tdefault:\n\t\treturn fmt.Errorf("unsupported event payload type %q", discriminator.Type)\n\t}\n\treturn nil\n}\n\nfunc (p ${name}) MarshalJSON() ([]byte, error) { if p.Value == nil { return nil, fmt.Errorf("event payload is nil") }; return json.Marshal(p.Value) }\n\n`;
}

const gofmt = spawnSync("gofmt", [], { input: go, encoding: "utf8" });
if (gofmt.status === 0) go = gofmt.stdout;
else if (gofmt.error?.code !== "ENOENT") throw new Error(gofmt.stderr || "gofmt failed");

const outputs = [
  [path.join(root, "schemas", "protocol", "v1", "protocol.schema.json"), JSON.stringify(baseSchema, null, 2) + "\n"],
  [path.join(root, "web", "src", "protocol", "generated.ts"), ts],
  [path.join(root, "collector", "crates", "protocol", "src", "generated.rs"), rust],
  [path.join(root, "server", "internal", "protocol", "generated.go"), go]
];
const rootSchemaContent = new Map();
for (const rootName of spec.roots) {
  const fileName = rootName.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase() + ".schema.json";
  const rootSchema = {
    $schema: "https://json-schema.org/draft/2020-12/schema",
    $id: `https://schemas.tokenshow.dev/protocol/v1/${fileName}`,
    $ref: `#/$defs/${rootName}`,
    $defs: defs
  };
  const content = JSON.stringify(rootSchema, null, 2) + "\n";
  rootSchemaContent.set(rootName, content);
  outputs.push([path.join(root, "schemas", "protocol", "v1", fileName), content]);
}
outputs.push(
  [path.join(root, "collector", "schemas", "events", "v1", "event-envelope.schema.json"), rootSchemaContent.get("EventEnvelope")],
  [path.join(root, "collector", "schemas", "adapter-manifest", "v1", "adapter-manifest.schema.json"), rootSchemaContent.get("AdapterManifest")]
);

const checkOnly = process.argv.includes("--check");
const stale = [];
for (const [target, content] of outputs) {
  if (checkOnly) {
    let existing = "";
    try { existing = await readFile(target, "utf8"); } catch { stale.push(path.relative(root, target)); continue; }
    if (existing !== content) stale.push(path.relative(root, target));
  } else {
    await mkdir(path.dirname(target), { recursive: true });
    await writeFile(target, content, "utf8");
  }
}
if (stale.length) {
  console.error(`Generated protocol artifacts are stale:\n${stale.map(item => `- ${item}`).join("\n")}`);
  process.exitCode = 1;
} else console.log(`${checkOnly ? "Verified" : "Generated"} ${outputs.length} protocol artifacts for v${spec.version}`);
