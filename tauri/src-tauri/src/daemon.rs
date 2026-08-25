use std::sync::atomic::Ordering::SeqCst;

use crate::{AppState, ClipboardItem, ItemType, Notification, Peer};
use chrono::{DateTime, FixedOffset, Local};
use serde::{Deserialize, Serialize};
use tauri::{Emitter, Manager};
use tauri_plugin_shell::{
    process::{CommandChild, CommandEvent},
    ShellExt,
};

pub async fn spawn_daemon(app_handle: &tauri::AppHandle) -> CommandChild {
    let (mut rx, sidecar_command) = match app_handle.shell().sidecar("StateTransfer") {
        Ok(cmd) => match cmd.args(["-daemon", "-port=9092"]).spawn() {
            Ok(child) => child,
            Err(e) => panic!("Problem with spawning the sidecar: {}", e),
        },
        Err(e) => panic!("Problem with creating the sidecar: {}", e),
    };

    let app_handle = app_handle.clone();
    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(line) => match serde_json::from_slice::<IpcResponse>(&line) {
                    Ok(response) => {
                        println!("Response was {:?}", &response);
                        if let Some(peers) = &response.peers {
                            println!("The peers are {peers:?}");
                            let missing = peers.iter().filter(|dp| {
                                !app_handle
                                    .state::<AppState>()
                                    .peer_stack
                                    .lock()
                                    .unwrap()
                                    .iter()
                                    .any(|pp| pp.id == dp.id)
                            });
                            if missing.clone().count() > 0 {
                                for dp in missing {
                                    app_handle
                                        .state::<AppState>()
                                        .peer_stack
                                        .lock()
                                        .unwrap()
                                        .push(Peer {
                                            id: dp.id.clone(),
                                            address: dp.addr.clone(),
                                            active: true,
                                            name: "".to_string(),
                                        })
                                }
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
                                    Err(e) => println!("Couldn't emit list of peers event {e}"),
                                };
                            }
                        }
                    }
                    Err(_) => match serde_json::from_slice::<IpcEvent>(&line) {
                        Ok(response) => {
                            println!("Event was {:?}", &response);
                            match &response.event {
                                IpcEventTypes::PeerConnected { id, addr } => {
                                    let peer_exists = {
                                        match app_handle
                                            .state::<AppState>()
                                            .peer_stack
                                            .lock()
                                            .unwrap()
                                            .iter_mut()
                                            .find(|p| p.id == *id)
                                        {
                                            Some(peer_in_state) => {
                                                peer_in_state.active = true;
                                                true
                                            }
                                            None => false,
                                        }
                                    };
                                    if peer_exists {
                                        app_handle
                                            .state::<AppState>()
                                            .notification_stack
                                            .lock()
                                            .unwrap()
                                            .push(Notification {
                                                text: "Peer Reconnected".to_string(),
                                                r#type: crate::NotificationType::Normal,
                                                timestamp: Local::now(),
                                            });
                                        app_handle
                                            .emit(
                                                "new_notification",
                                                Notification {
                                                    text: "Peer Reconnected".to_string(),
                                                    r#type: crate::NotificationType::Normal,
                                                    timestamp: Local::now(),
                                                },
                                            )
                                            .unwrap();
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
                                    } else {
                                        println!("Peer was not in the stack");
                                        let peer = Peer {
                                            id: id.to_string(),
                                            address: addr.to_string(),
                                            name: "".to_string(),
                                            active: true,
                                        };
                                        app_handle
                                            .state::<AppState>()
                                            .peer_stack
                                            .lock()
                                            .unwrap()
                                            .push(peer.clone());
                                        match app_handle.emit("added_to_list_of_peers", peer) {
                                            Ok(_) => {
                                                println!("Emitted addition to list of peers event");
                                                app_handle
                                                    .state::<AppState>()
                                                    .notification_stack
                                                    .lock()
                                                    .unwrap()
                                                    .push(Notification {
                                                        text: "New Peer Connected".to_string(),
                                                        r#type: crate::NotificationType::Normal,
                                                        timestamp: Local::now(),
                                                    });
                                                app_handle
                                                    .emit(
                                                        "new_notification",
                                                        Notification {
                                                            text: "New Peer Connected".to_string(),
                                                            r#type: crate::NotificationType::Normal,
                                                            timestamp: Local::now(),
                                                        },
                                                    )
                                                    .unwrap();
                                            }
                                            Err(e) => {
                                                println!(
                                                "Couldn't emit addition to list of peers event {e}"
                                            );
                                                app_handle
                                                .state::<AppState>()
                                                .notification_stack
                                                .lock()
                                                .unwrap()
                                                .push(Notification {
                                                    text: "Error emiting addition to list of peers"
                                                        .to_string(),
                                                    r#type: crate::NotificationType::Error,
                                                    timestamp: Local::now(),
                                                });
                                                app_handle
                                                .emit(
                                                    "new_notification",
                                                    Notification {
                                                        text: "Error emiting addition to list of peers".to_string(),
                                                        r#type: crate::NotificationType::Normal,
                                                        timestamp: Local::now(),
                                                    },
                                                )
                                                .unwrap();
                                            }
                                        };
                                    }
                                }
                                IpcEventTypes::PeerDisconnected { peer } => {
                                    match app_handle
                                        .state::<AppState>()
                                        .peer_stack
                                        .lock()
                                        .unwrap()
                                        .iter_mut()
                                        .find(|peer_in_state| peer_in_state.id == *peer)
                                    {
                                        Some(peer) => peer.active = false,
                                        None => println!("Peer was not in the stack"),
                                    };
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
                                        Err(e) => println!("Couldn't emit list of peers event {e}"),
                                    };
                                    println!("Peer disconnected {peer}");
                                    app_handle
                                        .state::<AppState>()
                                        .notification_stack
                                        .lock()
                                        .unwrap()
                                        .push(Notification {
                                            text: "Peer Disconnected".to_string(),
                                            r#type: crate::NotificationType::Normal,
                                            timestamp: Local::now(),
                                        });
                                    app_handle
                                        .emit(
                                            "new_notification",
                                            Notification {
                                                text: "Peer Disconnected".to_string(),
                                                r#type: crate::NotificationType::Normal,
                                                timestamp: Local::now(),
                                            },
                                        )
                                        .unwrap();
                                }
                                IpcEventTypes::TextReceived { from, text } => {
                                    println!("Added new text entry to clipboard stack");
                                    app_handle
                                        .state::<AppState>()
                                        .clipboard_stack
                                        .lock()
                                        .unwrap()
                                        .push(ClipboardItem {
                                            value: text.clone(),
                                            item_type: ItemType::Text,
                                            from: from.clone(),
                                        });
                                    match app_handle.emit(
                                        "added_to_clipboard_stack",
                                        ClipboardItem {
                                            value: text.clone(),
                                            item_type: ItemType::Text,
                                            from: from.clone(),
                                        },
                                    ) {
                                        Ok(_) => {
                                            println!("Emitted addition to clipboard event (text)")
                                        }
                                        Err(e) => {
                                            println!("Couldn't emit addition clipboard event {e}")
                                        }
                                    };
                                }
                                IpcEventTypes::LinkReceived { from, link } => {
                                    println!("Added new link entry to clipboard stack");
                                    app_handle
                                        .state::<AppState>()
                                        .clipboard_stack
                                        .lock()
                                        .unwrap()
                                        .push(ClipboardItem {
                                            value: link.clone(),
                                            item_type: ItemType::Url,
                                            from: from.clone(),
                                        });
                                    match app_handle.emit(
                                        "added_to_clipboard_stack",
                                        ClipboardItem {
                                            value: link.clone(),
                                            item_type: ItemType::Url,
                                            from: from.clone(),
                                        },
                                    ) {
                                        Ok(_) => {
                                            println!("Emitted addition to clipboard event (link)")
                                        }
                                        Err(e) => {
                                            println!("Couldn't emit addition clipboard event {e}")
                                        }
                                    };
                                }
                                IpcEventTypes::FileTransferStarted { from, meta } => {
                                    println!("FileTransferStarted {:?}, {:?}", from, meta)
                                }
                                IpcEventTypes::FileProgress { id, pct, total } => {
                                    println!("FileProgress {:?}, {:?}, {:?}", id, pct, total)
                                }
                                IpcEventTypes::FileReceived { from, path, meta } => {
                                    println!("FileReceived {:?}, {:?}, {:?}", from, path, meta);
                                    println!("Added new path entry to clipboard stack");
                                    app_handle
                                        .state::<AppState>()
                                        .clipboard_stack
                                        .lock()
                                        .unwrap()
                                        .push(ClipboardItem {
                                            value: path.clone(),
                                            item_type: ItemType::FilePath,
                                            from: from.clone(),
                                        });
                                    match app_handle.emit(
                                        "added_to_clipboard_stack",
                                        ClipboardItem {
                                            value: path.clone(),
                                            item_type: ItemType::FilePath,
                                            from: from.clone(),
                                        },
                                    ) {
                                        Ok(_) => {
                                            println!("Emitted addition to clipboard event")
                                        }
                                        Err(e) => {
                                            println!("Couldn't emit addition clipboard event {e}")
                                        }
                                    };
                                }
                                IpcEventTypes::Error { error } => {
                                    println!("Err: {:?}", error)
                                }
                            }
                        }
                        Err(_) => println!("STDOUT: {}", String::from_utf8_lossy(&line)),
                    },
                },
                CommandEvent::Stderr(line) => {
                    println!("STDERR: {}", String::from_utf8_lossy(&line))
                }
                CommandEvent::Terminated(status) => {
                    println!("Process exited with code: {:?}", status.code);
                    break;
                }
                _ => {}
            }
        }
    });

    return sidecar_command;
}

