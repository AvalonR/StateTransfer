use chrono::{DateTime, Local};
// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
use serde::{Deserialize, Serialize};
use std::sync::atomic::AtomicU64;
use std::sync::Mutex;
use tauri::{Builder, Emitter, Manager};
use tauri_plugin_shell::process::CommandChild;

pub mod daemon;

use crate::daemon::{connect_to_peer, list_peers, send_file, send_link, send_text, spawn_daemon};

pub struct AppState {
    pub daemon_process: Mutex<Option<CommandChild>>,
    pub clipboard_stack: Mutex<Vec<ClipboardItem>>,
    pub notification_stack: Mutex<Vec<Notification>>,
    pub peer_stack: Mutex<Vec<Peer>>,
    pub next_request_id: AtomicU64,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
pub struct Peer {
    id: String,
    address: String,
    name: String,
    active: bool,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
pub enum NotificationType {
    Normal,
    Error,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
pub struct Notification {
    text: String,
    r#type: NotificationType,
    timestamp: DateTime<Local>,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
pub enum ItemType {
    Url,
    FilePath,
    Text,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
#[serde(rename_all = "camelCase")]
pub struct ClipboardItem {
    value: String,
    item_type: ItemType,
    from: String,
}

#[tauri::command]
fn get_clipboard_stack(state: tauri::State<AppState>) -> Vec<ClipboardItem> {
    let stack = state.clipboard_stack.lock().unwrap();
    stack.clone()
}

#[tauri::command]
fn get_notification_stack(state: tauri::State<AppState>) -> Vec<Notification> {
    let stack = state.notification_stack.lock().unwrap();
    stack.clone()
}

#[tauri::command]
fn get_peer_stack(app_handle: tauri::AppHandle) -> Vec<Peer> {
    let stack = app_handle
        .state::<AppState>()
        .peer_stack
        .lock()
        .unwrap()
        .clone();
    let app_handle = app_handle.clone();
    tauri::async_runtime::spawn(async move {
        let state = app_handle.state::<AppState>();
        let _ = list_peers(state).await;
    });
    stack
}

#[tauri::command]
fn change_peer_name(app_handle: tauri::AppHandle, id: String, name: String) {
    let updated: bool = {
        match app_handle
            .state::<AppState>()
            .peer_stack
            .lock()
            .unwrap()
            .iter_mut()
            .find(|peer| peer.id == id)
        {
            Some(peer) => {
                peer.name = name;
                true
            }
            None => {
                app_handle
                    .emit(
                        "new_notification",
                        Notification {
                            text: format!("Peer with ID({id}) not found"),
                            r#type: NotificationType::Error,
                            timestamp: Local::now(),
                        },
                    )
                    .unwrap();
                false
            }
        }
    };
    if updated {
        match app_handle.emit(
            "updated_list_of_peers",
            app_handle
                .state::<AppState>()
                .peer_stack
                .lock()
                .unwrap()
                .clone(),
        ) {
            Ok(_) => println!("Emitted list of peers event"),
            Err(e) => {
                println!("Couldn't emit list of peers event {e}")
            }
        };
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .invoke_handler(tauri::generate_handler![
            get_clipboard_stack,
            get_notification_stack,
            get_peer_stack,
            list_peers,
            connect_to_peer,
            send_text,
            send_link,
            send_file,
            change_peer_name,
        ])
        .setup(|app| {
            let app_handle = app.handle().clone();

            tauri::async_runtime::spawn(async move {
                app_handle.manage(AppState {
                    daemon_process: Mutex::new(Some(spawn_daemon(&app_handle).await)),
                    clipboard_stack: Mutex::new(vec![]),
                    notification_stack: Mutex::new(vec![]),
                    peer_stack: Mutex::new(vec![]),
                    next_request_id: AtomicU64::new(0),
                })
            });

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
