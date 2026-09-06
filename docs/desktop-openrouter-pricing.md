# Desktop OpenRouter estimates

Desktop 0.1.7 estimates costs locally using the public OpenRouter model catalog. No usage, code, account cookies or keys are sent to OpenRouter. Prices are cached for six hours; a failed refresh keeps the previous cache and retries after five minutes. Previously computed costs keep their estimation date and matched model.

Input, output, cache reads/writes, request charges and prompt-length tiers use decimal arithmetic, rounded once to eight decimal places in USD. Codex input includes cached tokens; the cache component is subtracted before applying the normal input rate. Time-of-day promotional overrides are not applied; models with those promotions use their base reference rates. Reasoning tokens are already part of output and are not charged again. Only exact model IDs, unique slugs and the same three explicit runtime aliases as the server are matched.

The local ledger retains per-event pricing ingredients and deduplicates stable IDs. On first upgrade it reads retained encrypted WAL transactions without changing ACKs or sending them again. A desktop-only observation buffer also captures replayed normalized events before ACK deduplication, enabling richer model metadata to fill old unknown records only when their event IDs are already present in local accounting, without increasing token totals. Compacted-away usage without ingredients stays explicitly incomplete.

Recorded charges suppress estimates for the corresponding agent/session/turn. Legacy aggregate charges without retained call metadata conservatively suppress estimates on that agent/day. The UI combines recorded charges and remaining estimates, labels the total as an estimate when applicable, and shows unpriced records and incomplete history. A free model is a known zero; missing pricing is not zero.

Validation: desktop pricing/ledger/restart/provider-charge tests, desktop usage-period tests, WAL ACK/observer/history regressions, and Windows production build.
