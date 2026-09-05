use protocol::{PathPermission, SourceKind};
use serde::{Deserialize, Serialize};

use crate::error::{AdapterError, ErrorCode};
use crate::manifest::path_permission_covers;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "kind")]
pub enum SourceSpec {
    #[serde(rename = "otlp")]
    OtlpReceiver {
        id: String,
        bind_host: String,
        #[serde(skip_serializing_if = "Option::is_none")]
        bind_port: Option<u16>,
    },
    #[serde(rename = "jsonl_tail")]
    JsonlTail { id: String, path_template: String },
    #[serde(rename = "sqlite_snapshot")]
    SqliteSnapshot { id: String, path_template: String },
    #[serde(rename = "file_snapshot")]
    FileSnapshot { id: String, path_template: String },
    #[serde(rename = "runtime_stream")]
    RuntimeStream { id: String, stream_id: String },
    #[serde(rename = "local_http_api")]
    LocalHttpApi { id: String, url: String },
    #[serde(rename = "command_snapshot")]
    CommandSnapshot {
        id: String,
        executable_id: String,
        args: Vec<String>,
    },
    #[serde(rename = "remote_api")]
    RemoteApi { id: String, domain: String },
}

impl SourceSpec {
    pub fn id(&self) -> &str {
        match self {
            Self::OtlpReceiver { id, .. }
            | Self::JsonlTail { id, .. }
            | Self::SqliteSnapshot { id, .. }
            | Self::FileSnapshot { id, .. }
            | Self::RuntimeStream { id, .. }
            | Self::LocalHttpApi { id, .. }
            | Self::CommandSnapshot { id, .. }
            | Self::RemoteApi { id, .. } => id,
        }
    }

    pub fn kind(&self) -> SourceKind {
        match self {
            Self::OtlpReceiver { .. } => SourceKind::Otlp,
            Self::JsonlTail { .. } => SourceKind::JsonlTail,
            Self::SqliteSnapshot { .. } => SourceKind::SqliteSnapshot,
            Self::FileSnapshot { .. } => SourceKind::FileSnapshot,
            Self::RuntimeStream { .. } => SourceKind::RuntimeStream,
            Self::LocalHttpApi { .. } => SourceKind::LocalHttpApi,
            Self::CommandSnapshot { .. } => SourceKind::CommandSnapshot,
            Self::RemoteApi { .. } => SourceKind::RemoteApi,
        }
    }
}

pub fn assert_source_allowed(
    manifest: &protocol::AdapterManifest,
    spec: &SourceSpec,
) -> Result<(), AdapterError> {
    if !manifest.sources.contains(&spec.kind()) {
        return Err(AdapterError::new(
            ErrorCode::SourcePermissionDenied,
            format!(
                "source kind {:?} is not declared in manifest {}",
                spec.kind(),
                manifest.id
            ),
        ));
    }
    match spec {
        SourceSpec::OtlpReceiver { bind_host, .. } => {
            if !is_loopback(bind_host) {
                return Err(AdapterError::new(
                    ErrorCode::SourcePermissionDenied,
                    format!("OTLP bind host `{bind_host}` is not loopback"),
                ));
            }
            Ok(())
        }
        SourceSpec::JsonlTail { path_template, .. }
        | SourceSpec::SqliteSnapshot { path_template, .. }
        | SourceSpec::FileSnapshot { path_template, .. } => {
            assert_path_declared(&manifest.permissions.read_paths, path_template)
        }
        SourceSpec::RuntimeStream { .. } => Ok(()),
        SourceSpec::LocalHttpApi { url, .. } => {
            if !is_loopback_http_url(url) {
                return Err(AdapterError::new(
                    ErrorCode::SourcePermissionDenied,
                    format!("local HTTP source `{url}` is not loopback"),
                ));
            }
            Ok(())
        }
        SourceSpec::CommandSnapshot {
            executable_id,
            args,
            ..
        } => {
            if manifest
                .permissions
                .commands
                .iter()
                .any(|item| item.executable_id == *executable_id && item.args == *args)
            {
                Ok(())
            } else {
                Err(AdapterError::new(
                    ErrorCode::SourcePermissionDenied,
                    format!("command `{executable_id}` is not declared in manifest"),
                ))
            }
        }
        SourceSpec::RemoteApi { domain, .. } => {
            if manifest
                .permissions
                .network_domains
                .iter()
                .any(|item| item == domain)
            {
                Ok(())
            } else {
                Err(AdapterError::new(
                    ErrorCode::SourcePermissionDenied,
                    format!("domain `{domain}` is not declared in manifest"),
                ))
            }
        }
    }
}

pub fn assert_write_path_allowed(
    manifest: &protocol::AdapterManifest,
    path_template: &str,
) -> Result<(), AdapterError> {
    assert_path_declared(&manifest.permissions.write_paths, path_template)
}

