"""Scan only pushed commits; manual runs scan the selected HEAD commit."""

import json
import os
from pathlib import Path
import re
import subprocess


def git(*args):
    return subprocess.check_output(['git', *args], text=True, stderr=subprocess.DEVNULL).strip()


def checked_commit(value):
    if not isinstance(value, str) or not re.fullmatch(r'[0-9a-f]{40}', value):
        raise ValueError('Invalid commit ID')
    git('cat-file', '-e', value + '^{commit}')
    return value


def log_options(event_name, event):
    if event_name == 'workflow_dispatch':
        return '--max-count=1 --diff-merges=first-parent ' + checked_commit(git('rev-parse', 'HEAD'))
    if event_name != 'push':
        raise ValueError('Unsupported scan event')
    if event.get('deleted'):
        return None
    head = checked_commit(event.get('after'))
    before = event.get('before')
    if before == '0' * 40:
        # A newly created feature branch is compared with the default branch,
        # avoiding a scan of all inherited history. Initial/default-branch and
        # same-tip branch creation have no divergence: scan the tip commit only.
        default = event['repository']['default_branch']
        ref = 'refs/remotes/origin/' + default
        git('check-ref-format', ref)
        base = checked_commit(git('merge-base', ref, head))
        if base == head:
            return '--max-count=1 --diff-merges=first-parent ' + head
    else:
        # Also works for force pushes: scan commits reachable from after but not
        # before. Missing objects fail the check instead of silently skipping it.
        base = checked_commit(before)
    return '--diff-merges=first-parent ' + base + '..' + head


def main():
    event = json.loads(Path(os.environ['GITHUB_EVENT_PATH']).read_text(encoding='utf-8'))
    options = log_options(os.environ['GITHUB_EVENT_NAME'], event)
    if options is None:
        print('Deleted branch: no new commits to scan.')
        return 0
    return subprocess.call(['gitleaks', 'git', '.', '--log-opts=' + options,
                            '--redact=100', '--no-banner', '--log-level', 'error',
                            '--ignore-gitleaks-allow'])


if __name__ == '__main__':
    try:
        raise SystemExit(main())
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError):
        raise SystemExit('Cannot determine pushed commits; check event metadata and fetched Git objects.')
