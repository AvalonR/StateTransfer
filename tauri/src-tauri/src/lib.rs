use chrono::{DateTime, Local};
// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
use serde::{Deserialize, Serialize};
use std::sync::atomic::AtomicU64;
use std::sync::Mutex;
use tauri::{Builder, Emitter, Manager};
use tauri_plugin_shell::process::CommandChild;

pub mod daemon;

use crate::daemon::{
    connect_to_peer, daemon_info, list_peers, send_file, send_link, send_text, spawn_daemon,
    DaemonInfo,
};

pub struct AppState {
    pub daemon_process: Mutex<Option<CommandChild>>,
    pub clipboard_stack: Mutex<Vec<ClipboardItem>>,
    pub notification_stack: Mutex<Vec<Notification>>,
    pub peer_stack: Mutex<Vec<Peer>>,
    pub next_request_id: AtomicU64,
    pub daemon_info: Mutex<DaemonInfo>,
}

impl AppState {
    fn peers(&self) -> Vec<Peer> {
        self.peer_stack
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }
    fn add_peer(&self, peer_to_add: Peer) {
        self.peer_stack
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .push(peer_to_add);
    }
    // fn find_peer_by_id(&self, id_to_find: &String) -> std::option::Option<&mut Peer> {
    //     self.peer_stack
    //         .lock()
    //         .unwrap_or_else(|e| e.into_inner())
    //         .iter_mut()
    //         .find(|peer| &peer.id == id_to_find)
    // }
    fn clipboard(&self) -> Vec<ClipboardItem> {
        self.clipboard_stack
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }
    fn add_clipboard_item(&self, item_to_add: ClipboardItem) {
        self.clipboard_stack
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .push(item_to_add);
    }
    fn notifications(&self) -> Vec<Notification> {
        self.notification_stack
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .clone()
    }
    fn add_notification(&self, item_to_add: Notification) {
        self.notification_stack
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .push(item_to_add);
    }
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

impl Notification {
    fn new(text: impl Into<String>, kind: NotificationType) -> Self {
        Self {
            text: text.into(),
            r#type: kind,
            timestamp: Local::now(),
        }
    }
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
    state.clipboard()
}

#[tauri::command]
fn get_notification_stack(state: tauri::State<AppState>) -> Vec<Notification> {
    state.notifications()
}

#[tauri::command]
fn get_peer_stack(state: tauri::State<AppState>) -> Vec<Peer> {
    state.peers()
}

#[tauri::command]
fn get_daemon_info(state: tauri::State<AppState>) -> DaemonInfo {
    state
        .daemon_info
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .clone()
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
                    .state::<AppState>()
                    .add_notification(Notification::new(
                        "Peer with ID({id}) not found",
                        NotificationType::Error,
                    ));
                app_handle
                    .emit(
                        "new_notification",
                        Notification::new("Peer with ID({id}) not found", NotificationType::Error),
                    )
                    .unwrap();
                false
            }
        }
    };
    if updated {
        emit_peer_list(&app_handle);
    }
}

fn emit_peer_list(app_handle: &tauri::AppHandle) {
    if let Err(e) = app_handle.emit(
        "updated_list_of_peers",
        app_handle.state::<AppState>().peers(),
    ) {
        println!("Couldn't emit list of peers event {e}");
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
            get_daemon_info,
            list_peers,
            connect_to_peer,
            send_text,
            send_link,
            send_file,
            change_peer_name,
            daemon_info,
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
                    daemon_info: Mutex::new(DaemonInfo {
                        addr: "".to_string(),
                        id: "".to_string(),
                    }),
                });
                let _ = daemon_info(app_handle.state::<AppState>()).await;
            });

            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
