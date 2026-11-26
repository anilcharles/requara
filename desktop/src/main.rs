//! Requara desktop: the enterprise client for a Requara server, built with GPUI.
//!
//! The app holds no data of its own. People connect it to their company's
//! Requara server (self-hosted or hosted), sign in with SSO in the browser,
//! and work with the workspace their team shares.

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod api;
mod app;
mod auth;
mod local;
mod settings;
mod time;

use gpui_kit::*;

/// Writes panics to ~/Library/Logs/Requara/crash.log (or the platform's cache
/// dir), since a windowed app has no terminal to print them to.
fn log_panics() {
    let default = std::panic::take_hook();
    std::panic::set_hook(Box::new(move |info| {
        if let Some(dir) = dirs::home_dir()
            .map(|h| h.join("Library/Logs/Requara"))
            .filter(|_| cfg!(target_os = "macos"))
            .or_else(|| dirs::cache_dir().map(|c| c.join("Requara")))
        {
            let _ = std::fs::create_dir_all(&dir);
            let entry = format!(
                "--- Requara {} ---\n{info}\n{}\n\n",
                env!("CARGO_PKG_VERSION"),
                std::backtrace::Backtrace::force_capture()
            );
            use std::io::Write as _;
            if let Ok(mut f) = std::fs::OpenOptions::new()
                .create(true)
                .append(true)
                .open(dir.join("crash.log"))
            {
                let _ = f.write_all(entry.as_bytes());
            }
        }
        default(info);
    }));
}

fn main() {
    log_panics();
    gpui_kit::application()
        .with_assets(gpui_kit::assets::Assets)
        .run(|cx| {
            gpui_kit::init(cx);
            // Light or dark with the system, like the web app; same blue as the web app.
            gpui_kit::component::Theme::sync_system_appearance(None, cx);
            app::apply_brand(cx);
            app::bind_keys(cx);

            let bounds = Bounds::centered(None, size(px(1280.), px(820.)), cx);
            let options = WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(960.), px(600.))),
                titlebar: Some(TitlebarOptions {
                    title: Some("Requara".into()),
                    ..Default::default()
                }),
                ..Default::default()
            };
            #[allow(unused_variables)]
            let (handle, view) = gpui_kit::open_window(options, cx, |window, cx| {
                cx.new(|cx| app::RequaraApp::new(window, cx))
            })
            .expect("failed to open the Requara window");
            cx.activate(true);

            #[cfg(feature = "screenshot")]
            if let Ok(path) = std::env::var("REQUARA_SHOT") {
                view.update(cx, |app, cx| app.shot_setup(cx));
                cx.spawn(async move |cx| {
                    let wait = std::env::var("REQUARA_SHOT_WAIT")
                        .ok()
                        .and_then(|s| s.parse().ok())
                        .unwrap_or(3);
                    cx.background_executor()
                        .timer(std::time::Duration::from_secs(wait))
                        .await;
                    match cx.update_window(handle, |_, window, _| window.render_to_image()) {
                        Ok(Ok(image)) => match image.save(&path) {
                            Ok(()) => eprintln!("screenshot saved to {path}"),
                            Err(e) => eprintln!("couldn't save the screenshot: {e}"),
                        },
                        Ok(Err(e)) => eprintln!("couldn't render the window: {e:#}"),
                        Err(e) => eprintln!("the window is gone: {e:#}"),
                    }
                    std::process::exit(0);
                })
                .detach();
            }
        });
}
