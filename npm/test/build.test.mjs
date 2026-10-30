// Tests for scripts/npm-build.mjs: node --test npm/test/
import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { build, machCPU, PLATFORMS } from '../../scripts/npm-build.mjs';

// fakeMachO writes a file with a 64-bit Mach-O header for the given CPU type, and some padding.
function fakeMachO(dir, name, cpu) {
  const buf = Buffer.alloc(64);
  buf.writeUInt32LE(0xfeedfacf, 0);
  buf.writeUInt32LE(cpu, 4);
  const file = path.join(dir, name);
  fs.writeFileSync(file, buf);
  return file;
}

function tmp(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dotlane-npm-build-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('machCPU reads the CPU type of a 64-bit Mach-O, or null', (t) => {
  const dir = tmp(t);
  assert.equal(machCPU(fakeMachO(dir, 'a', PLATFORMS['darwin-arm64'].machCPU)), 0x0100000c);
  assert.equal(machCPU(fakeMachO(dir, 'x', PLATFORMS['darwin-x64'].machCPU)), 0x01000007);
  fs.writeFileSync(path.join(dir, 'elf'), Buffer.from('\x7fELF\x02\x01\x01\x00'));
  assert.equal(machCPU(path.join(dir, 'elf')), null);
  fs.writeFileSync(path.join(dir, 'short'), 'ab');
  assert.equal(machCPU(path.join(dir, 'short')), null);
});

test('packages share one version, and a pre-release is tagged next', (t) => {
  const dir = tmp(t);
  const binaries = {
    'darwin-arm64': fakeMachO(dir, 'arm64', PLATFORMS['darwin-arm64'].machCPU),
    'darwin-x64': fakeMachO(dir, 'x64', PLATFORMS['darwin-x64'].machCPU),
  };
  const out = path.join(dir, 'out');
  const { tarballs, tag } = build({ version: '1.2.3-rc.4', binaries, out });

  assert.equal(tag, 'next');
  assert.equal(fs.readFileSync(path.join(out, 'dist-tag'), 'utf8'), 'next\n');
  assert.deepEqual(tarballs.map((f) => path.basename(f)).sort(), [
    'dotlane-1.2.3-rc.4.tgz',
    'dotlane-darwin-arm64-1.2.3-rc.4.tgz',
    'dotlane-darwin-x64-1.2.3-rc.4.tgz',
  ]);

  const launcher = JSON.parse(fs.readFileSync(path.join(out, 'dotlane/package.json'), 'utf8'));
  assert.equal(launcher.name, 'dotlane');
  assert.equal(launcher.version, '1.2.3-rc.4');
  assert.deepEqual(launcher.optionalDependencies, { '@dotlane/darwin-arm64': '1.2.3-rc.4', '@dotlane/darwin-x64': '1.2.3-rc.4' });
  assert.deepEqual(launcher.bin, { dotlane: 'bin/dotlane.js' });
  assert.equal(launcher.scripts, undefined, 'no install scripts');

  for (const [key, p] of Object.entries(PLATFORMS)) {
    const pkg = JSON.parse(fs.readFileSync(path.join(out, key, 'package.json'), 'utf8'));
    assert.equal(pkg.name, `@dotlane/${key}`);
    assert.equal(pkg.version, '1.2.3-rc.4');
    assert.deepEqual([pkg.os, pkg.cpu], [[p.os], [p.cpu]]);
    assert.equal(pkg.scripts, undefined, 'no install scripts');
    const bin = path.join(out, key, 'bin/dotlane');
    assert.equal(fs.statSync(bin).mode & 0o777, 0o755);
    assert.ok(fs.readFileSync(bin).equals(fs.readFileSync(binaries[key])), `${key} carries its own binary`);
    for (const f of ['LICENSE', 'NOTICE', 'README.md']) assert.ok(fs.existsSync(path.join(out, key, f)), `${key}/${f}`);
  }
});

test('a release is tagged latest', (t) => {
  const dir = tmp(t);
  const binaries = {
    'darwin-arm64': fakeMachO(dir, 'arm64', PLATFORMS['darwin-arm64'].machCPU),
    'darwin-x64': fakeMachO(dir, 'x64', PLATFORMS['darwin-x64'].machCPU),
  };
  assert.equal(build({ version: '1.2.3', binaries, out: path.join(dir, 'out') }).tag, 'latest');
});
