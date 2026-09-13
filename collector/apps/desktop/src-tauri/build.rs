use std::path::PathBuf;

fn main() {
    // A release must embed its UI instead of depending on a Vite dev server.
    if std::env::var("PROFILE").as_deref() == Ok("release")
        && std::env::var_os("CARGO_FEATURE_CUSTOM_PROTOCOL").is_none()
    {
        panic!("Release builds must enable --features custom-protocol after building the frontend (npm run build:windows or npm run build:macos)");
    }
    ensure_frontend_dist();
    println!("cargo:rerun-if-changed=../dist");
    tauri_build::build()
}

fn ensure_frontend_dist() {
    let dist = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../dist");
    let index = dist.join("index.html");
    if index.exists() {
        return;
    }
    assert_ne!(std::env::var("PROFILE").as_deref(), Ok("release"), "Build the desktop frontend before producing a release");
    std::fs::create_dir_all(&dist).expect("create stub frontend dist for compile");
    std::fs::write(
        &index,
        "<!doctype html><html><head><meta charset=\"utf-8\"><title>TokenDance</title></head><body></body></html>\n",
    )
    .expect("write stub frontend index for compile");
    println!(
        "cargo:warning=frontend dist missing; wrote a compile stub at {}",
        index.display()
    );
}
