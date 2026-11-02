// Tests for scripts/npm-build.mjs: node --test npm/test/
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { build, elfMachine, machCPU, peMachine, PLATFORMS } from '../../scripts/npm-build.mjs';

// fakeMachO writes a file with a 64-bit Mach-O header for the given CPU type, and some padding.
function fakeMachO(dir, name, cpu) {
  const buf = Buffer.alloc(64);
  buf.writeUInt32LE(0xfeedfacf, 0);
  buf.writeUInt32LE(cpu, 4);
  const file = path.join(dir, name);
  fs.writeFileSync(file, buf);
  return file;
}

// fakeELF writes the start of a 64-bit little-endian ELF file for the given e_machine.
function fakeELF(dir, name, machine, { class64 = true } = {}) {
  const buf = Buffer.alloc(64);
  buf.write('\x7fELF', 0, 'latin1');
  buf[4] = class64 ? 2 : 1;
  buf[5] = 1; // little-endian
  buf.writeUInt16LE(2, 16); // ET_EXEC
  buf.writeUInt16LE(machine, 18);
  const file = path.join(dir, name);
  fs.writeFileSync(file, buf);
  return file;
}

// fakePE writes a DOS header pointing at a PE signature and COFF header with the given Machine.
function fakePE(dir, name, machine, { peOffset = 0x80 } = {}) {
  const buf = Buffer.alloc(0x100);
  buf.write('MZ', 0, 'latin1');
  buf.writeUInt32LE(peOffset, 0x3c);
  buf.write('PE\0\0', peOffset, 'latin1');
  buf.writeUInt16LE(machine, peOffset + 4);
  const file = path.join(dir, name);
  fs.writeFileSync(file, buf);
  return file;
}

const FAKES = { macho: fakeMachO, elf: fakeELF, pe: fakePE };

// fakeBinaries writes a correct fake binary for every platform, keyed like PLATFORMS.
function fakeBinaries(dir) {
  return Object.fromEntries(Object.entries(PLATFORMS).map(([key, p]) => [key, FAKES[p.format](dir, key, p.machine)]));
}

const buildScript = fileURLToPath(new URL('../../scripts/npm-build.mjs', import.meta.url));

// spawnBuild runs the command line with the given binaries and returns its status and output.
function spawnBuild(dir, binaries) {
  const args = Object.entries(binaries).flatMap(([k, f]) => [`--${k}`, f]);
  return spawnSync(process.execPath, [buildScript, '--version', '1.2.3', '--out', path.join(dir, 'out'), ...args], { encoding: 'utf8' });
}

