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
# The platform packages are the launcher's optional dependencies, so the list cannot drift from
# what it depends on: @dotlane/linux-x64 is packed as dotlane-linux-x64-<version>.tgz. A tarball
# that is missing makes `npm publish` fail before the launcher is published.
platforms=$(node -p 'Object.keys(require("./dotlane/package.json").optionalDependencies).map((n) => n.replace("@dotlane/", "dotlane-")).join(" ")')
[[ -n $platforms ]] || { echo "npm-publish: the launcher has no platform packages" >&2; exit 1; }
for p in $platforms dotlane; do
  npm publish --access public --tag "$tag" "$@" "./$p-$version.tgz"
done
