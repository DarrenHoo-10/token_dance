#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

#[tokio::main]
async fn main() {
    if let Err(error) = collector_service::runtime::run_headless().await {
        let root = collector_service::runtime::collector_data_root();
        collector_service::runtime::append_log(&root, &format!("collector fatal: {error}"));
        eprintln!("tokendance-collector: {error}");
        std::process::exit(1);
    }
}