function tmp(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dotlane-npm-build-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('machCPU reads the CPU type of a 64-bit Mach-O, or null', (t) => {
  const dir = tmp(t);
  assert.equal(machCPU(fakeMachO(dir, 'a', PLATFORMS['darwin-arm64'].machine)), 0x0100000c);
  assert.equal(machCPU(fakeMachO(dir, 'x', PLATFORMS['darwin-x64'].machine)), 0x01000007);
  assert.equal(machCPU(fakeELF(dir, 'elf', 0x3e)), null);
  fs.writeFileSync(path.join(dir, 'short'), 'ab');
  assert.equal(machCPU(path.join(dir, 'short')), null);
});

test('elfMachine reads e_machine of a 64-bit little-endian ELF, or null', (t) => {
  const dir = tmp(t);
  assert.equal(elfMachine(fakeELF(dir, 'x', 0x3e)), 0x3e);
  assert.equal(elfMachine(fakeELF(dir, 'a', 0xb7)), 0xb7);
  assert.equal(elfMachine(fakeELF(dir, '32', 0x3e, { class64: false })), null, '32-bit class');
  assert.equal(elfMachine(fakeMachO(dir, 'macho', 0x01000007)), null);
  assert.equal(elfMachine(fakePE(dir, 'pe', 0x8664)), null);
  fs.writeFileSync(path.join(dir, 'short'), '\x7fELF\x02\x01');
  assert.equal(elfMachine(path.join(dir, 'short')), null);
});

test('peMachine reads Machine of a PE file, or null', (t) => {
  const dir = tmp(t);
  assert.equal(peMachine(fakePE(dir, 'x', 0x8664)), 0x8664);
  assert.equal(peMachine(fakePE(dir, 'a', 0xaa64, { peOffset: 0xc0 })), 0xaa64, 'follows e_lfanew');
  assert.equal(peMachine(fakeELF(dir, 'elf', 0x3e)), null);
  // MZ without a PE signature where e_lfanew points (a DOS-only executable).
  const dos = Buffer.alloc(0x100);
  dos.write('MZ', 0, 'latin1');
  dos.writeUInt32LE(0x80, 0x3c);
  fs.writeFileSync(path.join(dir, 'dos'), dos);
  assert.equal(peMachine(path.join(dir, 'dos')), null);
  // e_lfanew past the end of the file.
  dos.writeUInt32LE(0x10000, 0x3c);
  fs.writeFileSync(path.join(dir, 'far'), dos);
  assert.equal(peMachine(path.join(dir, 'far')), null);
  fs.writeFileSync(path.join(dir, 'short'), 'MZ');
  assert.equal(peMachine(path.join(dir, 'short')), null);
});

test('the platform table covers the six supported platforms', () => {
  assert.deepEqual(Object.keys(PLATFORMS).sort(), ['darwin-arm64', 'darwin-x64', 'linux-arm64', 'linux-x64', 'win32-arm64', 'win32-x64']);
  assert.deepEqual(
    Object.values(PLATFORMS).map((p) => [p.format, p.machine, p.file]).sort(),
    [
      ['elf', 0x3e, 'dotlane'],
      ['elf', 0xb7, 'dotlane'],
      ['macho', 0x0100000c, 'dotlane'],
      ['macho', 0x01000007, 'dotlane'],
      ['pe', 0x8664, 'dotlane.exe'],
      ['pe', 0xaa64, 'dotlane.exe'],
    ].sort(),
  );
});

test('packages share one version, and a pre-release is tagged next', (t) => {
  const dir = tmp(t);
  const binaries = fakeBinaries(dir);
  const out = path.join(dir, 'out');
  const { tarballs, tag } = build({ version: '1.2.3-rc.4', binaries, out });

  assert.equal(tag, 'next');
  assert.equal(fs.readFileSync(path.join(out, 'dist-tag'), 'utf8'), 'next\n');
  assert.deepEqual(
    tarballs.map((f) => path.basename(f)).sort(),
    ['dotlane', ...Object.keys(PLATFORMS).map((k) => `dotlane-${k}`)].map((n) => `${n}-1.2.3-rc.4.tgz`).sort(),
  );
  assert.ok(tarballs.length === 7 && tarballs.at(-1).endsWith('/dotlane-1.2.3-rc.4.tgz'), 'the launcher is packed last');

  const launcher = JSON.parse(fs.readFileSync(path.join(out, 'dotlane/package.json'), 'utf8'));
  assert.equal(launcher.name, 'dotlane');
  assert.equal(launcher.version, '1.2.3-rc.4');
  assert.deepEqual(launcher.optionalDependencies, Object.fromEntries(Object.keys(PLATFORMS).map((k) => [`@dotlane/${k}`, '1.2.3-rc.4'])));
  assert.deepEqual(launcher.bin, { dotlane: 'bin/dotlane.js' });
  assert.equal(launcher.scripts, undefined, 'no install scripts');

  for (const [key, p] of Object.entries(PLATFORMS)) {
    const pkg = JSON.parse(fs.readFileSync(path.join(out, key, 'package.json'), 'utf8'));
    assert.equal(pkg.name, `@dotlane/${key}`);
    assert.equal(pkg.version, '1.2.3-rc.4');
    assert.deepEqual([pkg.os, pkg.cpu], [[p.os], [p.cpu]]);
    assert.deepEqual(pkg.files, [`bin/${p.file}`, 'NOTICE']);
    assert.equal(pkg.scripts, undefined, 'no install scripts');
    const bin = path.join(out, key, 'bin', p.file);
    if (process.platform !== 'win32') assert.equal(fs.statSync(bin).mode & 0o777, 0o755);
    assert.deepEqual(fs.readdirSync(path.join(out, key, 'bin')), [p.file], `${key} holds only ${p.file}`);
    assert.ok(fs.readFileSync(bin).equals(fs.readFileSync(binaries[key])), `${key} carries its own binary`);
    for (const f of ['LICENSE', 'NOTICE', 'README.md']) assert.ok(fs.existsSync(path.join(out, key, f)), `${key}/${f}`);
  }
});

test('a release is tagged latest', (t) => {
  const dir = tmp(t);
  assert.equal(build({ version: '1.2.3', binaries: fakeBinaries(dir), out: path.join(dir, 'out') }).tag, 'latest');
});

// Every binary of a wrong format or CPU, in every package, is refused: the table of what each
// package must hold is the only thing between a mix-up of GoReleaser's directories and a release.
test('a binary of the wrong format or CPU is refused for every platform', (t) => {
  const dir = tmp(t);
  const good = fakeBinaries(dir);
  const wrong = {
    macho: fakeELF(dir, 'wrong-elf', 0x3e),
    elf: fakePE(dir, 'wrong-pe', 0x8664),
    pe: fakeMachO(dir, 'wrong-macho', 0x01000007),
  };
  for (const [key, p] of Object.entries(PLATFORMS)) {
    // Right format, other CPU.
    const otherCPU = Object.entries(PLATFORMS).find(([k, q]) => q.format === p.format && k !== key)[1];
    const candidates = { 'another format': wrong[p.format], 'another CPU': good[Object.keys(PLATFORMS).find((k) => PLATFORMS[k] === otherCPU)] };
    for (const [what, bin] of Object.entries(candidates)) {
      const r = spawnBuild(dir, { ...good, [key]: bin });
      assert.equal(r.status, 1, `${key} with ${what}`);
      assert.match(r.stderr, /^npm-build: .* is not a 64-bit /, `${key} with ${what}`);
    }
  }
});

test('a 32-bit ELF for linux-x64 is refused', (t) => {
  const dir = tmp(t);
  const r = spawnBuild(dir, { ...fakeBinaries(dir), 'linux-x64': fakeELF(dir, '32', 0x3e, { class64: false }) });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /is not a 64-bit ELF for linux x64/);
});

// A partial set would publish a launcher whose optional dependencies do not all exist.
test('a missing binary fails the build, whichever one it is', (t) => {
  const dir = tmp(t);
  const good = fakeBinaries(dir);
  for (const key of Object.keys(PLATFORMS)) {
    const { [key]: _omitted, ...rest } = good;
    const r = spawnBuild(dir, rest);
    assert.equal(r.status, 1, key);
    assert.match(r.stderr, new RegExp(`missing binary for ${key}: pass --${key}`));
  }
});

test('an unknown option is refused', (t) => {
  const dir = tmp(t);
  const r = spawnBuild(dir, { ...fakeBinaries(dir), 'linux-x86': 'x' });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /unknown option --linux-x86/);
});
