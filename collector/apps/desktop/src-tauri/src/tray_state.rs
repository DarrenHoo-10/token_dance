use std::sync::{atomic::{AtomicBool, Ordering}, Mutex};
use tauri::{menu::MenuItem, AppHandle, Manager, WebviewWindow};

pub struct TrayMenuState {
    english: AtomicBool,
    items: [MenuItem<tauri::Wry>; 5],
    last: Mutex<Option<(bool, bool, bool)>>,
}

pub fn saved_english() -> bool {
    std::fs::read_to_string(crate::state::app_data_root().join("ui-language.txt"))
        .is_ok_and(|value| value.trim()=="en")
}

pub fn menu_labels(english: bool, orb_enabled: bool, paused: bool) -> [&'static str;5] {
    if english {
        ["Open Settings", if orb_enabled {"Hide floating orb"} else {"Show floating orb"},
         "Open usage window", if paused {"Resume Collection"} else {"Pause Collection"}, "Quit TokenDance"]
    } else {
        ["打开 TokenDance 设置", if orb_enabled {"隐藏悬浮球"} else {"显示悬浮球"},
         "打开用量窗口", if paused {"恢复数据采集"} else {"暂停数据采集"}, "退出程序"]
    }
}

impl TrayMenuState {
    pub fn new(english: bool, items: [MenuItem<tauri::Wry>;5]) -> Self {
        Self { english: AtomicBool::new(english), items, last: Mutex::new(None) }
    }

    fn apply(&self, app: &AppHandle, enabled: bool, paused: bool) -> Result<(), String> {
        let english = self.english.load(Ordering::Relaxed);
        let next = (english, enabled, paused);
        let mut last = self.last.lock().map_err(|e| e.to_string())?;
        if *last == Some(next) { return Ok(()); }
        for (item,label) in self.items.iter().zip(menu_labels(english,enabled,paused)) {
            item.set_text(label).map_err(|e| e.to_string())?;
        }
        if let Some(tray) = app.tray_by_id("main-tray") {
            let tooltip = match (english,paused) {
                (true,true)=>"TokenDance · Paused", (true,false)=>"TokenDance · Running",
                (false,true)=>"TokenDance · 已暂停", (false,false)=>"TokenDance · 运行中",
            };
            tray.set_tooltip(Some(tooltip)).map_err(|e| e.to_string())?;
        }
        *last = Some(next);
        Ok(())
    }
}

pub async fn refresh(app: &AppHandle) -> Result<(), String> {
    let enabled = app.state::<crate::orb::controller::OrbHandle>().preferences().enabled;
    let paused = app.state::<crate::state::AppState>().is_global_paused().await;
    if let Some(menu) = app.try_state::<TrayMenuState>() { menu.apply(app,enabled,paused)?; }
    Ok(())
}

pub fn start(app: AppHandle) {
    tauri::async_runtime::spawn(async move {
        let mut interval = tokio::time::interval(std::time::Duration::from_millis(250));
        loop { interval.tick().await; let _ = refresh(&app).await; }
    });
}

#[tauri::command]
pub async fn set_tray_language(app: AppHandle, window: WebviewWindow, language: String) -> Result<(), String> {
    if !matches!(window.label(),"main"|"settings") { return Err("language changes require a primary window".into()); }
    if !matches!(language.as_str(),"zh"|"en") { return Err("unsupported language".into()); }
    let english = language=="en";
    tokio::task::spawn_blocking(move || {
        let path = crate::state::app_data_root().join("ui-language.txt");
        if std::fs::read_to_string(&path).ok().as_deref()!=Some(language.as_str()) {
            std::fs::write(path,language).map_err(|e| e.to_string())?;
        }
        Ok::<_,String>(())
    }).await.map_err(|e| e.to_string())??;
    // Primary WebViews can finish loading while the native tray is still being
    // installed. Do not silently lose their initial language in that race.
    for _ in 0..50 {
        if let Some(menu) = app.try_state::<TrayMenuState>() {
            menu.english.store(english,Ordering::Relaxed);
            return refresh(&app).await;
        }
        tokio::time::sleep(std::time::Duration::from_millis(20)).await;
    }
    Err("tray is not initialized".into())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn menu_contains_one_language_and_only_the_available_action() {
        for enabled in [false,true] { for paused in [false,true] {
            let zh = menu_labels(false,enabled,paused);
            assert_eq!(zh[1],if enabled {"隐藏悬浮球"} else {"显示悬浮球"});
            assert_eq!(zh[3],if paused {"恢复数据采集"} else {"暂停数据采集"});
            let en = menu_labels(true,enabled,paused);
            assert_eq!(en[1],if enabled {"Hide floating orb"} else {"Show floating orb"});
            assert_eq!(en[3],if paused {"Resume Collection"} else {"Pause Collection"});
            assert!(zh.iter().chain(en.iter()).all(|label| !label.contains('/')));
            assert!(en.iter().all(|label| label.is_ascii()));
        }}
    }
}
