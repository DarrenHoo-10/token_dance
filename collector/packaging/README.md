# Desktop build checks and signed releases

`cross-platform-packaging` builds Windows and macOS on collector changes and pull requests. Both builds use the checked-in desktop `Cargo.lock`, `--locked`, and the `custom-protocol` feature so the frontend is embedded in the executable.

Ordinary CI uploads artifacts explicitly named `tokendance-desktop-windows-unsigned` and `tokendance-desktop-macos-unsigned`. These builds do not claim Authenticode verification, Developer ID signing, or Apple notarization.

For a signed release, run this workflow manually with `sign_release: true`. Configure the Windows signing certificate secrets and Apple Developer ID/notarization secrets referenced in the workflow first. Missing certificates, failed signatures, missing trusted timestamps, and failed notarization remain fatal. Signed artifacts are uploaded only after verification succeeds.

After changing desktop dependencies or any referenced crate dependencies, update `collector/apps/desktop/src-tauri/Cargo.lock` as well as any affected workspace lockfile. The desktop is a separate Cargo workspace. Do not remove `--locked` to conceal stale dependencies.

On Windows, run `npm run build:windows` from `collector/apps/desktop` to produce the local portable `release/TokenDance.exe`. Close an existing copy before the final replacement step. The local release is unsigned unless separately signed using the signing scripts.
