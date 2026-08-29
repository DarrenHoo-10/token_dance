use std::any::Any;
use std::future::Future;
use std::panic::AssertUnwindSafe;

use adapter_sdk::{AdapterError, ErrorCode};
use futures::FutureExt;

/// Run an Adapter future and map panics to `ErrorCode::AdapterPanic`.
pub async fn isolate<F, T>(future: F) -> Result<T, AdapterError>
where
    F: Future<Output = Result<T, AdapterError>>,
{
    match AssertUnwindSafe(future).catch_unwind().await {
        Ok(value) => value,
        Err(payload) => Err(AdapterError::new(
            ErrorCode::AdapterPanic,
            panic_message(&*payload),
        )),
    }
}

fn panic_message(payload: &(dyn Any + Send)) -> String {
    if let Some(message) = payload.downcast_ref::<&'static str>() {
        (*message).to_string()
    } else if let Some(message) = payload.downcast_ref::<String>() {
        message.clone()
    } else {
        "adapter panicked".to_string()
    }
}