#[derive(Serialize, Deserialize, Debug)]
#[serde(tag = "cmd", content = "args", rename_all = "snake_case")]
enum CommandPayload {
    ListPeers,
    SendText { target: String, text: String },
    SendLink { target: String, link: String },
    SendFile { target: String, path: String },
    Connect { addr: String },
}

#[derive(Serialize, Deserialize, Debug)]
struct IpcRequest {
    id: u64,
    #[serde(flatten)]
    payload: CommandPayload,
}

#[derive(Serialize, Deserialize, Debug)]
struct DaemonPeer {
    id: String,
    addr: String,
}

#[derive(Serialize, Deserialize, Debug)]
struct IpcResponse {
    id: u64,
    ok: bool,
    #[serde(default)]
    error: Option<String>,
    #[serde(default)]
    peers: Option<Vec<DaemonPeer>>,
}

#[derive(Serialize, Deserialize, Debug)]
struct FileMeta {
    id: String,
    name: String,
    size: u64,
    total: u8,
    mod_time: DateTime<FixedOffset>,
}

#[derive(Serialize, Deserialize, Debug)]
#[serde(tag = "event", rename_all = "snake_case")]
enum IpcEventTypes {
    PeerConnected {
        id: String,
        addr: String,
    },
    PeerDisconnected {
        peer: String,
    },
    TextReceived {
        from: String,
        text: String,
    },
    LinkReceived {
        from: String,
        link: String,
    },
    FileTransferStarted {
        from: String,
        meta: FileMeta,
    },
    FileProgress {
        id: String,
        pct: u8,
        total: u32,
    },
    FileReceived {
        from: String,
        path: String,
        meta: FileMeta,
    },
    Error {
        error: String,
    },
}

