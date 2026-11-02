#!/usr/bin/env node
// Builds the npm packages for one version from the release binaries:
//
//   node scripts/npm-build.mjs --version 0.1.0-rc.2 \
//     --darwin-arm64 dist/dotlane_darwin_arm64_v8.0/dotlane \
//     --darwin-x64 dist/dotlane_darwin_amd64_v1/dotlane \
//     --linux-arm64 dist/dotlane-linux-windows_linux_arm64_v8.0/dotlane \
//     --linux-x64 dist/dotlane-linux-windows_linux_amd64_v1/dotlane \
//     --win32-arm64 dist/dotlane-linux-windows_windows_arm64_v8.0/dotlane.exe \
//     --win32-x64 dist/dotlane-linux-windows_windows_amd64_v1/dotlane.exe \
//     --out npm/build
//
// writes <out>/{dotlane,darwin-arm64,darwin-x64,linux-arm64,linux-x64,win32-arm64,win32-x64}/
// and an `npm pack` tarball of each, all at the same version (the launcher depends on exactly
// that version of the platform packages), plus <out>/dist-tag: "next" for a pre-release,
// "latest" otherwise. Each binary is checked to be a Mach-O, ELF or PE for its package's OS and
// CPU, so a mix-up cannot ship. npm/ROADMAP.md.
//
// All six binaries are required, not "at least one": the launcher lists every platform package
// as an optional dependency at exactly this version, so a release that left one out would
// publish a launcher whose install fails or finds nothing on that platform, and a version
// cannot be re-published. A build that cannot make a platform's binary must fail, not ship less.
import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');

