package migrations

import _ "embed"

//go:embed 0001_tokenshow_server.sql
var InitialSchema string

//go:embed 0003_ingest_durability.sql
var IngestDurability string

//go:embed 0002_aggregation_safety.sql
var AggregationSafety string

//go:embed 0004_typed_event_fields.sql
var TypedEventFields string
