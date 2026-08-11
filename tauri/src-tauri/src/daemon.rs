use tauri_plugin_shell::{
    process::{CommandChild, CommandEvent},
    ShellExt,
};

pub async fn spawn_daemon(app_handle: &tauri::AppHandle) -> CommandChild {
    let (mut rx, sidecar_command) = match app_handle.shell().sidecar("StateTransfer") {
        Ok(cmd) => match cmd.arg("-trial").spawn() {
            Ok(child) => child,
            Err(e) => panic!("Problem with spawning the sidecar: {}", e),
        },
        Err(e) => panic!("Problem with creating the sidecar: {}", e),
    };

    tauri::async_runtime::spawn(async move {
        while let Some(event) = rx.recv().await {
            match event {
                CommandEvent::Stdout(line) => {
                    println!("STDOUT: {}", String::from_utf8_lossy(&line))
                }
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
