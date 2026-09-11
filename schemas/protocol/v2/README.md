# Protocol v2（P0 契约冻结）

本目录冻结事件管线 v2 的跨端契约：

| 产物 | 说明 |
| --- | --- |
| `spec.json` | 事件 envelope、ACK、capabilities、hash 白名单 |
| `*.schema.json` | 由 `tools/generate-protocol.mjs` 生成 |
| `fixtures/golden` | 跨语言 content_hash / ACK golden |
| `fixtures/negative` | 重复键、未知字段、负计数、NaN |
| `ACK_STATUS_MAPPING.md` | ACK → `status_json.upload` 映射 |

生成与校验：

```bash
npm run generate:protocol
npm run check:protocol:v2
cargo test -p protocol   # collector/
go test ./internal/protocol/v2/   # server/
```

规范编码与 hash 实现：

- JS: `tools/protocol-v2/canonical.mjs`
- Rust: `collector/crates/protocol/src/v2`
- Go: `server/internal/protocol/v2`
- TS types: `web/src/protocol/v2`
