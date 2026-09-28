# TokenDance V8 decoder patch

This crate retains the V8/Blink deserializer from `blob-decoder` 0.2.2 on crates.io,
licensed Apache-2.0 (see `LICENSE`). Unused decoders and dependencies are omitted.
TokenDance uses it for read-only Claude Desktop IndexedDB values.

The local patch in `src/v8_value.rs` skips V8 `kPadding` (`0x00`) before a value. Claude
Desktop's serialized Cowork records contain that tag, while upstream 0.2.2 rejects it.
The regression test is `v8_padding_before_a_value_is_ignored`. Keep this patch until a
compatible upstream release is verified against Claude Desktop's current format.
