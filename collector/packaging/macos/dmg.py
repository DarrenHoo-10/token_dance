"""Create and verify a drag-to-Applications disk image without Finder automation."""
import argparse
from contextlib import contextmanager
import hashlib
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile


def run(*args):
    return subprocess.run([str(arg) for arg in args], check=True, capture_output=True).stdout


def app_info(app):
    if app.is_symlink() or not app.is_dir() or app.suffix != '.app':
        raise ValueError('Expected an application bundle directory')
    with (app / 'Contents/Info.plist').open('rb') as stream:
        info = plistlib.load(stream)
    executable = info.get('CFBundleExecutable', '')
    if not executable or Path(executable).name != executable or executable in ('.', '..'):
        raise ValueError('Invalid application executable')
    if not (app / 'Contents/MacOS' / executable).is_file():
        raise ValueError('Application executable is missing')
    return info


def packaged_name(app):
    identifier = app_info(app).get('CFBundleIdentifier')
    if identifier == 'io.tokendance.desktop':
        return 'TokenDance.app'
    if identifier == 'io.tokendance.desktop.local-test':
        return 'TokenDance Test.app'
    return app.name


def bundle_digest(app):
    """Compare signed bundle contents, including symlink targets and permissions."""
    digest = hashlib.sha256()
    for entry in sorted(app.rglob('*')):
        relative = entry.relative_to(app).as_posix()
        digest.update(relative.encode() + b'\0')
        if entry.is_symlink():
            digest.update(b'L' + os.readlink(entry).encode() + b'\0')
        elif entry.is_file():
            digest.update(b'F' + str(entry.stat().st_mode & 0o777).encode() + b'\0')
            with entry.open('rb') as stream:
                for block in iter(lambda: stream.read(1024 * 1024), b''):
                    digest.update(block)
        elif entry.is_dir():
            digest.update(b'D')
        else:
            raise ValueError('Unexpected application filesystem entry')
    return digest.hexdigest()


@contextmanager
def mounted(image):
    with tempfile.TemporaryDirectory(prefix='tokendance-dmg-verify-') as temporary:
        mount = Path(temporary) / 'volume'
        mount.mkdir()
        attached = False
        try:
            run('hdiutil', 'attach', '-readonly', '-nobrowse', '-noautoopen', '-owners', 'off',
                '-mountpoint', mount, image)
            attached = True
            yield mount
        finally:
            if attached:
                run('hdiutil', 'detach', mount)


def verify_contents(app, mount):
    app_info(app)
    name = packaged_name(app)
    packaged = mount / name
    if not packaged.is_dir() or packaged.is_symlink():
        raise ValueError('DMG is missing the expected app')
    link = mount / 'Applications'
    if not link.is_symlink() or os.readlink(link) != '/Applications':
        raise ValueError('DMG must contain a link to /Applications')
    extras = {item.name for item in mount.iterdir() if not item.name.startswith('.')} - {name, 'Applications', 'Read Me.txt'}
    if extras:
        raise ValueError('Unexpected content in DMG')
    if bundle_digest(app) != bundle_digest(packaged):
        raise ValueError('DMG application differs from the signed source bundle')
    return packaged


def verify(app, image, require_staple=False):
    run('hdiutil', 'verify', image)
    if require_staple:
        run('codesign', '--verify', '--strict', image)
        run('xcrun', 'stapler', 'validate', image)
        run('spctl', '--assess', '--type', 'open', '--context', 'context:primary-signature', image)
    with mounted(image) as mount:
        packaged = verify_contents(app, mount)
        if require_staple:
            run('codesign', '--verify', '--deep', '--strict', packaged)
            run('xcrun', 'stapler', 'validate', packaged)
            run('spctl', '--assess', '--type', 'execute', packaged)


def create(app, image):
    app_info(app)
    name = packaged_name(app)
    if image.suffix != '.dmg' or image.exists():
        raise ValueError('Output must be a new .dmg path')
    image.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='tokendance-dmg-') as temporary:
        staging = Path(temporary) / 'contents'
        staging.mkdir()
        run('ditto', app, staging / name)
        (staging / 'Applications').symlink_to('/Applications', target_is_directory=True)
        first_open = ''
        if app_info(app).get('TokenDanceDistribution') == 'unnotarized':
            first_open = ('\n此版本未经过 Apple 公证。确认下载来源后，若 macOS 阻止首次打开，可在“系统设置 → 隐私与安全”中选择“仍要打开”。\n'
                'This build is not notarized by Apple. After verifying the download source, use System Settings → Privacy & Security → Open Anyway if macOS blocks first launch.\n')
        (staging / 'Read Me.txt').write_text(
            f'TokenDance\n\n将 {name} 拖入 Applications，然后从“应用程序”打开。\n'
            '更新前请退出正在运行的旧版本；更新应用无需删除本机数据。\n\n'
            f'Drag {name} to Applications, then open it from Applications.\n'
            'Quit the previous version before replacing it. Keep your application data.\n' + first_open, encoding='utf-8')
        temporary_image = Path(temporary) / 'TokenDance.dmg'
        run('hdiutil', 'create', '-format', 'UDZO', '-fs', 'HFS+', '-volname', Path(name).stem,
            '-srcfolder', staging, temporary_image)
        verify(app, temporary_image)
        # Publish only a complete verified image, preserving any existing output.
        with image.open('xb') as output, temporary_image.open('rb') as source:
            shutil.copyfileobj(source, output)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['create', 'verify'])
    parser.add_argument('app', type=Path)
    parser.add_argument('image', type=Path)
    parser.add_argument('--require-staple', action='store_true')
    parser.add_argument('--local-test', action='store_true')
    args = parser.parse_args()
    app, image = args.app.resolve(), args.image.absolute()
    info = app_info(app)
    if args.command == 'create':
        if args.local_test:
            if info.get('CFBundleIdentifier') != 'io.tokendance.desktop.local-test':
                raise ValueError('Local-test images must contain the isolated test app')
        else:
            if info.get('CFBundleIdentifier') != 'io.tokendance.desktop':
                raise ValueError('Release image requires the production bundle identifier')
            root = Path(__file__).resolve().parents[3]
            # Run the same source guard as the app build; no release branch bypass.
            subprocess.run(['node', str(root / 'collector/apps/desktop/scripts/build-source.mjs')], cwd=root, check=True)
            run('codesign', '--verify', '--deep', '--strict', app)
        create(app, image)
    else:
        verify(app, image, args.require_staple)
    print(f'DMG {args.command} verified: {image}')


if __name__ == '__main__':
    main()
