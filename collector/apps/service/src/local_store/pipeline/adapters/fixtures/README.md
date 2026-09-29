`doubao-activity.bin` is a synthetic LevelDB WAL containing one write batch with
one V8-serialized object. Its `content` is JSON for a local-work user message
(`fixture-message`, `fixture-session`) with one `fixture-skill` observation.
It contains no real conversation or account data and no Token consumption.
The layout regression copies this same record into both platform cache names
and two files to verify discovery, decoding and message deduplication.
