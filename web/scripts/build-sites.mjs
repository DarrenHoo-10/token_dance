import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const distRoot = path.join(webRoot, 'dist');
const serverRoot = path.join(distRoot, 'server');

function run(command, args) {
  const result = spawnSync(command, args, {
    cwd: webRoot,
    stdio: 'inherit',
    shell: false,
  });

  if (result.status !== 0) {
    process.exit(result.status ?? 1);
  }
}

rmSync(distRoot, { recursive: true, force: true });
run(process.execPath, [path.join(webRoot, 'node_modules/typescript/bin/tsc'), '--noEmit']);
run(process.execPath, [path.join(webRoot, 'node_modules/vite/bin/vite.js'), 'build']);

mkdirSync(serverRoot, { recursive: true });

writeFileSync(
  path.join(serverRoot, 'index.js'),
  `export default {
  async fetch(request, env) {
    return env.ASSETS.fetch(request);
  },
};
`,
  'utf8'
);

writeFileSync(
  path.join(serverRoot, 'wrangler.json'),
  `${JSON.stringify(
    {
      name: 'tokendance',
      compatibility_date: '2026-05-15',
      compatibility_flags: ['nodejs_compat'],
      main: 'index.js',
      no_bundle: true,
      rules: [{ type: 'ESModule', globs: ['**/*.js', '**/*.mjs'] }],
      assets: {
        binding: 'ASSETS',
        directory: '../client',
        not_found_handling: 'single-page-application',
      },
      observability: { enabled: true },
    },
    null,
    2
  )}\n`,
  'utf8'
);
