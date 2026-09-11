import { createHash } from "node:crypto";

/** Unicode code-point lexicographic compare for JSON object keys. */
export function compareCodePoints(a, b) {
  const max = Math.max(a.length, b.length);
  for (let i = 0; i < max; i += 1) {
    const ca = i < a.length ? a.codePointAt(i) : -1;
    const cb = i < b.length ? b.codePointAt(i) : -1;
    if (ca !== cb) return ca < cb ? -1 : 1;
    if (ca > 0xffff) i += 1;
  }
  return 0;
}

/**
 * Strict JSON parser that rejects duplicate object keys, NaN/Infinity literals,
 * and trailing content. Numbers are only accepted as JSON numbers (finite).
 */
export function parseStrictJson(text) {
  if (typeof text !== "string") throw new Error("JSON text must be a string");
  let i = 0;
  const end = text.length;

  const peek = () => text[i];
  const next = () => text[i++];
  const fail = message => {
    throw new Error(`${message} at ${i}`);
  };

  const skipWs = () => {
    while (i < end && (text[i] === " " || text[i] === "\t" || text[i] === "\n" || text[i] === "\r")) i += 1;
  };

  const parseString = () => {
    if (next() !== "\"") fail("expected string");
    let out = "";
    while (i < end) {
      const ch = next();
      if (ch === "\"") return out;
      if (ch === "\\") {
        const esc = next();
        switch (esc) {
          case "\"": case "\\": case "/": out += esc; break;
          case "b": out += "\b"; break;
          case "f": out += "\f"; break;
          case "n": out += "\n"; break;
          case "r": out += "\r"; break;
          case "t": out += "\t"; break;
          case "u": {
            const hex = text.slice(i, i + 4);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) fail("invalid unicode escape");
            out += String.fromCharCode(parseInt(hex, 16));
            i += 4;
            break;
          }
          default: fail("invalid escape");
        }
      } else {
        if (ch.charCodeAt(0) < 0x20) fail("unescaped control character");
        out += ch;
      }
    }
    fail("unterminated string");
  };

  const parseNumber = () => {
    const start = i;
    if (peek() === "-") i += 1;
    if (peek() === "0") {
      i += 1;
      if (peek() && /[0-9]/.test(peek())) fail("leading zeros are not allowed");
    } else {
      if (!/[1-9]/.test(peek() || "")) fail("invalid number");
      while (peek() && /[0-9]/.test(peek())) i += 1;
    }
    if (peek() === ".") {
      i += 1;
      if (!/[0-9]/.test(peek() || "")) fail("invalid fraction");
      while (peek() && /[0-9]/.test(peek())) i += 1;
    }
    if (peek() === "e" || peek() === "E") {
      i += 1;
      if (peek() === "+" || peek() === "-") i += 1;
      if (!/[0-9]/.test(peek() || "")) fail("invalid exponent");
      while (peek() && /[0-9]/.test(peek())) i += 1;
    }
    const raw = text.slice(start, i);
    if (/nan|inf/i.test(raw)) fail("NaN/Infinity are not allowed");
    const value = Number(raw);
    if (!Number.isFinite(value)) fail("non-finite number");
    return value;
  };

  const parseValue = () => {
    skipWs();
    const ch = peek();
    if (ch === "\"") return parseString();
    if (ch === "{") return parseObject();
    if (ch === "[") return parseArray();
    if (ch === "t") {
      if (text.slice(i, i + 4) !== "true") fail("expected true");
      i += 4;
      return true;
    }
    if (ch === "f") {
      if (text.slice(i, i + 5) !== "false") fail("expected false");
      i += 5;
      return false;
    }
    if (ch === "n") {
      if (text.slice(i, i + 4) !== "null") fail("expected null");
      i += 4;
      return null;
    }
    if (ch === "-" || /[0-9]/.test(ch || "")) return parseNumber();
    fail("unexpected token");
  };

  const parseArray = () => {
    next();
    skipWs();
    const arr = [];
    if (peek() === "]") {
      next();
      return arr;
    }
    while (true) {
      arr.push(parseValue());
      skipWs();
      if (peek() === ",") {
        next();
        continue;
      }
      if (peek() === "]") {
        next();
        return arr;
      }
      fail("expected ',' or ']'");
    }
  };

  const parseObject = () => {
    next();
    skipWs();
    const obj = Object.create(null);
    const seen = new Set();
    if (peek() === "}") {
      next();
      return obj;
    }
    while (true) {
      skipWs();
      const key = parseString();
      if (seen.has(key)) fail(`duplicate key ${JSON.stringify(key)}`);
      seen.add(key);
      skipWs();
      if (next() !== ":") fail("expected ':'");
      obj[key] = parseValue();
      skipWs();
      if (peek() === ",") {
        next();
        continue;
      }
      if (peek() === "}") {
        next();
        return obj;
      }
      fail("expected ',' or '}'");
    }
  };

  const value = parseValue();
  skipWs();
  if (i !== end) fail("trailing content");
  return value;
}

