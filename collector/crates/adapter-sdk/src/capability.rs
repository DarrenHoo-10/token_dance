use protocol::{Accuracy, Capability, CapabilityAvailability, CapabilityStatus};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CapabilityLevel {
    Full,
    Partial,
    None,
}

#[derive(Debug, Clone, PartialEq, Default)]
pub struct CapabilityReport {
    pub adapter_id: String,
    pub adapter_version: String,
    pub capabilities: Vec<CapabilityStatus>,
}

impl CapabilityReport {
    pub fn from_declared(
        adapter_id: impl Into<String>,
        adapter_version: impl Into<String>,
        declared: &[Capability],
        available: &[Capability],
    ) -> Self {
        let capabilities = declared
            .iter()
            .copied()
            .map(|capability| {
                let is_available = available.contains(&capability);
                CapabilityStatus {
                    capability,
                    availability: if is_available {
                        CapabilityAvailability::Available
                    } else {
                        CapabilityAvailability::Unavailable
                    },
                    accuracy: if is_available {
                        Some(Accuracy::Exact)
                    } else {
                        None
                    },
                    safe_reason_code: None,
                }
            })
            .collect();
        Self {
            adapter_id: adapter_id.into(),
            adapter_version: adapter_version.into(),
            capabilities,
        }
    }

    pub fn is_available(status: &CapabilityStatus) -> bool {
        status.availability == CapabilityAvailability::Available
    }

    pub fn available(&self) -> Vec<Capability> {
        self.capabilities
            .iter()
            .filter(|item| Self::is_available(item))
            .map(|item| item.capability)
            .collect()
    }

    pub fn missing(&self) -> Vec<Capability> {
        self.capabilities
            .iter()
            .filter(|item| !Self::is_available(item))
            .map(|item| item.capability)
            .collect()
    }

    pub fn level(&self) -> CapabilityLevel {
        if self.capabilities.is_empty() {
            return CapabilityLevel::None;
        }
        let available = self
            .capabilities
            .iter()
            .filter(|item| Self::is_available(item))
            .count();
        if available == 0 {
            CapabilityLevel::None
        } else if available == self.capabilities.len() {
            CapabilityLevel::Full
        } else {
            CapabilityLevel::Partial
        }
    }
}

#[cfg(test)]
mod tests {
    use protocol::Capability;

    use super::*;

    #[test]
    fn reports_full_partial_and_none() {
        let full = CapabilityReport::from_declared(
            "adapter",
            "1.0.0",
            &[Capability::Tokens, Capability::Sessions],
            &[Capability::Tokens, Capability::Sessions],
        );
        assert_eq!(full.level(), CapabilityLevel::Full);
        let partial = CapabilityReport::from_declared(
            "adapter",
            "1.0.0",
            &[Capability::Tokens, Capability::Sessions],
            &[Capability::Sessions],
        );
        assert_eq!(partial.level(), CapabilityLevel::Partial);
        assert_eq!(partial.missing(), vec![Capability::Tokens]);
        let none = CapabilityReport::from_declared("adapter", "1.0.0", &[Capability::Tokens], &[]);
        assert_eq!(none.level(), CapabilityLevel::None);
    }
}
