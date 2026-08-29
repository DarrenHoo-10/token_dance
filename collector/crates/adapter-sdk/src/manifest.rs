use protocol::{
    AdapterManifest, AdapterPermissions, AgentDescriptor, Capability, CommandPermission,
    PathAccess, PathPermission, PathTemplateId, Platform, SourceKind, PROTOCOL_VERSION,
};

use crate::error::AdapterError;

/// Logical path roots Adapter manifests may use. Resolved by Core, never uploaded.
pub const SUPPORTED_PATH_ROOTS: &[&str] = &[
    "USER_HOME",
    "LOCAL_APP_DATA",
    "ROAMING_APP_DATA",
    "MAC_APP_SUPPORT",
    "CODEX_HOME",
    "AGENT_CONFIG_HOME",
];

/// Manifest validation helpers implemented for the generated protocol type.
pub trait AdapterManifestExt {
    fn validate(&self) -> Result<(), AdapterError>;
}

impl AdapterManifestExt for AdapterManifest {
    fn validate(&self) -> Result<(), AdapterError> {
        validate_manifest(self)
    }
}

pub fn sample_manifest(id: impl Into<String>) -> AdapterManifest {
    AdapterManifest {
        schema: "https://schemas.tokenshow.dev/adapter-manifest/v1.json".into(),
        manifest_version: "1.0".into(),
        id: id.into(),
        name: "Example".into(),
        version: "1.0.0".into(),
        protocol_version: PROTOCOL_VERSION.into(),
        agent: AgentDescriptor {
            id: "example".into(),
            version_range: ">=1.0.0".into(),
        },
        platforms: vec![Platform::WindowsX64, Platform::MacosArm64],
        sources: vec![SourceKind::JsonlTail],
        permissions: AdapterPermissions {
            read_paths: vec![PathPermission {
                template: PathTemplateId::AgentConfigHome,
                relative_glob: "sessions/**".into(),
                access: PathAccess::Read,
            }],
            write_paths: vec![PathPermission {
                template: PathTemplateId::AgentConfigHome,
                relative_glob: "config.json".into(),
                access: PathAccess::Write,
            }],
            commands: vec![],
            network_domains: vec![],
        },
        capabilities: vec![Capability::Sessions, Capability::Tokens],
    }
}

pub fn validate_manifest(manifest: &AdapterManifest) -> Result<(), AdapterError> {
    if manifest.schema.trim().is_empty() {
        return Err(AdapterError::manifest_invalid(
            "manifest $schema must not be empty",
        ));
    }
    if manifest.manifest_version != "1.0" {
        return Err(AdapterError::manifest_invalid(format!(
            "unsupported manifestVersion `{}`",
            manifest.manifest_version
        )));
    }
    require_non_empty("id", &manifest.id)?;
    require_non_empty("name", &manifest.name)?;
    require_non_empty("version", &manifest.version)?;
    require_non_empty("protocolVersion", &manifest.protocol_version)?;
    require_non_empty("agent.id", &manifest.agent.id)?;
    require_non_empty("agent.versionRange", &manifest.agent.version_range)?;

    if !protocol_compatible(&manifest.protocol_version, PROTOCOL_VERSION) {
        return Err(AdapterError::protocol_incompatible(format!(
            "adapter protocol {} is incompatible with collector {}",
            manifest.protocol_version, PROTOCOL_VERSION
        )));
    }

    if manifest.platforms.is_empty() {
        return Err(AdapterError::manifest_invalid(
            "manifest platforms must not be empty",
        ));
    }
    if manifest.sources.is_empty() {
        return Err(AdapterError::manifest_invalid(
            "manifest sources must not be empty",
        ));
    }

    for permission in &manifest.permissions.read_paths {
        validate_path_permission(permission, PathAccess::Read)?;
    }
    for permission in &manifest.permissions.write_paths {
        validate_path_permission(permission, PathAccess::Write)?;
    }
    for command in &manifest.permissions.commands {
        validate_command(command)?;
    }
    for domain in &manifest.permissions.network_domains {
        validate_network_domain(domain)?;
    }
    Ok(())
}

