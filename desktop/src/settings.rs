//! App settings kept between launches (the server to connect to).
//! The session token is not here: it lives in the OS keychain (see `auth`).

use std::fs;
use std::path::PathBuf;

use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct Settings {
    /// Base URL of the Speklo server, e.g. `https://speklo.company.com`.
    pub server_url: Option<String>,
}

fn path() -> Option<PathBuf> {
    Some(dirs::config_dir()?.join("Speklo").join("settings.json"))
}

impl Settings {
    pub fn load() -> Self {
        path()
            .and_then(|p| fs::read(p).ok())
            .and_then(|b| serde_json::from_slice(&b).ok())
            .unwrap_or_default()
    }

    pub fn save(&self) -> anyhow::Result<()> {
        let p = path().ok_or_else(|| anyhow::anyhow!("no config directory on this system"))?;
        if let Some(dir) = p.parent() {
            fs::create_dir_all(dir)?;
        }
        fs::write(p, serde_json::to_vec_pretty(self)?)?;
        Ok(())
    }
}

/// Turns what a person typed ("speklo.acme.com", "http://localhost:5173/")
/// into a base URL: adds https:// when no scheme is given, drops the trailing slash.
pub fn normalize_server_url(input: &str) -> Option<String> {
    let s = input.trim();
    if s.is_empty() {
        return None;
    }
    let s = if s.starts_with("http://") || s.starts_with("https://") {
        s.to_string()
    } else {
        format!("https://{s}")
    };
    let s = s.trim_end_matches('/').to_string();
    url::Url::parse(&s)
        .ok()
        .filter(|u| u.host_str().is_some_and(|h| !h.is_empty()))?;
    Some(s)
}

#[cfg(test)]
mod tests {
    use super::normalize_server_url as n;

    #[test]
    fn normalizes() {
        assert_eq!(
            n("speklo.acme.com").as_deref(),
            Some("https://speklo.acme.com")
        );
        assert_eq!(
            n(" http://localhost:5173/ ").as_deref(),
            Some("http://localhost:5173")
        );
        assert_eq!(n(""), None);
        assert_eq!(n("https://"), None);
    }
}
