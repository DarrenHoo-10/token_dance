"""Package a free, ad-hoc signed DMG without Apple credentials or notarization."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import dmg


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
    # Ad-hoc signing is free and keeps Mach-O/bundle integrity on Apple Silicon.
    # It is explicitly NOT a verified Developer ID signature or notarization.
    for path in sorted((app / 'Contents').rglob('*'), key=lambda p: len(p.parts), reverse=True):
        if is_macho(path):
            dmg.run('codesign','--force','--sign','-',path)
    dmg.run('codesign','--force','--sign','-',app)
    dmg.run('codesign','--verify','--deep','--strict',app)
    signature = subprocess.run(['codesign','-d','--verbose=4',str(app)],check=True,capture_output=True,text=True)
    if 'Signature=adhoc' not in signature.stderr+signature.stdout:
        raise ValueError('Expected explicit ad-hoc signing')
    dmg.create(app,image)
    def digest(path):
        with path.open('rb') as stream:
            return hashlib.file_digest(stream,'sha256').hexdigest()
    metadata = {**source,'commit':source['commitSha'],'profile':'release','version':info['CFBundleShortVersionString'],
        'architecture':architectures[0],'bundleId':info['CFBundleIdentifier'],
        'minimumSystemVersion':info.get('LSMinimumSystemVersion','13.0'),
        'distribution':'unnotarized','notarized':False,'signingAuthority':'adhoc','teamIdentifier':None,
        'credentialStore':'login-keychain','executable':binary.name,'sha256':digest(binary),
        'dmg':{'file':image.name,'size':image.stat().st_size,'sha256':digest(image)}}
    image.with_suffix('.build-info.json').write_text(json.dumps(metadata,indent=2)+'\n')
    image.with_suffix('.dmg.sha256').write_text(f"{metadata['dmg']['sha256']}  {image.name}\n")
    print(f'Unnotarized DMG ready: {image}')


if __name__ == '__main__':
    main()
