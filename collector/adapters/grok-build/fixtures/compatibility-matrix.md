| Agent version | Adapter | OS | Source | Fingerprint | Capabilities | Result |
| --- | --- | --- | --- | --- | --- | --- |
| 1.x | 0.1.0 | Windows/macOS | OTLP | `grok_code.*` | tokens, sessions, turns, tools, skills, code (derived), cost | supported |
| 1.x | 0.1.0 | Windows/macOS | JSONL | `grok_code.*` session events | tokens, sessions, turns, tools, skills | supported |
| 1.x | 0.1.0 | Windows/macOS | JSONL | `updates.jsonl` `turn_completed.usage` | tokens | supported |
| unknown/newer | 0.1.0 | Windows/macOS | verified JSONL only | verified event names only | tokens, sessions, turns | degraded |
