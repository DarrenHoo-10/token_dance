"""Package a free DMG without Apple credentials or notarization.

The app is signed with the project's own self-signed code-signing certificate
when it is installed in the build machine's keychain (see
create-self-signed-identity.sh). A certificate keeps the code's designated
requirement stable across releases, so Keychain "Always Allow" grants survive
updates; an ad-hoc signature is pinned to one binary hash.

Local non-release builds may fall back to ad-hoc with a warning. Release and
CI builds fail closed unless TOKENDANCE_SIGNING_IDENTITY=adhoc is set.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

# Loaded by path from the repo root in CI; dmg.py is a sibling script.
sys.path.insert(0, str(Path(__file__).resolve().parent))
import dmg

DEFAULT_IDENTITY = 'TokenDance Self-Signed'


def is_macho(path):
    if path.is_symlink() or not path.is_file():
        return False
    with path.open('rb') as stream:
        return stream.read(4) in (b'\xfe\xed\xfa\xce', b'\xce\xfa\xed\xfe', b'\xfe\xed\xfa\xcf', b'\xcf\xfa\xed\xfe', b'\xca\xfe\xba\xbe', b'\xbe\xba\xfe\xca')


def validate_app(info):
    if info.get('CFBundleIdentifier') != 'io.tokendance.desktop':
        raise ValueError('Release must use the production application identifier')
    if info.get('TokenDanceCredentialStore') != 'login-keychain' or info.get('TokenDanceDistribution') != 'unnotarized':
        raise ValueError('Unnotarized app must be built with the login-keychain distribution configuration')


def find_identity(name, listing):
    """SHA-1 of the valid code-signing identity called `name`, or None.

    `security find-identity -v` prints one line per keychain. After the
    certificate is trusted into the System keychain, the same SHA-1 is listed
    twice and is still one identity. Distinct hashes stay ambiguous.
    """
    hashes = [match.group(1).upper() for line in listing.splitlines()
              if (match := re.fullmatch(r'\s*\d+\)\s+([A-Fa-f0-9]{40})\s+"([^"]+)"(?:\s+\(.*\))?\s*', line))
              and match.group(2) == name]
    unique = set(hashes)
    if len(unique) > 1:
        raise ValueError(f'More than one code-signing identity is named "{name}"')
    return hashes[0] if hashes else None


def require_identity(name, listing, expected):
    """Return `expected` when every `name` line in `listing` is that SHA-1.

    Duplicate lines of the same certificate are accepted. A missing identity,
    a different SHA-1, more than one distinct SHA-1, or a malformed expected
    fingerprint all fail closed.
    """
    normalized = re.sub(r'[\s:]', '', expected or '').upper()
    if not re.fullmatch(r'[0-9A-F]{40}', normalized):
        raise ValueError('BLOCKED: expected code-signing SHA1 is not 40 hex characters')
    try:
        found = find_identity(name, listing)
    except ValueError as error:
        raise ValueError(f'BLOCKED: {error}') from error
    if found != normalized:
        raise ValueError(f'BLOCKED: find-identity -v does not list {name} with the expected SHA1')
    return found


def _flag(value):
    return str(value or '').strip().lower() in {'1', 'true', 'yes'}


def release_signing_required(environ):
    """Release and CI builds must not silently fall back to a per-build ad-hoc identity."""
    return _flag(environ.get('CI')) or _flag(environ.get('GITHUB_ACTIONS')) or environ.get('BUILD_PROFILE') == 'release'


def select_signing(environ, listing):
    """Return (name, sha1) of the identity to sign with, or None for ad-hoc.

    An explicitly requested identity must exist. The default identity is optional
    for local non-release builds, which still produce an ad-hoc package and a
    warning. Release and CI builds fail instead. TOKENDANCE_SIGNING_IDENTITY=adhoc
    (or "-") is the only ad-hoc escape hatch.
    """
    requested = environ.get('TOKENDANCE_SIGNING_IDENTITY')
    if requested in ('-', 'adhoc'):
        return None
    name = requested or DEFAULT_IDENTITY
    if name.startswith('Developer ID') or name.startswith('Apple '):
        raise ValueError('Free distribution must not be signed with an Apple-issued identity')
    sha1 = find_identity(name, listing)
    if sha1:
        return name, sha1
    if not release_signing_required(environ):
        if requested:
            raise ValueError(f'Code-signing identity "{requested}" was not found in the keychain')
        return None
    raise ValueError(
        f'BLOCKED: code-signing identity "{name}" is not installed. '
        'Release builds must not fall back to ad-hoc. '
        'Set TOKENDANCE_SIGNING_IDENTITY=adhoc only to force a one-off ad-hoc signature.'
    )


def stable_requirement(requirement, sha1):
    """The designated requirement must be bound to the certificate, not a cdhash."""
    return f'certificate leaf = H"{sha1.lower()}"' in requirement and 'cdhash' not in requirement


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('app', type=Path)
    parser.add_argument('image', type=Path)
    args = parser.parse_args()
    app, image = args.app.resolve(), args.image.absolute()
    info = dmg.app_info(app)
    validate_app(info)
    if (app / 'Contents/embedded.provisionprofile').exists():
        raise ValueError('Free distribution must not contain a paid provisioning profile')
    if image.exists() or image.suffix != '.dmg':
        raise ValueError('Output must be a new DMG')
    root = Path(__file__).resolve().parents[3]
    source = json.loads(subprocess.check_output(['node', str(root / 'collector/apps/desktop/scripts/build-source.mjs')], cwd=root))
    binary = app / 'Contents/MacOS' / info['CFBundleExecutable']
    architectures = dmg.run('lipo','-archs',binary).decode().split()
    if len(architectures) != 1 or architectures[0] not in ('arm64','x86_64'):
        raise ValueError('Expected one supported macOS architecture')
    resources = app / 'Contents/Resources'
    resources.mkdir(exist_ok=True)
    (resources / 'tokendance-build-source.json').write_text(json.dumps(source,indent=2)+'\n')
    listing = subprocess.run(['security','find-identity','-v','-p','codesigning'],check=True,capture_output=True,text=True).stdout
    signing = select_signing(os.environ, listing)
    if signing:
        authority, sha1 = signing
        identity = sha1
    else:
        authority, sha1, identity = 'adhoc', None, '-'
        print('WARNING: ad-hoc signing; Keychain approvals will not survive updates. '
              'Run collector/packaging/macos/create-self-signed-identity.sh once on this machine. '
              'Release and CI builds fail instead of falling back unless TOKENDANCE_SIGNING_IDENTITY=adhoc.',
              file=sys.stderr)
    # Neither mode is a verified Developer ID signature or notarization.
    for path in sorted((app / 'Contents').rglob('*'), key=lambda p: len(p.parts), reverse=True):
        if is_macho(path):
            dmg.run('codesign','--force','--sign',identity,path)
    dmg.run('codesign','--force','--sign',identity,app)
    dmg.run('codesign','--verify','--deep','--strict',app)
    signature = subprocess.run(['codesign','-d','--verbose=4',str(app)],check=True,capture_output=True,text=True)
    details = signature.stderr+signature.stdout
    if sha1:
        requirement = subprocess.run(['codesign','-d','-r-',str(app)],check=True,capture_output=True,text=True)
        if 'Signature=adhoc' in details or not stable_requirement(requirement.stderr+requirement.stdout, sha1):
            raise ValueError('Expected a designated requirement bound to the self-signed certificate')
    elif 'Signature=adhoc' not in details:
        raise ValueError('Expected explicit ad-hoc signing')
    dmg.create(app,image)
    def digest(path):
        with path.open('rb') as stream:
            return hashlib.file_digest(stream,'sha256').hexdigest()
    metadata = {**source,'commit':source['commitSha'],'profile':'release','version':info['CFBundleShortVersionString'],
        'architecture':architectures[0],'bundleId':info['CFBundleIdentifier'],
        'minimumSystemVersion':info.get('LSMinimumSystemVersion','13.0'),
        'distribution':'unnotarized','notarized':False,'signingAuthority':'self-signed' if sha1 else 'adhoc','signingIdentity':authority if sha1 else None,
        'certificateSha1':sha1,'teamIdentifier':None,
        'credentialStore':'login-keychain','executable':binary.name,'sha256':digest(binary),
        'dmg':{'file':image.name,'size':image.stat().st_size,'sha256':digest(image)}}
    image.with_suffix('.build-info.json').write_text(json.dumps(metadata,indent=2)+'\n')
    image.with_suffix('.dmg.sha256').write_text(f"{metadata['dmg']['sha256']}  {image.name}\n")
    print(f'Unnotarized DMG ready: {image}')


if __name__ == '__main__':
    main()