/** Restricted JSON canonical encoding (UTF-8, sorted keys, no extra whitespace). */
export function canonicalize(value) {
  if (value === null) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new Error("NaN/Infinity are not allowed");
    if (!Number.isInteger(value)) throw new Error("non-integer JSON numbers are not allowed in protocol wire values");
    return JSON.stringify(value);
  }
  if (typeof value === "string") return JSON.stringify(value);
  if (Array.isArray(value)) {
    return `[${value.map(item => canonicalize(item)).join(",")}]`;
  }
  if (typeof value === "object") {
    const keys = Object.keys(value).sort(compareCodePoints);
    return `{${keys.map(key => `${JSON.stringify(key)}:${canonicalize(value[key])}`).join(",")}}`;
  }
  throw new Error(`unsupported JSON type: ${typeof value}`);
}

const HASH_ROOT_KEYS = new Set([
  "eventId", "factKey", "factRevision", "schemaVersion", "metricSemanticsVersion",
  "harnessId", "eventType", "occurredAt", "model", "skill", "sessionKey", "turnKey",
  "costScopeKey", "payload"
]);

function isPlainObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function assertUint64String(value, path) {
  if (typeof value !== "string" || !/^(0|[1-9][0-9]*)$/.test(value) || value.length > 20) {
    throw new Error(`${path} must be an unsigned decimal string`);
  }
}

function assertNonNegativeCount(value, path) {
  assertUint64String(value, path);
}

function omitNullsDeep(value) {
  if (Array.isArray(value)) return value.map(omitNullsDeep);
  if (!isPlainObject(value)) return value;
  const out = {};
  for (const [key, child] of Object.entries(value)) {
    if (child === undefined || child === null) continue;
    out[key] = omitNullsDeep(child);
  }
  return out;
}

function projectForHash(event) {
  if (!isPlainObject(event)) throw new Error("event must be an object");
  const projected = {};
  for (const key of Object.keys(event)) {
    if (key === "contentHash") continue;
    if (!HASH_ROOT_KEYS.has(key)) throw new Error(`unknown business field: ${key}`);
    const value = event[key];
    if (value === undefined || value === null) continue;
    if (key === "skill") {
      if (!isPlainObject(value) || typeof value.skillKey !== "string") throw new Error("skill.skillKey is required");
      const skill = { skillKey: value.skillKey };
      // publicName is intentionally excluded from content_hash
      projected.skill = skill;
      continue;
    }
    if (key === "model") {
      if (!isPlainObject(value)) throw new Error("model must be an object");
      projected.model = {
        providerId: value.providerId,
        modelId: value.modelId
      };
      continue;
    }
    if (key === "payload") {
      projected.payload = projectPayload(value);
      continue;
    }
    if (key === "factRevision" || key === "occurredAt") assertUint64String(value, key);
    projected[key] = value;
  }
  if (projected.payload == null) throw new Error("payload is required");
  return omitNullsDeep(projected);
}

