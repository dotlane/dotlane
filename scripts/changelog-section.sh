#!/usr/bin/env bash
# Prints the CHANGELOG.md section of one version, to be used as its release notes:
#
#   scripts/changelog-section.sh v0.1.0          # or 0.1.0; also v0.1.0-rc.1
#
# A pre-release tag (v0.1.0-rc.1) uses its own section if it has one, else the section of the
# version it leads to (0.1.0). Exits 1 when there is no such section, or it is empty, so a
# release cannot go out undocumented.
set -euo pipefail

version=${1:?usage: scripts/changelog-section.sh <version>}
version=${version#v}
file=${CHANGELOG:-CHANGELOG.md}

section() {
  # Lines after "## [<v>]" up to the next "## [" heading or the link references at the end.
  awk -v v="$1" '
    index($0, "## [" v "]") == 1 { on = 1; next }
    on && (/^## \[/ || /^\[[^]]+\]: /) { exit }
    on { print }
  ' "$file"
}

notes=$(section "$version")
if [[ -z "${notes//[$'\n\t ']/}" && "$version" == *-* ]]; then
  notes=$(section "${version%%-*}")
fi
if [[ -z "${notes//[$'\n\t ']/}" ]]; then
  echo "changelog-section: $file has no section for $version; add \"## [$version] - $(date +%F)\" before tagging" >&2
  exit 1
fi
printf '%s\n' "$notes" | sed -e '/./,$!d' # without leading blank lines