pub fn protocol_compatible(adapter_protocol: &str, collector_protocol: &str) -> bool {
    match (
        protocol_major(adapter_protocol),
        protocol_major(collector_protocol),
    ) {
        (Some(adapter_major), Some(collector_major)) => adapter_major == collector_major,
        _ => false,
    }
}

pub fn path_template_id_name(id: PathTemplateId) -> &'static str {
    match id {
        PathTemplateId::UserHome => "USER_HOME",
        PathTemplateId::LocalAppData => "LOCAL_APP_DATA",
        PathTemplateId::RoamingAppData => "ROAMING_APP_DATA",
        PathTemplateId::MacAppSupport => "MAC_APP_SUPPORT",
        PathTemplateId::CodexHome => "CODEX_HOME",
        PathTemplateId::AgentConfigHome => "AGENT_CONFIG_HOME",
    }
}

pub fn format_path_permission(permission: &PathPermission) -> String {
    let root = path_template_id_name(permission.template);
    if permission.relative_glob.is_empty() {
        format!("${{{root}}}")
    } else {
        format!(
            "${{{root}}}/{}",
            permission.relative_glob.trim_start_matches('/')
        )
    }
}

pub fn path_permission_covers(allowed: &PathPermission, requested: &str) -> bool {
    path_template_covers(&format_path_permission(allowed), requested)
}

/// Returns true when `requested` is the same as `allowed` or nested under a glob.
pub fn path_template_covers(allowed: &str, requested: &str) -> bool {
    let Some(allowed) = split_template(allowed) else {
        return false;
    };
    let Some(requested) = split_template(requested) else {
        return false;
    };
    if allowed.var != requested.var {
        return false;
    }
    glob_match(&allowed.segments, &requested.segments)
}

struct TemplateParts {
    var: String,
    segments: Vec<String>,
}

fn validate_path_permission(
    permission: &PathPermission,
    expected_access: PathAccess,
) -> Result<(), AdapterError> {
    if permission.access != expected_access {
        return Err(AdapterError::manifest_invalid(format!(
            "path {} has access {:?}, expected {:?}",
            format_path_permission(permission),
            permission.access,
            expected_access
        )));
    }
    if !SUPPORTED_PATH_ROOTS.contains(&path_template_id_name(permission.template)) {
        return Err(AdapterError::manifest_invalid("unsupported path template"));
    }
    if permission
        .relative_glob
        .split(['/', '\\'])
        .any(|segment| segment == "..")
    {
        return Err(AdapterError::manifest_invalid(format!(
            "path `{}` must not contain `..`",
            format_path_permission(permission)
        )));
    }
    if expected_access == PathAccess::Read && is_unbounded_home_read(permission) {
        return Err(AdapterError::manifest_permission_denied(format!(
            "path `{}` grants whole-home read access",
            format_path_permission(permission)
        )));
    }
    Ok(())
}

fn is_unbounded_home_read(permission: &PathPermission) -> bool {
    if permission.template != PathTemplateId::UserHome {
        return false;
    }
    let glob = permission.relative_glob.trim();
    glob.is_empty() || glob == "*" || glob == "**" || glob == "/**"
}

fn validate_command(command: &CommandPermission) -> Result<(), AdapterError> {
    if command.executable_id.trim().is_empty() {
        return Err(AdapterError::manifest_invalid(
            "command executableId must not be empty",
        ));
    }
    for part in std::iter::once(command.executable_id.as_str())
        .chain(command.args.iter().map(String::as_str))
    {
        if part
            .chars()
            .any(|ch| matches!(ch, '|' | ';' | '&' | '`' | '\n' | '\r'))
            || part.contains('$')
        {
            return Err(AdapterError::manifest_invalid(format!(
                "command `{part}` must not contain shell metacharacters"
            )));
        }
    }
    Ok(())
}

fn validate_network_domain(domain: &str) -> Result<(), AdapterError> {
    if domain.trim().is_empty() {
        return Err(AdapterError::manifest_invalid(
            "network domain must not be empty",
        ));
    }
    if domain.contains('*') {
        return Err(AdapterError::manifest_permission_denied(format!(
            "network domain `{domain}` must be exact; wildcards are forbidden"
        )));
    }
    if domain.contains('/') || domain.contains(' ') || domain.contains(':') {
        return Err(AdapterError::manifest_invalid(format!(
            "network domain `{domain}` must be a hostname"
        )));
    }
    Ok(())
}