function projectPayload(payload) {
  if (!isPlainObject(payload)) throw new Error("payload must be an object");
  const allowed = new Set(["usage", "cost", "code", "activity", "context", "meta"]);
  const out = {};
  for (const [key, value] of Object.entries(payload)) {
    if (!allowed.has(key)) throw new Error(`unknown business field: payload.${key}`);
    if (value === undefined || value === null) continue;
    if (key === "usage") out.usage = projectUsage(value);
    else if (key === "cost") out.cost = projectCost(value);
    else if (key === "code") out.code = projectCode(value);
    else if (key === "activity") out.activity = projectActivity(value);
    else if (key === "context") out.context = value;
    else if (key === "meta") out.meta = projectMeta(value);
  }
  return out;
}

function projectUsage(usage) {
  if (!isPlainObject(usage)) throw new Error("usage must be an object");
  const allowed = [
    "token_total", "input_context_tokens", "output_tokens", "cache_read_tokens",
    "cache_write_tokens", "reasoning_tokens", "request_count"
  ];
  const out = {};
  for (const [key, value] of Object.entries(usage)) {
    if (!allowed.includes(key)) throw new Error(`unknown business field: usage.${key}`);
    if (value === undefined || value === null) continue;
    assertNonNegativeCount(value, `usage.${key}`);
    out[key] = value;
  }
  return out;
}

function projectCost(cost) {
  if (!isPlainObject(cost)) throw new Error("cost must be an object");
  const allowed = ["units", "currency", "source", "price_basis_id", "coverage"];
  const out = {};
  for (const [key, value] of Object.entries(cost)) {
    if (!allowed.includes(key)) throw new Error(`unknown business field: cost.${key}`);
    if (value === undefined || value === null) continue;
    if (key === "units") assertNonNegativeCount(value, "cost.units");
    out[key] = value;
  }
  return out;
}

function projectCode(code) {
  if (!isPlainObject(code)) throw new Error("code must be an object");
  const allowed = ["generated", "accepted", "added", "removed", "file_touch_count"];
  const out = {};
  for (const [key, value] of Object.entries(code)) {
    if (!allowed.includes(key)) throw new Error(`unknown business field: code.${key}`);
    if (value === undefined || value === null) continue;
    assertNonNegativeCount(value, `code.${key}`);
    out[key] = value;
  }
  return out;
}

function projectActivity(activity) {
  if (!isPlainObject(activity)) throw new Error("activity must be an object");
  const allowed = ["duration_ms", "success", "trigger", "reason", "tool_category"];
  const out = {};
  for (const [key, value] of Object.entries(activity)) {
    if (!allowed.includes(key)) throw new Error(`unknown business field: activity.${key}`);
    if (value === undefined || value === null) continue;
    if (key === "duration_ms") assertNonNegativeCount(value, "activity.duration_ms");
    out[key] = value;
  }
  return out;
}

function projectMeta(meta) {
  if (!isPlainObject(meta)) throw new Error("meta must be an object");
  const allowed = ["accuracy", "time_source", "safe_tags"];
  const out = {};
  for (const [key, value] of Object.entries(meta)) {
    if (!allowed.includes(key)) throw new Error(`unknown business field: meta.${key}`);
    if (value === undefined || value === null) continue;
    out[key] = value;
  }
  return out;
}

export function contentHashInput(event) {
  return projectForHash(event);
}

export function contentHashCanonicalJson(event) {
  return canonicalize(contentHashInput(event));
}

export function computeContentHash(event) {
  const canonical = contentHashCanonicalJson(event);
  return createHash("sha256").update(canonical, "utf8").digest("base64url");
}

export function classifyAck(existingHash, candidateHash) {
  if (existingHash == null) return "accepted";
  if (existingHash === candidateHash) return "duplicate";
  return "conflict";
}

export const ACK_UPLOAD_MAPPING = {
  accepted: { upload: 3, action: "delete_upload_task" },
  duplicate: { upload: 3, action: "delete_upload_task" },
  discarded: { upload: 4, action: "finish_upload_task_not_applicable" },
  retry: { upload: 1, action: "reschedule_runnable_at" },
  blocked: { upload: 5, action: "wait_for_compat" },
  conflict: { upload: 6, action: "quarantine_stop_retry" },
  invalid: { upload: 6, action: "quarantine_stop_retry" }
};
