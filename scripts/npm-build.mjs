#!/usr/bin/env node
// Builds the npm packages for one version from the release binaries:
//
//   node scripts/npm-build.mjs --version 0.1.0-rc.2 \
//     --darwin-arm64 dist/dotlane_darwin_arm64_v8.0/dotlane \
//     --darwin-x64 dist/dotlane_darwin_amd64_v1/dotlane \
//     --out npm/build
//
// writes <out>/{dotlane,darwin-arm64,darwin-x64}/ and an `npm pack` tarball of each, all at the
// same version (the launcher depends on exactly that version of the platform packages), plus
// <out>/dist-tag: "next" for a pre-release, "latest" otherwise. Each binary is checked to be a
// Mach-O for its package's CPU, so a mix-up cannot ship. npm/ROADMAP.md.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// npm `cpu` value → the Mach-O CPU type of a 64-bit binary for it.
export const PLATFORMS = {
  'darwin-arm64': { os: 'darwin', cpu: 'arm64', machCPU: 0x0100000c, label: 'macOS on Apple Silicon' },
  'darwin-x64': { os: 'darwin', cpu: 'x64', machCPU: 0x01000007, label: 'macOS on Intel' },
};

const SEMVER = /^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/;

function fail(msg) {
  console.error(`npm-build: ${msg}`);
  process.exit(1);
}

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 2) {
    if (!argv[i].startsWith('--') || argv[i + 1] === undefined) fail(`bad arguments near ${argv[i]}`);
    args[argv[i].slice(2)] = argv[i + 1];
  }
  return args;
}

// machCPU returns the CPU type of a thin 64-bit little-endian Mach-O file, or null.
export function machCPU(file) {
  const fd = fs.openSync(file, 'r');
  try {
    const header = Buffer.alloc(8);
    if (fs.readSync(fd, header, 0, 8, 0) < 8) return null;
    if (header.readUInt32LE(0) !== 0xfeedfacf) return null; // MH_MAGIC_64
    return header.readUInt32LE(4);
  } finally {
    fs.closeSync(fd);
  }
}

function writeJSON(file, value) {
  fs.writeFileSync(file, JSON.stringify(value, null, 2) + '\n');
}

function copyShared(dir) {
  for (const f of ['LICENSE', 'NOTICE']) fs.copyFileSync(path.join(root, f), path.join(dir, f));
}

export function build({ version, binaries, out }) {
  if (!SEMVER.test(version)) fail(`not a version: ${version}`);
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });
  const launcher = JSON.parse(fs.readFileSync(path.join(root, 'npm/dotlane/package.json'), 'utf8'));

  // Platform packages: one binary each, installed only where os and cpu match.
  for (const [key, p] of Object.entries(PLATFORMS)) {
    const bin = binaries[key];
    if (!bin || !fs.existsSync(bin)) fail(`missing binary for ${key}: ${bin}`);
    const cpu = machCPU(bin);
    if (cpu !== p.machCPU) fail(`${bin} is not a 64-bit Mach-O for ${p.cpu} (CPU type ${cpu === null ? 'none' : '0x' + cpu.toString(16)})`);
    const dir = path.join(out, key);
    fs.mkdirSync(path.join(dir, 'bin'), { recursive: true });
    fs.copyFileSync(bin, path.join(dir, 'bin/dotlane'));
    fs.chmodSync(path.join(dir, 'bin/dotlane'), 0o755);
    copyShared(dir);
    fs.writeFileSync(
      path.join(dir, 'README.md'),
      `# @dotlane/${key}\n\nThe \`dotlane\` binary for ${p.label}. Install [\`dotlane\`](https://www.npmjs.com/package/dotlane) instead: it pulls in this package on matching machines.\n`,
    );
    writeJSON(path.join(dir, 'package.json'), {
      name: `@dotlane/${key}`,
      version,
      description: `The dotlane binary for ${p.label}`,
      license: launcher.license,
      homepage: launcher.homepage,
      repository: { ...launcher.repository, directory: 'npm' },
      os: [p.os],
      cpu: [p.cpu],
      files: ['bin/dotlane', 'NOTICE'],
      preferUnplugged: true,
    });
  }

  // The launcher, depending on exactly this version of each platform package.
  const dir = path.join(out, 'dotlane');
  fs.mkdirSync(path.join(dir, 'bin'), { recursive: true });
  fs.copyFileSync(path.join(root, 'npm/dotlane/bin/dotlane.js'), path.join(dir, 'bin/dotlane.js'));
  fs.chmodSync(path.join(dir, 'bin/dotlane.js'), 0o755);
  fs.copyFileSync(path.join(root, 'npm/dotlane/README.md'), path.join(dir, 'README.md'));
  copyShared(dir);
  const pkg = { ...launcher, version, files: [...launcher.files, 'NOTICE'] };
  delete pkg.scripts;
  pkg.optionalDependencies = Object.fromEntries(Object.keys(PLATFORMS).map((k) => [`@dotlane/${k}`, version]));
  writeJSON(path.join(dir, 'package.json'), pkg);

  const tarballs = [];
  for (const name of [...Object.keys(PLATFORMS), 'dotlane']) {
    const file = execFileSync('npm', ['pack', '--silent', '--pack-destination', path.resolve(out)], { cwd: path.join(out, name), encoding: 'utf8' }).trim().split('\n').pop();
    tarballs.push(path.join(out, file));
  }
  const tag = version.includes('-') ? 'next' : 'latest';
  fs.writeFileSync(path.join(out, 'dist-tag'), tag + '\n');
  return { tarballs, tag };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const args = parseArgs(process.argv.slice(2));
  if (!args.version) fail('--version is required');
  const { tarballs, tag } = build({
    version: args.version.replace(/^v/, ''),
    binaries: { 'darwin-arm64': args['darwin-arm64'], 'darwin-x64': args['darwin-x64'] },
    out: path.resolve(args.out ?? 'npm/build'),
  });
  for (const t of tarballs) console.log(path.relative(process.cwd(), t));
  console.log(`dist-tag: ${tag}`);
}