#[derive(Serialize, Deserialize, Debug)]
struct IpcEvent {
    #[serde(flatten)]
    event: IpcEventTypes,
}

#[tauri::command]
pub async fn send_text(
    target: String,
    text: String,
    state: tauri::State<'_, AppState>,
) -> Result<(), String> {
    let mut daemon = state.daemon_process.lock().unwrap();
    // build out a request
    let request: IpcRequest = IpcRequest {
        id: state.next_request_id.fetch_add(1, SeqCst),
        payload: CommandPayload::SendText {
            target: target,
            text: text,
        },
    };
    let mut json = serde_json::to_string(&request).unwrap();
    json.push('\n');
    if let Some(child) = daemon.as_mut() {
        return match child.write(json.as_bytes()) {
            Ok(_) => {
                println!("Send Text write was successful");
                Ok(())
            }
            Err(e) => Err(format!("Failed to write to daemon {e}")),
        };
    } else {
        Err("Daemon process is not running".to_string())
    }
}

#[tauri::command]
pub async fn send_link(
    target: String,
    link: String,
    state: tauri::State<'_, AppState>,
) -> Result<(), String> {
    let mut daemon = state.daemon_process.lock().unwrap();
    // build out a request
    let request: IpcRequest = IpcRequest {
        id: state.next_request_id.fetch_add(1, SeqCst),
        payload: CommandPayload::SendLink {
            target: target,
            link: link,
        },
    };
    let mut json = serde_json::to_string(&request).unwrap();
    json.push('\n');
    if let Some(child) = daemon.as_mut() {
        return match child.write(json.as_bytes()) {
            Ok(_) => {
                println!("Send Text write was successful");
                Ok(())
            }
            Err(e) => Err(format!("Failed to write to daemon {e}")),
        };
    } else {
        Err("Daemon process is not running".to_string())
    }
}