// Package key (`${process.platform}-${process.arch}`) → what its binary must be: `format` is the
// executable format of the OS, `machine` the CPU in that format's header (Mach-O cputype, ELF
// e_machine, PE Machine), `file` the binary's name inside the package.
export const PLATFORMS = {
  'darwin-arm64': { os: 'darwin', cpu: 'arm64', format: 'macho', machine: 0x0100000c, file: 'dotlane', label: 'macOS on Apple Silicon' },
  'darwin-x64': { os: 'darwin', cpu: 'x64', format: 'macho', machine: 0x01000007, file: 'dotlane', label: 'macOS on Intel' },
  'linux-arm64': { os: 'linux', cpu: 'arm64', format: 'elf', machine: 0xb7, file: 'dotlane', label: 'Linux on arm64' },
  'linux-x64': { os: 'linux', cpu: 'x64', format: 'elf', machine: 0x3e, file: 'dotlane', label: 'Linux on x64' },
  'win32-arm64': { os: 'win32', cpu: 'arm64', format: 'pe', machine: 0xaa64, file: 'dotlane.exe', label: 'Windows on arm64' },
  'win32-x64': { os: 'win32', cpu: 'x64', format: 'pe', machine: 0x8664, file: 'dotlane.exe', label: 'Windows on x64' },
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

function readHead(file, length) {
  const fd = fs.openSync(file, 'r');
  try {
    const buf = Buffer.alloc(length);
    const n = fs.readSync(fd, buf, 0, length, 0);
    return buf.subarray(0, n);
  } finally {
    fs.closeSync(fd);
  }
}

// machCPU returns the CPU type of a thin 64-bit little-endian Mach-O file, or null.
export function machCPU(file) {
  const header = readHead(file, 8);
  if (header.length < 8 || header.readUInt32LE(0) !== 0xfeedfacf) return null; // MH_MAGIC_64
  return header.readUInt32LE(4);
}

// elfMachine returns the e_machine of a 64-bit little-endian ELF file, or null.
export function elfMachine(file) {
  const header = readHead(file, 20);
  if (header.length < 20 || header.readUInt32BE(0) !== 0x7f454c46) return null; // "\x7fELF"
  if (header[4] !== 2 || header[5] !== 1) return null; // ELFCLASS64, little-endian
  return header.readUInt16LE(18);
}

// peMachine returns the Machine field of a PE file (a Windows executable), or null. The PE
// header is where the DOS header's e_lfanew (offset 0x3c) points, behind "MZ" at the start.
export function peMachine(file) {
  const dos = readHead(file, 0x40);
  if (dos.length < 0x40 || dos.readUInt16BE(0) !== 0x4d5a) return null; // "MZ"
  const offset = dos.readUInt32LE(0x3c);
  const fd = fs.openSync(file, 'r');
  try {
    const pe = Buffer.alloc(6);
    if (fs.readSync(fd, pe, 0, 6, offset) < 6 || pe.readUInt32BE(0) !== 0x50450000) return null; // "PE\0\0"
    return pe.readUInt16LE(4);
  } finally {
    fs.closeSync(fd);
  }
}

const READERS = { macho: machCPU, elf: elfMachine, pe: peMachine };
const FORMAT_NAMES = { macho: 'Mach-O', elf: 'ELF', pe: 'PE' };

// checkBinary fails unless the file is the executable format and CPU the package expects.
function checkBinary(p, bin) {
  const machine = READERS[p.format](bin);
  if (machine !== p.machine) {
    fail(`${bin} is not a 64-bit ${FORMAT_NAMES[p.format]} for ${p.os} ${p.cpu} (machine ${machine === null ? 'none' : '0x' + machine.toString(16)}, want 0x${p.machine.toString(16)})`);
  }
}

function writeJSON(file, value) {
  fs.writeFileSync(file, JSON.stringify(value, null, 2) + '\n');
}

function copyShared(dir) {
  for (const f of ['LICENSE', 'NOTICE']) fs.copyFileSync(path.join(root, f), path.join(dir, f));
}

// npm runs `npm` with args in cwd and returns its output. On Windows npm is npm.cmd, which Node
// runs only through a shell (since its 2024 fix for batch-file arguments); the shell takes one
// command line, so each argument is quoted. Paths on Windows cannot contain a double quote.
function npm(args, cwd) {
  if (process.platform !== 'win32') return execFileSync('npm', args, { cwd, encoding: 'utf8' });
  const line = ['npm.cmd', ...args].map((a) => (/[\s&|<>^]/.test(a) ? `"${a}"` : a)).join(' ');
  return execFileSync(line, { cwd, encoding: 'utf8', shell: true });
}

export function build({ version, binaries, out }) {
  if (!SEMVER.test(version)) fail(`not a version: ${version}`);
  fs.rmSync(out, { recursive: true, force: true });
  fs.mkdirSync(out, { recursive: true });
  const launcher = JSON.parse(fs.readFileSync(path.join(root, 'npm/dotlane/package.json'), 'utf8'));

  // Platform packages: one binary each, installed only where os and cpu match.
  for (const [key, p] of Object.entries(PLATFORMS)) {
    const bin = binaries[key];
    if (!bin || !fs.existsSync(bin)) fail(`missing binary for ${key}: ${bin ?? `pass --${key} <path>`}`);
    checkBinary(p, bin);
    const dir = path.join(out, key);
    fs.mkdirSync(path.join(dir, 'bin'), { recursive: true });
    fs.copyFileSync(bin, path.join(dir, 'bin', p.file));
    fs.chmodSync(path.join(dir, 'bin', p.file), 0o755);
    copyShared(dir);
    fs.writeFileSync(
      path.join(dir, 'README.md'),
      `# @dotlane/${key}\n\nThe \`${p.file}\` binary for ${p.label}. Install [\`dotlane\`](https://www.npmjs.com/package/dotlane) instead: it pulls in this package on matching machines.\n`,
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
      files: [`bin/${p.file}`, 'NOTICE'],
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
    const file = npm(['pack', '--silent', '--pack-destination', path.resolve(out)], path.join(out, name)).trim().split('\n').pop();
    tarballs.push(path.join(out, file));
  }
  const tag = version.includes('-') ? 'next' : 'latest';
  fs.writeFileSync(path.join(out, 'dist-tag'), tag + '\n');
  return { tarballs, tag };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const args = parseArgs(process.argv.slice(2));
  if (!args.version) fail('--version is required');
  for (const name of Object.keys(args)) {
    if (name !== 'version' && name !== 'out' && !(name in PLATFORMS)) fail(`unknown option --${name}`);
  }
  const { tarballs, tag } = build({
    version: args.version.replace(/^v/, ''),
    binaries: Object.fromEntries(Object.keys(PLATFORMS).map((key) => [key, args[key]])),
    out: path.resolve(args.out ?? 'npm/build'),
  });
  for (const t of tarballs) console.log(path.relative(process.cwd(), t));
  console.log(`dist-tag: ${tag}`);
}
