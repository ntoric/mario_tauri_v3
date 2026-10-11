mod local;
mod printer;

use local::api::{self, ApiRequest};
use local::LocalBackend;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use printer::{PrinterService, PrintJob, Device, RawPrintRequest};
use base64::{Engine as _, engine::general_purpose};

// Native Rust printer imports (no go-printer feature)
#[cfg(all(not(feature = "go-printer"), not(target_os = "windows")))]
use printer::USBPrinterService;
#[cfg(all(not(feature = "go-printer"), not(target_os = "windows")))]
use rusb::UsbContext;
#[cfg(all(not(feature = "go-printer"), target_os = "windows"))]
use printer::WindowsPrinterService;

// Go sidecar printer import
#[cfg(feature = "go-printer")]
use printer::GoPrinterService;

/// Create the platform-appropriate printer service.
fn create_printer_service() -> Box<dyn PrinterService + Send + Sync> {
    #[cfg(feature = "go-printer")]
    {
        Box::new(GoPrinterService::new())
    }
    #[cfg(all(not(feature = "go-printer"), not(target_os = "windows")))]
    {
        Box::new(USBPrinterService::new())
    }
    #[cfg(all(not(feature = "go-printer"), target_os = "windows"))]
    {
        Box::new(WindowsPrinterService::new())
    }
}

#[derive(Debug, Serialize, Deserialize)]
struct PrinterStatus {
    status: String,
    system: String,
}

#[tauri::command]
async fn get_printer_status() -> Result<PrinterStatus, String> {
    Ok(PrinterStatus {
        status: "online".to_string(),
        system: "Mario Printer Service".to_string(),
    })
}

#[tauri::command]
async fn get_printers() -> Result<Vec<Device>, String> {
    let service = create_printer_service();
    let devices = service.detect_printers()?;
    Ok(devices)
}

#[cfg(all(not(feature = "go-printer"), not(target_os = "windows")))]
#[tauri::command]
async fn debug_usb_devices() -> Result<String, String> {
    let context = rusb::Context::new()
        .map_err(|e| format!("Failed to create USB context: {}", e))?;

    let devices_iter = context.devices()
        .map_err(|e| format!("Failed to list USB devices: {}", e))?;

    let mut output = String::new();
    output.push_str("All USB Devices:\n");
    output.push_str("================\n");

    for device in devices_iter.iter() {
        let device_desc = device.device_descriptor()
            .map_err(|e| format!("Failed to get device descriptor: {}", e))?;

        output.push_str(&format!("VID: {:04x}, PID: {:04x}, Class: {:02x}\n", 
            device_desc.vendor_id(), 
            device_desc.product_id(),
            device_desc.class_code()));

        if let Ok(config_desc) = device.config_descriptor(0) {
            for interface in config_desc.interfaces() {
                for interface_desc in interface.descriptors() {
                    output.push_str(&format!("  Interface Class: {:02x}\n", interface_desc.class_code()));
                }
            }
        }
        output.push_str("\n");
    }

    Ok(output)
}

#[cfg(any(feature = "go-printer", target_os = "windows"))]
#[tauri::command]
async fn debug_usb_devices() -> Result<String, String> {
    let service = create_printer_service();
    let devices = service.detect_printers()?;

    let mut output = String::new();
    output.push_str("Available Printers:\n");
    output.push_str("===================\n");
    for dev in &devices {
        output.push_str(&format!("Name: {}, Type: {}\n", dev.name, dev.device_type));
    }
    Ok(output)
}

#[tauri::command]
async fn save_csv_file(content: String, default_filename: String) -> Result<String, String> {
    // Fallback method - try to write to Downloads folder
    let mut path = dirs::download_dir()
        .unwrap_or_else(|| std::env::current_dir().unwrap());
    path.push(&default_filename);
    
    std::fs::write(&path, content)
        .map_err(|e| format!("Failed to write file: {}", e))?;
    Ok(format!("File saved to: {}", path.display()))
}

#[tauri::command]
async fn print_job(print_data: serde_json::Value) -> Result<String, String> {
    if let Ok(job) = serde_json::from_value::<PrintJob>(print_data.clone()) {
        println!("Received PrintJob for {}, type: {}", job.printer.name.as_ref().unwrap_or(&"unknown".to_string()), job.job_type);
        
        let service = create_printer_service();
        let printer_name = job.printer.name.clone().unwrap_or_else(|| "default".to_string());
        
        let data = printer::render_print_job(&job)
            .map_err(|e| format!("Failed to render print job: {}", e))?;
        
        service.print(&printer_name, &data)
            .map_err(|e| format!("Printing failed: {}", e))?;
        
        Ok("Printed successfully".to_string())
    }
    else if let Ok(req) = serde_json::from_value::<RawPrintRequest>(print_data) {
        println!("Received raw print job for {}, length: {}", req.printer_name, req.data.len());
        
        let data = general_purpose::STANDARD
            .decode(&req.data)
            .map_err(|e| format!("Failed to decode base64 data: {}", e))?;
        
        let service = create_printer_service();
        service.print(&req.printer_name, &data)
            .map_err(|e| format!("Printing failed: {}", e))?;
        
        Ok("Printed successfully".to_string())
    } else {
        Err("Invalid request format. Expected PrintJob or RawPrintRequest JSON.".to_string())
    }
}

