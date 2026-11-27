//! Times from the server (RFC 3339), shown the way the web app shows them.

use jiff::{Timestamp, ZonedDateTime as Zoned, tz::TimeZone};

fn local(iso: &str) -> Option<Zoned> {
    Some(
        iso.parse::<Timestamp>()
            .ok()?
            .to_zoned_datetime(TimeZone::system()),
    )
}

/// "14:05"
pub fn clock(iso: &str, seconds: bool) -> String {
    local(iso)
        .map(|z| {
            z.strftime(if seconds { "%H:%M:%S" } else { "%H:%M" })
                .to_string()
        })
        .unwrap_or_default()
}

/// "Today", "Yesterday" or "Thursday, Nov 26".
pub fn day_label(iso: &str) -> String {
    let Some(z) = local(iso) else {
        return String::new();
    };
    let today = Zoned::now().date();
    if z.date() == today {
        "Today".into()
    } else if today.yesterday().is_ok_and(|y| y == z.date()) {
        "Yesterday".into()
    } else {
        z.strftime("%A, %b %-d").to_string()
    }
}

/// "5 minutes ago", "Yesterday", "Nov 3".
pub fn ago(iso: &str) -> String {
    let Some(z) = local(iso) else {
        return "—".into();
    };
    let secs = Zoned::now().timestamp().as_second() - z.timestamp().as_second();
    let plural = |n: i64, unit: &str| format!("{n} {unit}{} ago", if n == 1 { "" } else { "s" });
    match secs {
        s if s < 45 => "a few seconds ago".into(),
        s if s < 3600 => plural((s / 60).max(1), "minute"),
        s if s < 86_400 => plural(s / 3600, "hour"),
        s if s < 2 * 86_400 => "Yesterday".into(),
        s if s < 7 * 86_400 => plural(s / 86_400, "day"),
        _ => z.strftime("%b %-d").to_string(),
    }
}

/// 12_400 → "12K"
pub fn compact(n: i64) -> String {
    match n {
        n if n >= 1_000_000 => format!("{:.1}M", n as f64 / 1e6),
        n if n >= 10_000 => format!("{}K", n / 1000),
        n if n >= 1_000 => format!("{:.1}K", n as f64 / 1e3),
        n => n.to_string(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn formats() {
        assert_eq!(compact(950), "950");
        assert_eq!(compact(1_500), "1.5K");
        assert_eq!(compact(12_400), "12K");
        assert_eq!(compact(2_300_000), "2.3M");
        assert_eq!(ago("not a date"), "—");
        assert_eq!(day_label(&Zoned::now().timestamp().to_string()), "Today");
        assert!(ago(&Zoned::now().timestamp().to_string()).contains("seconds"));
    }
}
