fn main() {
    // A release must embed its UI instead of depending on a Vite dev server.
    if std::env::var("PROFILE").as_deref() == Ok("release")
        && std::env::var_os("CARGO_FEATURE_CUSTOM_PROTOCOL").is_none()
    {
        panic!("Use npm run build:windows, or build with --features custom-protocol after building the frontend");
    }
    println!("cargo:rerun-if-changed=../dist");
    tauri_build::build()
}