/// LAN server info for the desktop UI (mobile devices connect to this URL).
#[tauri::command]
async fn lan_server_info(
    state: tauri::State<'_, Arc<LocalBackend>>,
) -> Result<serde_json::Value, String> {
    Ok(local::lan_server::server_info(&state))
}

/// Return the persisted login session (token + user) so the frontend can
/// restore authentication on app reopen. Deliberately a Tauri command rather
/// than an HTTP route so LAN/mobile clients cannot read the desktop session.
#[tauri::command]
async fn get_stored_session(
    state: tauri::State<'_, Arc<LocalBackend>>,
) -> Result<serde_json::Value, String> {
    Ok(local::sync::stored_session(&state))
}

/// Manual cloud sync for the header's sync button: pushes pending changes,
/// pulls a fresh snapshot for the given store (or the currently scoped one),
/// and drains incremental events. Returns a summary or an error string the
/// UI can surface.
#[tauri::command]
async fn sync_now(
    state: tauri::State<'_, Arc<LocalBackend>>,
    app: tauri::AppHandle,
    store_id: Option<String>,
) -> Result<serde_json::Value, String> {
    local::sync::sync_now(state.inner(), &app, store_id).await
}

/// Generic API bridge — the frontend calls this instead of HTTP fetch.
/// Dispatches method+path+body to the local backend handlers backed by SQLite.
#[tauri::command]
async fn api_request(
    state: tauri::State<'_, Arc<LocalBackend>>,
    app: tauri::AppHandle,
    method: String,
    path: String,
    body: Option<serde_json::Value>,
    token: Option<String>,
) -> Result<api::ApiResponse, String> {
    let req = ApiRequest {
        method,
        path,
        body,
        token,
    };
    Ok(api::dispatch(&**state, &app, req).await)
}

/// Last-resort fatal error reporting for startup failures. With
/// `windows_subsystem = "windows"` there is no console, so we append to a
/// log file in the temp dir and show a MessageBox on Windows.
fn report_fatal_startup_error(msg: &str) {
    report_error(msg, true);
}

fn report_error(msg: &str, show_dialog: bool) {
    let log_path = std::env::temp_dir().join("mario_juicy_startup.log");
    let line = format!(
        "[{}] {msg}\n",
        chrono::Local::now().format("%Y-%m-%d %H:%M:%S")
    );
    use std::io::Write as _;
    if let Ok(mut f) = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)
    {
        let _ = f.write_all(line.as_bytes());
    }
    eprintln!("{line}");
    let _ = show_dialog;

    #[cfg(target_os = "windows")]
    if show_dialog {
        use windows_sys::Win32::UI::WindowsAndMessaging::{
            MessageBoxW, MB_ICONERROR, MB_OK,
        };
        let text = format!("{msg}\n\nLog: {}", log_path.display());
        let to_wide = |s: &str| -> Vec<u16> {
            s.encode_utf16().chain(std::iter::once(0)).collect()
        };
        let text_w = to_wide(&text);
        let caption_w = to_wide("Mario Juicy failed to start");
        unsafe {
            MessageBoxW(
                std::ptr::null_mut(),
                text_w.as_ptr(),
                caption_w.as_ptr(),
                MB_OK | MB_ICONERROR,
            );
        }
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    // Surface panics that happen before/without a window — otherwise the
    // process exits silently (no console on Windows GUI apps).
    let default_hook = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        let main = std::thread::current().name() == Some("main");
        report_error(&format!("Fatal error: {info}"), main);
        default_hook(info);
    }));

    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_updater::Builder::new().build())
        .plugin(tauri_plugin_process::init())
        .setup(|app| {
            use tauri::Manager;
            // Candidate directories for the local DB, in priority order.
            // Local data first so fresh installs avoid roaming/redirected
            // profile folders; existing DBs elsewhere are still found.
            let mut dirs: Vec<std::path::PathBuf> = Vec::new();
            let mut push_unique = |d: std::path::PathBuf| {
                if !dirs.contains(&d) {
                    dirs.push(d);
                }
            };
            if let Ok(d) = app.path().app_local_data_dir() {
                push_unique(d);
            }
            if let Ok(d) = app.path().app_data_dir() {
                push_unique(d);
            }
            if let Some(d) = dirs::data_local_dir().map(|d| d.join("com.mario.juicy")) {
                push_unique(d);
            }
            if let Some(d) = dirs::home_dir().map(|d| d.join(".mario_juicy")) {
                push_unique(d);
            }

            let backend = match LocalBackend::open_resilient(&dirs) {
                Ok(b) => Arc::new(b),
                Err(e) => {
                    report_fatal_startup_error(&e);
                    return Err(e.into());
                }
            };
            local::cleanup::start_cleanup_worker(backend.clone());
            local::cleanup::start_bill_queue_worker(backend.clone());
            local::sync::start(backend.clone(), app.handle().clone());
            app.manage(backend.clone());

            // LAN server for mobile apps on the same network + discovery responder.
            let handle = app.handle().clone();
            tauri::async_runtime::spawn(local::lan_server::serve(
                backend.clone(),
                handle,
            ));
            local::lan_server::start_discovery_responder(backend.lan_server_id.clone());
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            get_printer_status,
            get_printers,
            print_job,
            debug_usb_devices,
            save_csv_file,
            api_request,
            lan_server_info,
            get_stored_session,
            sync_now
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