#[tauri::command]
pub async fn send_file(
    target: String,
    path: String,
    state: tauri::State<'_, AppState>,
) -> Result<(), String> {
    let mut daemon = state.daemon_process.lock().unwrap();
    // build out a request
    let request: IpcRequest = IpcRequest {
        id: state.next_request_id.fetch_add(1, SeqCst),
        payload: CommandPayload::SendFile {
            target: target,
            path: path,
        },
    };
    let mut json = serde_json::to_string(&request).unwrap();
    json.push('\n');
    if let Some(child) = daemon.as_mut() {
        return match child.write(json.as_bytes()) {
            Ok(_) => {
                println!("Send Text write was successful");
                Ok(())
            }
            Err(e) => Err(format!("Failed to write to daemon {e}")),
        };
    } else {
        Err("Daemon process is not running".to_string())
    }
}

#[tauri::command]
pub async fn connect_to_peer(
    peer_address: String,
    state: tauri::State<'_, AppState>,
) -> Result<(), String> {
    let mut daemon = state.daemon_process.lock().unwrap();
    // build out a request
    let request: IpcRequest = IpcRequest {
        id: state.next_request_id.fetch_add(1, SeqCst),
        payload: CommandPayload::Connect { addr: peer_address },
    };
    let mut json = serde_json::to_string(&request).unwrap();
    json.push('\n');
    if let Some(child) = daemon.as_mut() {
        return match child.write(json.as_bytes()) {
            Ok(_) => {
                println!("Connect write was successful");
                Ok(())
            }
            Err(e) => Err(format!("Failed to write to daemon {e}")),
        };
    } else {
        Err("Daemon process is not running".to_string())
    }
}

#[tauri::command]
pub async fn list_peers(state: tauri::State<'_, AppState>) -> Result<(), String> {
    let mut daemon = state.daemon_process.lock().unwrap();
    // build out a request
    let request: IpcRequest = IpcRequest {
        id: state.next_request_id.fetch_add(1, SeqCst),
        payload: CommandPayload::ListPeers,
    };
    let mut json = serde_json::to_string(&request).unwrap();
    json.push('\n');
    if let Some(child) = daemon.as_mut() {
        return match child.write(json.as_bytes()) {
            Ok(_) => {
                println!("List Peers write was successful");
                Ok(())
            }
            Err(e) => Err(format!("Failed to write to daemon {e}")),
        };
    } else {
        Err("Daemon process is not running".to_string())
    }
}
