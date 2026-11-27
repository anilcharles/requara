//! Signing in to a Requara server from the desktop app.
//!
//! The app never sees a password. It follows RFC 8252 (OAuth for native apps):
//!
//! 1. Listen on a random loopback port and make a PKCE verifier/challenge pair.
//! 2. Open the system browser at `<server>/auth/login?provider=…&desktop_port=…&desktop_challenge=…`.
//!    The person signs in with the provider they picked (Google, GitHub, company SSO, …).
//! 3. The server redirects the browser to `http://127.0.0.1:<port>/callback?code=…`.
//! 4. The app trades the one-time code plus the verifier for a session token
//!    (`POST /auth/desktop/token`) and keeps it in the OS keychain.

use std::io::{BufRead, BufReader, Write};
use std::net::{TcpListener, TcpStream};
use std::time::{Duration, Instant};

use anyhow::{Context as _, Result, anyhow, bail};
use base64::Engine as _;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use rand::RngCore as _;
use sha2::{Digest, Sha256};

use crate::api::Client;

const KEYCHAIN_SERVICE: &str = "Requara";
const SIGN_IN_TIMEOUT: Duration = Duration::from_secs(5 * 60);

/// Runs the whole browser sign-in and returns a session token. Blocks until
/// the person finishes in the browser or the five minutes run out.
pub fn sign_in(server: &str, provider: &str) -> Result<String> {
    let listener =
        TcpListener::bind("127.0.0.1:0").context("couldn't open a local port for sign-in")?;
    let port = listener.local_addr()?.port();

    let mut raw = [0u8; 32];
    rand::rng().fill_bytes(&mut raw);
    let verifier = URL_SAFE_NO_PAD.encode(raw);
    let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));

    let url = format!(
        "{server}/auth/login?provider={provider}&desktop_port={port}&desktop_challenge={challenge}"
    );
    // The end-to-end test plays the browser: it reads the address from a file.
    #[cfg(feature = "screenshot")]
    if let Ok(file) = std::env::var("REQUARA_SHOT_BROWSER_FILE") {
        std::fs::write(&file, &url)?;
        let code = wait_for_code(&listener)?;
        return Ok(Client::new(server, None)?.exchange(&code, &verifier)?.token);
    }
    open::that_detached(&url).with_context(|| format!("couldn't open the browser at {url}"))?;

    let code = wait_for_code(&listener)?;
    let token = Client::new(server, None)?.exchange(&code, &verifier)?.token;
    Ok(token)
}

fn wait_for_code(listener: &TcpListener) -> Result<String> {
    listener.set_nonblocking(true)?;
    let deadline = Instant::now() + SIGN_IN_TIMEOUT;
    loop {
        match listener.accept() {
            Ok((stream, _)) => {
                if let Some(result) = handle(stream)? {
                    return result;
                }
            }
            Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                if Instant::now() > deadline {
                    bail!("Sign-in timed out. Try again.");
                }
                std::thread::sleep(Duration::from_millis(150));
            }
            Err(e) => return Err(e.into()),
        }
    }
}

/// Answers one browser request. `Some` once the callback arrived (with the
/// code or the error the server sent), `None` for anything else (a favicon).
fn handle(mut stream: TcpStream) -> Result<Option<Result<String>>> {
    stream.set_nonblocking(false)?;
    stream.set_read_timeout(Some(Duration::from_secs(5)))?;
    let mut line = String::new();
    BufReader::new(&stream).read_line(&mut line)?;
    // "GET /callback?code=abc HTTP/1.1"
    let target = line.split_whitespace().nth(1).unwrap_or("/");
    let parsed = url::Url::parse(&format!("http://127.0.0.1{target}"))?;
    if parsed.path() != "/callback" {
        respond(&mut stream, "404 Not Found", "Not found")?;
        return Ok(None);
    }
    let param = |k: &str| {
        parsed
            .query_pairs()
            .find(|(name, _)| name == k)
            .map(|(_, v)| v.into_owned())
    };
    if let Some(code) = param("code") {
        respond(
            &mut stream,
            "200 OK",
            "You're signed in to Requara. You can close this tab and go back to the app.",
        )?;
        Ok(Some(Ok(code)))
    } else {
        let err = param("error").unwrap_or_else(|| "The server didn't send a sign-in code.".into());
        respond(
            &mut stream,
            "400 Bad Request",
            "Sign-in didn't finish. Go back to the app and try again.",
        )?;
        Ok(Some(Err(anyhow!(err))))
    }
}

fn respond(stream: &mut TcpStream, status: &str, message: &str) -> Result<()> {
    let body = format!(
        "<!doctype html><meta charset=utf-8><title>Requara</title>\
         <body style=\"font-family:system-ui;background:#14161F;color:#fff;display:grid;place-items:center;height:100vh;margin:0\">\
         <p style=\"font-size:18px\">{message}</p>"
    );
    write!(
        stream,
        "HTTP/1.1 {status}\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    )?;
    Ok(())
}

// ----- the session token in the OS keychain, one per server -----

fn entry(server: &str) -> Result<keyring::Entry> {
    Ok(keyring::Entry::new(KEYCHAIN_SERVICE, server)?)
}

pub fn load_token(server: &str) -> Option<String> {
    entry(server).ok()?.get_password().ok()
}

pub fn save_token(server: &str, token: &str) -> Result<()> {
    entry(server)?.set_password(token)?;
    Ok(())
}

pub fn delete_token(server: &str) {
    if let Ok(e) = entry(server) {
        let _ = e.delete_credential();
    }
}
