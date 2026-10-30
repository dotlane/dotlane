// Tests for the npm launcher (npm/dotlane/bin/dotlane.js): node --test npm/test/
'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const shimPath = path.join(__dirname, '../dotlane/bin/dotlane.js');
const { PACKAGES, binaryPath, ShimError } = require(shimPath);

// fakeBinary writes an executable shell script standing in for the dotlane binary.
function fakeBinary(t, body) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dotlane-shim-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const file = path.join(dir, 'dotlane');
  fs.writeFileSync(file, `#!/bin/sh\n${body}\n`, { mode: 0o755 });
  return file;
}

// shim runs the launcher as a child process, the way npx does.
function shim(args, env = {}, input) {
  return spawnSync(process.execPath, [shimPath, ...args], {
    env: { ...process.env, ...env },
    input,
    encoding: 'utf8',
  });
}

test('each supported platform maps to its package', () => {
  assert.deepEqual(PACKAGES, { 'darwin-arm64': '@dotlane/darwin-arm64', 'darwin-x64': '@dotlane/darwin-x64' });
  for (const [key, pkg] of Object.entries(PACKAGES)) {
    const [platform, arch] = key.split('-');
    const asked = [];
    const resolve = (id) => {
      asked.push(id);
      return `/nm/${id}`;
    };
    assert.equal(binaryPath({ platform, arch, env: {}, resolve }), `/nm/${pkg}/bin/dotlane`);
    assert.deepEqual(asked, [`${pkg}/bin/dotlane`]);
  }
});

test('an unsupported platform names the supported ones', () => {
  assert.throws(
    () => binaryPath({ platform: 'linux', arch: 'x64', env: {}, resolve: () => assert.fail('resolved') }),
    (err) => err instanceof ShimError && /linux-x64/.test(err.message) && /darwin-arm64, darwin-x64/.test(err.message),
  );
});

test('a missing platform package says how to install it', () => {
  const resolve = () => {
    throw Object.assign(new Error('Cannot find module'), { code: 'MODULE_NOT_FOUND' });
  };
  assert.throws(
    () => binaryPath({ platform: 'darwin', arch: 'arm64', env: {}, resolve }),
    (err) => err instanceof ShimError && /npm install @dotlane\/darwin-arm64/.test(err.message) && /--no-optional/.test(err.message),
  );
});

test('DOTLANE_BINARY_PATH overrides the platform package', () => {
  assert.equal(binaryPath({ platform: 'plan9', arch: 'mips', env: { DOTLANE_BINARY_PATH: '/x/dotlane' } }), '/x/dotlane');
});

test('arguments and the exit code pass through', (t) => {
  const bin = fakeBinary(t, 'printf "%s|" "$@"; exit 3');
  const r = shim(['add', 'my app', '5173', '--json'], { DOTLANE_BINARY_PATH: bin });
  assert.equal(r.stdout, 'add|my app|5173|--json|');
  assert.equal(r.status, 3);
});

test('stdin and stderr pass through', (t) => {
  const bin = fakeBinary(t, 'cat; echo oops >&2');
  const r = shim([], { DOTLANE_BINARY_PATH: bin }, 'from stdin\n');
  assert.equal(r.stdout, 'from stdin\n');
  assert.equal(r.stderr, 'oops\n');
  assert.equal(r.status, 0);
});

test('a signal that ends the binary ends the launcher the same way', (t) => {
  const bin = fakeBinary(t, 'kill -TERM $$');
  const r = shim([], { DOTLANE_BINARY_PATH: bin });
  assert.equal(r.signal, 'SIGTERM');
  assert.equal(r.status, null);
});

test('a binary that cannot run is reported, exit 1', (t) => {
  const r = shim(['version'], { DOTLANE_BINARY_PATH: path.join(os.tmpdir(), 'no-such-dotlane-binary') });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /^dotlane: could not run .*no-such-dotlane-binary/);
});

test('an unsupported platform exits 1 with the message', { skip: process.platform === 'darwin' && 'only off macOS' }, () => {
  const r = shim(['version'], { DOTLANE_BINARY_PATH: '' });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /^dotlane: dotlane does not support /);
});
