use std::sync::Mutex;

#[derive(Debug, Default)]
pub struct SafeLog {
    lines: Mutex<Vec<String>>,
}

impl SafeLog {
    pub fn record(&self, code: &str, detail: &str) {
        let safe_code = if is_safe_code(code) {
            code
        } else {
            "invalid_code"
        };
        let safe_detail = if is_safe_code(detail) {
            detail
        } else {
            "redacted"
        };
        self.lines
            .lock()
            .expect("log")
            .push(format!("{safe_code}:{safe_detail}"));
    }

    pub fn lines(&self) -> Vec<String> {
        self.lines.lock().expect("log").clone()
    }
}

fn is_safe_code(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value.bytes().all(|byte| {
            byte.is_ascii_lowercase() || byte.is_ascii_digit() || matches!(byte, b'_' | b':' | b'-')
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_structured_codes_reach_logs() {
        let log = SafeLog::default();
        log.record("decode_failed", "C:\\Users\\private\\session.jsonl");
        log.record("privacy_rejected", "authorization: bearer secret");
        assert_eq!(
            log.lines(),
            vec!["decode_failed:redacted", "privacy_rejected:redacted"]
        );
    }
}
