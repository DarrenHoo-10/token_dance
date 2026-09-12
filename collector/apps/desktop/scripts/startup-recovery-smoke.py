"""Run the debug-only recovery UI without opening production storage or Keychain."""
import argparse
from pathlib import Path
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('executable', type=Path)
    args = parser.parse_args()
    with tempfile.TemporaryFile(mode='w+b') as output:
        process = subprocess.Popen(
            [str(args.executable.resolve()), '--local-test', '--smoke-startup-error'],
            stdout=output, stderr=subprocess.STDOUT,
        )
        try:
            deadline = time.monotonic() + 25
            while time.monotonic() < deadline:
                output.seek(0)
                log = output.read().decode('utf-8', errors='replace')
                if process.poll() is not None:
                    raise RuntimeError(f'Recovery UI exited ({process.returncode}):\n{log}')
                if 'TOKENDANCE_STARTUP_ERROR_READY' in log:
                    time.sleep(1)
                    if process.poll() is not None:
                        raise RuntimeError('Recovery UI crashed after loading')
                    print('Recovery HTML loaded and the native app remained alive.')
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
