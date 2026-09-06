import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from scan_commits import git, log_options


class CommitSelectionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        previous = Path.cwd()
        os.chdir(self.temp.name)
        self.addCleanup(os.chdir, previous)
        git('init', '--quiet')
        git('config', 'user.name', 'Scan Test')
        git('config', 'user.email', 'test@example.invalid')
        git('config', 'commit.gpgsign', 'false')
        self.base = self.commit('baseline')
        git('update-ref', 'refs/remotes/origin/main', self.base)

    def commit(self, content):
        Path('example.txt').write_text(content)
        git('add', 'example.txt')
        git('commit', '--quiet', '-m', 'Fixture')
        return git('rev-parse', 'HEAD')

    def selected(self, before, after):
        options = log_options('push', {'before': before, 'after': after,
                                      'repository': {'default_branch': 'main'}})
        return git('log', '--format=%H', *options.split()).splitlines()

    def test_push_includes_all_new_commits_even_if_final_diff_is_empty(self):
        first = self.commit('temporary value')
        second = self.commit('baseline')
        self.assertEqual(git('diff', self.base, second), '')
        self.assertEqual(self.selected(self.base, second), [second, first])

    def test_new_branch_excludes_inherited_history(self):
        first = self.commit('first')
        second = self.commit('second')
        self.assertEqual(self.selected('0' * 40, second), [second, first])

    def test_manual_and_same_tip_creation_scan_one_commit(self):
        head = self.commit('current')
        options = log_options('workflow_dispatch', {})
        self.assertEqual(git('log', '--format=%H', *options.split()), head)
        git('update-ref', 'refs/remotes/origin/main', head)
        self.assertEqual(self.selected('0' * 40, head), [head])

    def test_force_push_scans_only_replacement_commits(self):
        old = self.commit('old branch commit')
        git('checkout', '--quiet', '--detach', self.base)
        new = self.commit('replacement')
        self.assertEqual(self.selected(old, new), [new])

    def test_deletion_skips_and_missing_commit_fails_closed(self):
        self.assertIsNone(log_options('push', {'deleted': True}))
        with self.assertRaises(subprocess.CalledProcessError):
            self.selected('f' * 40, self.base)


if __name__ == '__main__':
    unittest.main()