fn require_non_empty(field: &str, value: &str) -> Result<(), AdapterError> {
    if value.trim().is_empty() {
        Err(AdapterError::manifest_invalid(format!(
            "manifest {field} must not be empty"
        )))
    } else {
        Ok(())
    }
}

fn protocol_major(version: &str) -> Option<u32> {
    version.split('.').next()?.parse().ok()
}

fn split_template(template: &str) -> Option<TemplateParts> {
    let normalized = template.replace('\\', "/");
    let rest = normalized.strip_prefix("${")?;
    let close = rest.find('}')?;
    let var = rest[..close].to_string();
    if var.is_empty() || !var.chars().all(|ch| ch.is_ascii_uppercase() || ch == '_') {
        return None;
    }
    let mut path = &rest[close + 1..];
    if path.starts_with('/') {
        path = &path[1..];
    } else if !path.is_empty() {
        return None;
    }
    if path.contains("${") {
        return None;
    }
    let segments = if path.is_empty() {
        Vec::new()
    } else {
        path.split('/')
            .filter(|segment| !segment.is_empty())
            .map(ToOwned::to_owned)
            .collect()
    };
    Some(TemplateParts { var, segments })
}

fn glob_match(pattern: &[String], path: &[String]) -> bool {
    match (pattern.split_first(), path.split_first()) {
        (None, None) => true,
        (None, Some(_)) => false,
        (Some((head, rest)), _) if head == "**" => {
            glob_match(rest, path) || (!path.is_empty() && glob_match(pattern, &path[1..]))
        }
        (Some(_), None) => false,
        (Some((head, rest)), Some((seg, path_rest))) => {
            (head == "*" || head == seg) && glob_match(rest, path_rest)
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::error::ErrorCode;

    use super::*;

    #[test]
    fn accepts_supported_scoped_templates() {
        sample_manifest("dev.tokenshow.adapter.example")
            .validate()
            .unwrap();
    }

    #[test]
    fn rejects_whole_home_read() {
        let mut manifest = sample_manifest("dev.tokenshow.adapter.example");
        manifest.permissions.read_paths = vec![PathPermission {
            template: PathTemplateId::UserHome,
            relative_glob: String::new(),
            access: PathAccess::Read,
        }];
        let err = manifest.validate().unwrap_err();
        assert_eq!(err.code, ErrorCode::ManifestPermissionDenied);
        manifest.permissions.read_paths = vec![PathPermission {
            template: PathTemplateId::UserHome,
            relative_glob: "**".into(),
            access: PathAccess::Read,
        }];
        let err = manifest.validate().unwrap_err();
        assert_eq!(err.code, ErrorCode::ManifestPermissionDenied);
    }

    #[test]
    fn rejects_wildcard_network_domain() {
        let mut manifest = sample_manifest("dev.tokenshow.adapter.example");
        manifest.permissions.network_domains = vec!["*.example.com".into()];
        let err = manifest.validate().unwrap_err();
        assert_eq!(err.code, ErrorCode::ManifestPermissionDenied);
    }

    #[test]
    fn rejects_incompatible_protocol() {
        let mut manifest = sample_manifest("dev.tokenshow.adapter.example");
        manifest.protocol_version = "9.0".into();
        let err = manifest.validate().unwrap_err();
        assert_eq!(err.code, ErrorCode::ProtocolIncompatible);
    }

    #[test]
    fn path_glob_covers_nested_files() {
        assert!(path_template_covers(
            "${AGENT_CONFIG_HOME}/sessions/**",
            "${AGENT_CONFIG_HOME}/sessions/a.jsonl"
        ));
        assert!(path_template_covers(
            "${AGENT_CONFIG_HOME}/config.json",
            "${AGENT_CONFIG_HOME}/config.json"
        ));
        assert!(!path_template_covers(
            "${AGENT_CONFIG_HOME}/sessions/**",
            "${AGENT_CONFIG_HOME}/logs/a.jsonl"
        ));
    }

    #[test]
    fn protocol_major_match_is_compatible() {
        assert!(protocol_compatible("1.0", "1.2"));
        assert!(!protocol_compatible("2.0", "1.0"));
        assert!(!protocol_compatible("not-a-version", "1.0"));
    }
}
