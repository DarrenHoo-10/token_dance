use std::path::Path;
use std::sync::Arc;
use std::time::Duration;

use protocol::{Architecture, OsType};
use uploader::{
    DeviceSigner, FlushReport, HttpTransport, InMemoryDeviceSigner, RegistrationClient,
    RetryPolicy, Uploader,
};
use wal_spool::{KeyProvider, OsKeyProvider};

use crate::runtime::append_log;

const COLLECTOR_VERSION: &str = env!("CARGO_PKG_VERSION");

pub struct UploadPipeline {
    uploader: Option<Uploader<HttpTransport>>,
    signer: Arc<InMemoryDeviceSigner>,
    installation_id: String,
    api_base: String,
    session_token: String,
}

impl UploadPipeline {
    pub fn new(installation_id: String) -> Result<Self, String> {
        let seed_provider = OsKeyProvider::new("io.tokendance.desktop", "collector-device-ed25519");
        let seed = seed_provider
            .data_key()
            .map_err(|error| error.to_string())?;
        let signer = Arc::new(InMemoryDeviceSigner::from_seed(seed));
        let api_base = std::env::var("TOKENDANCE_API_BASE_URL")
            .or_else(|_| std::env::var("TOKENSHOW_API_BASE_URL"))
            .unwrap_or_else(|_| "http://127.0.0.1:8080".into());
        let claimed = std::env::var("TOKENDANCE_DEVICE_CLAIMED")
            .is_ok_and(|value| value.eq_ignore_ascii_case("true") || value == "1");
        let uploader = if claimed {
            let client = reqwest::Client::builder()
                .timeout(Duration::from_secs(10))
                .build()
                .map_err(|error| error.to_string())?;
            let transport = HttpTransport::new_claimed(
                api_base.clone(),
                client,
                installation_id.clone(),
                Arc::clone(&signer) as Arc<dyn DeviceSigner>,
            );
            Some(Uploader::new(installation_id.clone(), transport).with_retry(retry_policy()))
        } else {
            None
        };
        Ok(Self {
            uploader,
            signer,
            installation_id,
            api_base,
            session_token: std::env::var("TOKENSHOW_SESSION_TOKEN")
                .unwrap_or_else(|_| "local-dev-session".into()),
        })
    }

    pub async fn flush(
        &mut self,
        wal: &mut wal_spool::WalStore,
        log_root: &Path,
    ) -> Result<FlushReport, String> {
        if self.uploader.is_none() {
            match self.register().await {
                Ok(uploader) => {
                    append_log(
                        log_root,
                        &format!(
                            "registered installation {} with {}",
                            uploader.transport().installation_id(),
                            self.api_base
                        ),
                    );
                    self.uploader = Some(uploader);
                }
                Err(error) => {
                    append_log(log_root, &format!("register skipped: {error}"));
                    return Err(error);
                }
            }
        }
        let uploader = self.uploader.as_mut().expect("uploader after register");
        uploader.flush(wal).await.map_err(|error| error.to_string())
    }

    async fn register(&self) -> Result<Uploader<HttpTransport>, String> {
        let client = reqwest::Client::builder()
            .timeout(Duration::from_secs(10))
            .build()
            .map_err(|error| error.to_string())?;
        let registered =
            RegistrationClient::new(self.api_base.clone(), client, self.session_token.clone())
                .register_with_installation(
                    Arc::clone(&self.signer) as Arc<dyn DeviceSigner>,
                    OsType::Windows,
                    Architecture::X8664,
                    COLLECTOR_VERSION,
                    Some(self.installation_id.clone()),
                )
                .await
                .map_err(|error| error.to_string())?;
        Ok(Uploader::new(
            registered.registration.installation_id,
            registered.transport,
        )
        .with_retry(retry_policy()))
    }
}

fn retry_policy() -> RetryPolicy {
    RetryPolicy {
        initial: Duration::from_millis(250),
        max: Duration::from_secs(2),
        max_attempts: 3,
        jitter: false,
    }
}
