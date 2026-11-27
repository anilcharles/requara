//! Renders the Requara icon SVG into every file the apps need.
//!
//!     cargo run --manifest-path branding/icongen/Cargo.toml
//!
//! Writes desktop/assets/icons/ (PNGs, icon.icns, icon.ico) and the web
//! favicon PNGs. Run it again whenever branding/requara-icon.svg changes.

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

use anyhow::{Context, Result, bail};
use resvg::{tiny_skia, usvg};

fn render(svg: &usvg::Tree, size: u32) -> Result<tiny_skia::Pixmap> {
    let mut pixmap = tiny_skia::Pixmap::new(size, size).context("pixmap")?;
    let s = size as f32 / svg.size().width();
    resvg::render(svg, tiny_skia::Transform::from_scale(s, s), &mut pixmap.as_mut());
    Ok(pixmap)
}

fn load(path: &Path) -> Result<usvg::Tree> {
    let data = fs::read(path).with_context(|| format!("read {}", path.display()))?;
    Ok(usvg::Tree::from_data(&data, &usvg::Options::default())?)
}

fn main() -> Result<()> {
    let root = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../..").canonicalize()?;
    let icon = load(&root.join("branding/requara-icon.svg"))?;
    let favicon = load(&root.join("branding/requara-favicon.svg"))?;

    // Desktop: PNGs for Linux and the packager, then .icns and .ico.
    let out = root.join("desktop/assets/icons");
    fs::create_dir_all(&out)?;
    for size in [32, 64, 128, 256, 512, 1024] {
        render(&icon, size)?.save_png(out.join(format!("{size}x{size}.png")))?;
    }

    // macOS .icns via iconutil (macOS only).
    if cfg!(target_os = "macos") {
        let set = out.join("icon.iconset");
        fs::create_dir_all(&set)?;
        for size in [16, 32, 128, 256, 512] {
            render(if size <= 32 { &favicon } else { &icon }, size)?.save_png(set.join(format!("icon_{size}x{size}.png")))?;
            render(&icon, size * 2)?.save_png(set.join(format!("icon_{size}x{size}@2x.png")))?;
        }
        let status = Command::new("iconutil").arg("-c").arg("icns").arg(&set).arg("-o").arg(out.join("icon.icns")).status()?;
        if !status.success() {
            bail!("iconutil failed");
        }
        fs::remove_dir_all(&set)?;
    }

    // Windows .ico: small sizes from the favicon drawing (thicker lines).
    let mut dir = ico::IconDir::new(ico::ResourceType::Icon);
    for size in [16, 24, 32, 48, 64, 128, 256] {
        let pixmap = render(if size <= 32 { &favicon } else { &icon }, size)?;
        let image = ico::IconImage::from_rgba_data(size, size, pixmap.take());
        dir.add_entry(ico::IconDirEntry::encode(&image)?);
    }
    dir.write(fs::File::create(out.join("icon.ico"))?)?;

    // Web: PNG favicons and the touch icon next to favicon.svg.
    let web = root.join("server/web/public");
    render(&favicon, 32)?.save_png(web.join("favicon-32.png"))?;
    render(&icon, 180)?.save_png(web.join("apple-touch-icon.png"))?;
    render(&icon, 512)?.save_png(web.join("icon-512.png"))?;

    println!("icons written to {} and {}", out.display(), web.display());
    Ok(())
}
