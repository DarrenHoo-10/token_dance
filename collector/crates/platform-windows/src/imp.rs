use std::ffi::c_void;
use std::mem::size_of;
use std::path::Path;
use std::ptr::{null, null_mut};
use std::slice;

use windows_sys::Win32::Foundation::{
    GetLastError, ERROR_FILE_NOT_FOUND, ERROR_NOT_FOUND, ERROR_SUCCESS, FILETIME,
};
use windows_sys::Win32::Security::Credentials::{
    CredDeleteW, CredFree, CredReadW, CredWriteW, CREDENTIALW, CRED_PERSIST_LOCAL_MACHINE,
    CRED_TYPE_GENERIC,
};
use windows_sys::Win32::System::Registry::{
    RegCloseKey, RegCreateKeyExW, RegDeleteValueW, RegSetValueExW, HKEY, HKEY_CURRENT_USER,
    KEY_SET_VALUE, REG_OPTION_NON_VOLATILE, REG_SZ,
};

use super::{startup_command, PlatformError, SecretRef, DEVICE_KEY_LEN, RUN_VALUE_NAME};

const RUN_KEY: &str = r"Software\Microsoft\Windows\CurrentVersion\Run";

pub fn device_key(secret: &SecretRef) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
    validate_secret_ref(secret)?;
    let target = wide(&secret.target)?;
    let mut credential: *mut CREDENTIALW = null_mut();
    let found = unsafe { CredReadW(target.as_ptr(), CRED_TYPE_GENERIC, 0, &mut credential) };
    if found != 0 {
        let result = read_key(credential);
        unsafe { CredFree(credential.cast::<c_void>()) };
        return result;
    }

    let error = unsafe { GetLastError() };
    if error != ERROR_FILE_NOT_FOUND && error != ERROR_NOT_FOUND {
        return Err(api_error("CredReadW", error));
    }

    let mut key = [0u8; DEVICE_KEY_LEN];
    getrandom::getrandom(&mut key).map_err(|_| PlatformError::Random)?;
    write_key(&target, &mut key)?;
    Ok(key)
}

pub fn delete_secret(secret: &SecretRef) -> Result<(), PlatformError> {
    validate_secret_ref(secret)?;
    let target = wide(&secret.target)?;
    let deleted = unsafe { CredDeleteW(target.as_ptr(), CRED_TYPE_GENERIC, 0) };
    if deleted != 0 {
        return Ok(());
    }
    let error = unsafe { GetLastError() };
    if error == ERROR_FILE_NOT_FOUND || error == ERROR_NOT_FOUND {
        Ok(())
    } else {
        Err(api_error("CredDeleteW", error))
    }
}

pub fn set_current_user_autostart(
    executable: &Path,
    arguments: &[String],
) -> Result<(), PlatformError> {
    let command = startup_command(executable, arguments)?;
    let key = create_run_key()?;
    let name = wide(RUN_VALUE_NAME)?;
    let value = wide(&command)?;
    let bytes = unsafe {
        slice::from_raw_parts(value.as_ptr().cast::<u8>(), value.len() * size_of::<u16>())
    };
    let status = unsafe {
        RegSetValueExW(
            key.0,
            name.as_ptr(),
            0,
            REG_SZ,
            bytes.as_ptr(),
            bytes.len() as u32,
        )
    };
    if status == ERROR_SUCCESS {
        Ok(())
    } else {
        Err(api_error("RegSetValueExW", status))
    }
}

pub fn remove_current_user_autostart() -> Result<(), PlatformError> {
    let key = create_run_key()?;
    let name = wide(RUN_VALUE_NAME)?;
    let status = unsafe { RegDeleteValueW(key.0, name.as_ptr()) };
    if status == ERROR_SUCCESS || status == ERROR_FILE_NOT_FOUND {
        Ok(())
    } else {
        Err(api_error("RegDeleteValueW", status))
    }
}

fn read_key(credential: *mut CREDENTIALW) -> Result<[u8; DEVICE_KEY_LEN], PlatformError> {
    if credential.is_null() {
        return Err(api_error("CredReadW", ERROR_FILE_NOT_FOUND));
    }
    let credential = unsafe { &*credential };
    let actual = credential.CredentialBlobSize as usize;
    if actual != DEVICE_KEY_LEN {
        return Err(PlatformError::InvalidKeyLength { actual });
    }
    let mut key = [0u8; DEVICE_KEY_LEN];
    let blob = unsafe { slice::from_raw_parts(credential.CredentialBlob, actual) };
    key.copy_from_slice(blob);
    Ok(key)
}

fn write_key(target: &[u16], key: &mut [u8; DEVICE_KEY_LEN]) -> Result<(), PlatformError> {
    let mut credential = CREDENTIALW {
        Flags: 0,
        Type: CRED_TYPE_GENERIC,
        TargetName: target.as_ptr().cast_mut(),
        Comment: null_mut(),
        LastWritten: FILETIME {
            dwLowDateTime: 0,
            dwHighDateTime: 0,
        },
        CredentialBlobSize: key.len() as u32,
        CredentialBlob: key.as_mut_ptr(),
        Persist: CRED_PERSIST_LOCAL_MACHINE,
        AttributeCount: 0,
        Attributes: null_mut(),
        TargetAlias: null_mut(),
        UserName: null_mut(),
    };
    let written = unsafe { CredWriteW(&mut credential, 0) };
    if written != 0 {
        Ok(())
    } else {
        Err(api_error("CredWriteW", unsafe { GetLastError() }))
    }
}

fn validate_secret_ref(secret: &SecretRef) -> Result<(), PlatformError> {
    if secret.provider != "windows-credential-manager"
        || secret.target.trim().is_empty()
        || secret.target.contains('\0')
    {
        Err(PlatformError::InvalidTarget)
    } else {
        Ok(())
    }
}

fn create_run_key() -> Result<OwnedKey, PlatformError> {
    let path = wide(RUN_KEY)?;
    let mut key: HKEY = null_mut();
    let status = unsafe {
        RegCreateKeyExW(
            HKEY_CURRENT_USER,
            path.as_ptr(),
            0,
            null_mut(),
            REG_OPTION_NON_VOLATILE,
            KEY_SET_VALUE,
            null(),
            &mut key,
            null_mut(),
        )
    };
    if status == ERROR_SUCCESS {
        Ok(OwnedKey(key))
    } else {
        Err(api_error("RegCreateKeyExW", status))
    }
}

struct OwnedKey(HKEY);

impl Drop for OwnedKey {
    fn drop(&mut self) {
        unsafe { RegCloseKey(self.0) };
    }
}

fn wide(value: &str) -> Result<Vec<u16>, PlatformError> {
    if value.contains('\0') {
        return Err(PlatformError::InvalidTarget);
    }
    Ok(value.encode_utf16().chain([0]).collect())
}

fn api_error(operation: &'static str, code: u32) -> PlatformError {
    PlatformError::WindowsApi { operation, code }
}
