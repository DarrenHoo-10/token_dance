"""Run the debug-only recovery UI without opening production storage or Keychain."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('executable', type=Path)
    parser.add_argument('--flow', action='store_true', help='Exercise cancel, retry and success with a fake OS result')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='tokendance-recovery-fixture-') as fixture, tempfile.TemporaryFile(mode='w+b') as output:
        process = subprocess.Popen(
            [str(args.executable.resolve()), '--local-test', '--smoke-startup-error']
            + (['--smoke-recovery-flow'] if args.flow else []),
            stdout=output, stderr=subprocess.STDOUT,
            env={**os.environ, "TOKENDANCE_RECOVERY_SMOKE_DIR": fixture},
        )
        try:
            deadline = time.monotonic() + 25
            while time.monotonic() < deadline:
                output.seek(0)
                log = output.read().decode('utf-8', errors='replace')
                if process.poll() is not None:
                    raise RuntimeError(f'Recovery UI exited ({process.returncode}):\n{log}')
                marker = 'TOKENDANCE_RECOVERY_FLOW_PASSED' if args.flow else 'TOKENDANCE_STARTUP_ERROR_READY'
                if marker in log and (not args.flow or "TOKENDANCE_DESKTOP_READY" in log):
                    time.sleep(1)
                    if process.poll() is not None:
                        raise RuntimeError('Recovery UI crashed after loading')
                    print('Recovery authorization flow passed in the same process.' if args.flow
                          else 'Recovery HTML loaded and the native app remained alive.')
                    return
                time.sleep(0.1)
            raise RuntimeError(f'Recovery UI did not finish loading:\n{log}')
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


if __name__ == '__main__':
    main()
