// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
use tauri::{Builder, Manager};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};

pub mod daemon;

use crate::daemon::spawn_daemon;

struct AppState {
    daemon_process: CommandChild,
    clipboard_stack: Vec<String>,
}

#[tauri::command]
fn greet(name: &str) -> String {
    format!("Hello, {}! You've been greeted from Rust by AvalonR!", name)
}

#[tauri::command]
fn get_clipboard_stack(state: tauri::State<AppState>) -> Vec<String> {
    let stack = state.clipboard_stack.clone();
    stack.into()
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![greet])
        .invoke_handler(tauri::generate_handler![get_clipboard_stack])
        .setup(|app| {
            let app_handle = app.handle().clone();

            tauri::async_runtime::spawn(async move {
                app_handle.manage(AppState {
                    daemon_process: spawn_daemon(&app_handle).await,
                    clipboard_stack: vec![String::from("ok"), String::from("man")],
                })
            });

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