fn assert_path_declared(allowed: &[PathPermission], requested: &str) -> Result<(), AdapterError> {
    if allowed
        .iter()
        .any(|permission| path_permission_covers(permission, requested))
    {
        Ok(())
    } else {
        Err(AdapterError::new(
            ErrorCode::SourcePermissionDenied,
            format!("path `{requested}` is outside manifest permissions"),
        ))
    }
}

fn is_loopback(host: &str) -> bool {
    matches!(host, "127.0.0.1" | "localhost" | "::1")
}

fn is_loopback_http_url(url: &str) -> bool {
    let Some(rest) = url.strip_prefix("http://") else {
        return false;
    };
    if rest.contains('@') || rest.contains('#') || rest.contains('\\') {
        return false;
    }
    let authority = rest.split('/').next().unwrap_or("");
    if authority.is_empty() {
        return false;
    }
    let host = if let Some(ipv6) = authority.strip_prefix('[') {
        let Some(close) = ipv6.find(']') else {
            return false;
        };
        let host = &ipv6[..close];
        let suffix = &ipv6[close + 1..];
        if !suffix.is_empty()
            && (!suffix.starts_with(':')
                || suffix[1..].is_empty()
                || !suffix[1..].bytes().all(|byte| byte.is_ascii_digit()))
        {
            return false;
        }
        host
    } else {
        match authority.rsplit_once(':') {
            Some((host, port)) if !host.contains(':') => {
                if port.is_empty() || !port.bytes().all(|byte| byte.is_ascii_digit()) {
                    return false;
                }
                host
            }
            _ => authority,
        }
    };
    host == "localhost"
        || host
            .parse::<std::net::IpAddr>()
            .is_ok_and(|address| address.is_loopback())
}

#[cfg(test)]
mod tests {
    use protocol::{CommandPermission, SourceKind};

    use super::*;
    use crate::manifest::sample_manifest;

    fn manifest() -> protocol::AdapterManifest {
        let mut manifest = sample_manifest("dev.tokenshow.adapter.example");
        manifest.sources = vec![
            SourceKind::JsonlTail,
            SourceKind::Otlp,
            SourceKind::CommandSnapshot,
        ];
        manifest.permissions.commands = vec![CommandPermission {
            executable_id: "example".into(),
            args: vec!["--version".into()],
        }];
        manifest
    }

    #[test]
    fn allows_declared_jsonl_and_loopback_otlp() {
        let manifest = manifest();
        assert_source_allowed(
            &manifest,
            &SourceSpec::JsonlTail {
                id: "sessions".into(),
                path_template: "${AGENT_CONFIG_HOME}/sessions/a.jsonl".into(),
            },
        )
        .unwrap();
        assert_source_allowed(
            &manifest,
            &SourceSpec::OtlpReceiver {
                id: "otlp".into(),
                bind_host: "127.0.0.1".into(),
                bind_port: None,
            },
        )
        .unwrap();
    }

    #[test]
    fn local_http_requires_exact_loopback_authority() {
        let mut manifest = manifest();
        manifest.sources.push(SourceKind::LocalHttpApi);
        for url in [
            "http://127.0.0.1:4318/v1/events",
            "http://localhost:8080/health",
            "http://[::1]:8080/health",
        ] {
            assert_source_allowed(
                &manifest,
                &SourceSpec::LocalHttpApi {
                    id: "local".into(),
                    url: url.into(),
                },
            )
            .unwrap();
        }
        for url in [
            "http://localhost.evil.example/",
            "http://localhost@evil.example/",
            "http://127.0.0.1.evil.example/",
            "http://[::1]evil.example/",
            "https://127.0.0.1/",
        ] {
            assert!(
                assert_source_allowed(
                    &manifest,
                    &SourceSpec::LocalHttpApi {
                        id: "local".into(),
                        url: url.into(),
                    },
                )
                .is_err(),
                "accepted malicious URL {url}"
            );
        }
    }

    #[test]
    fn rejects_undeclared_path_and_non_loopback() {
        let manifest = manifest();
        let err = assert_source_allowed(
            &manifest,
            &SourceSpec::JsonlTail {
                id: "other".into(),
                path_template: "${USER_HOME}/secret.jsonl".into(),
            },
        )
        .unwrap_err();
        assert_eq!(err.code, ErrorCode::SourcePermissionDenied);
        let err = assert_source_allowed(
            &manifest,
            &SourceSpec::OtlpReceiver {
                id: "otlp".into(),
                bind_host: "0.0.0.0".into(),
                bind_port: Some(4318),
            },
        )
        .unwrap_err();
        assert_eq!(err.code, ErrorCode::SourcePermissionDenied);
    }

    #[test]
    fn write_path_must_be_declared() {
        let manifest = manifest();
        assert_write_path_allowed(&manifest, "${AGENT_CONFIG_HOME}/config.json").unwrap();
        let err =
            assert_write_path_allowed(&manifest, "${AGENT_CONFIG_HOME}/other.json").unwrap_err();
        assert_eq!(err.code, ErrorCode::SourcePermissionDenied);
    }
}
