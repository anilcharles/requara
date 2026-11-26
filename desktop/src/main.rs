//! Speklo desktop: the enterprise client for a Speklo server, built with GPUI.
//!
//! The app holds no data of its own. People connect it to their company's
//! Speklo server (self-hosted or hosted), sign in with SSO in the browser,
//! and work with the workspace their team shares.

#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod api;
mod app;
mod auth;
mod local;
mod settings;

use gpui_kit::*;

fn main() {
    gpui_kit::application()
        .with_assets(gpui_kit::assets::Assets)
        .run(|cx| {
            gpui_kit::init(cx);
            gpui_kit::component::Theme::change(gpui_kit::component::ThemeMode::Dark, None, cx);

            let bounds = Bounds::centered(None, size(px(1280.), px(820.)), cx);
            let options = WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                window_min_size: Some(size(px(960.), px(600.))),
                titlebar: Some(TitlebarOptions {
                    title: Some("Speklo".into()),
                    ..Default::default()
                }),
                ..Default::default()
            };
            gpui_kit::open_window(options, cx, |window, cx| {
                cx.new(|cx| app::SpekloApp::new(window, cx))
            })
            .expect("failed to open the Speklo window");
            cx.activate(true);
        });
}
