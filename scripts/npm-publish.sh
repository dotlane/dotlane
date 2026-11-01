#!/usr/bin/env bash
# Publishes the npm packages that scripts/npm-build.mjs wrote to a folder, platform packages
# first, so the launcher never points at versions that are not on npm yet:
#
#   scripts/npm-publish.sh dist/npm --provenance     # the release workflow
#   scripts/npm-publish.sh dist/npm --dry-run        # CI, on every pull request
#
# Extra arguments go to each `npm publish`. Tarballs are named exactly, with their version:
# `dotlane-*.tgz` would also match the platform packages' tarballs.
set -euo pipefail

cd "$1"
shift
version=$(node -p 'require("./dotlane/package.json").version')
tag=$(cat dist-tag)
for p in dotlane-darwin-arm64 dotlane-darwin-x64 dotlane; do
  npm publish --access public --tag "$tag" "$@" "./$p-$version.tgz"
done
