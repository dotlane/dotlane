#!/usr/bin/env node
// The `dotlane` command from npm. The binary itself comes in a platform package
// (@dotlane/darwin-arm64, @dotlane/darwin-x64) that npm installs as an optional dependency
// matching this machine's `os` and `cpu`; this shim finds it and runs it with the same
// arguments, standard streams, exit code and signals. No install scripts. npm/ROADMAP.md.
'use strict';

const { spawnSync } = require('node:child_process');
const path = require('node:path');

// Supported `${process.platform}-${process.arch}` and the package holding that binary.
const PACKAGES = {
  'darwin-arm64': '@dotlane/darwin-arm64',
  'darwin-x64': '@dotlane/darwin-x64',
};

class ShimError extends Error {}

// binaryPath returns the dotlane binary for this machine. DOTLANE_BINARY_PATH overrides it, for
// development and tests.
function binaryPath({ platform = process.platform, arch = process.arch, env = process.env, resolve = require.resolve } = {}) {
  if (env.DOTLANE_BINARY_PATH) {
    return env.DOTLANE_BINARY_PATH;
  }
  const key = `${platform}-${arch}`;
  const pkg = PACKAGES[key];
  if (!pkg) {
    throw new ShimError(
      `dotlane does not support ${key} yet; supported: ${Object.keys(PACKAGES).join(', ')}.\n` +
        'Linux and Windows are planned: https://github.com/dotlane/dotlane/blob/main/docs/roadmap.md',
    );
  }
  try {
    return resolve(`${pkg}/bin/dotlane`);
  } catch {
    throw new ShimError(
      `the package ${pkg}, which holds the dotlane binary for ${key}, is not installed.\n` +
        'It is an optional dependency of dotlane: reinstall without --no-optional / --omit=optional,\n' +
        `or install it directly: npm install ${pkg}`,
    );
  }
}

// run runs the binary and returns how the shim should end: an exit code, or a signal to
// re-raise so the caller sees the same termination as the binary's.
function run(args, options = {}) {
  const bin = binaryPath(options);
  const result = spawnSync(bin, args, { stdio: 'inherit' });
  if (result.error) {
    throw new ShimError(`could not run ${bin}: ${result.error.message}`);
  }
  if (result.signal) {
    return { signal: result.signal };
  }
  return { code: result.status ?? 1 };
}

function main() {
  let outcome;
  try {
    outcome = run(process.argv.slice(2));
  } catch (err) {
    if (err instanceof ShimError) {
      process.stderr.write(`dotlane: ${err.message}\n`);
      process.exit(1);
    }
    throw err;
  }
  if (outcome.signal) {
    // Ignore our own handler for it, then die of the same signal.
    process.removeAllListeners(outcome.signal);
    process.kill(process.pid, outcome.signal);
    return;
  }
  process.exit(outcome.code);
}

if (require.main === module) {
  main();
}

module.exports = { PACKAGES, binaryPath, run, ShimError, packageDir: path.join(__dirname, '..') };
