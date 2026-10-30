#!/usr/bin/env bash
# Prints the CHANGELOG.md section of one version, to be used as its release notes:
#
#   scripts/changelog-section.sh v0.1.0          # or 0.1.0; also v0.1.0-rc.1
#
# A pre-release tag (v0.1.0-rc.1) uses its own section if it has one, else the section of the
# version it leads to (0.1.0). Exits 1 when there is no such section, or it is empty, so a
# release cannot go out undocumented.
#
# Relative links ([roadmap](docs/roadmap.md)) become full URLs at the tag
# (https://github.com/dotlane/dotlane/blob/v0.1.0/docs/roadmap.md), since a release page does not
# resolve paths in the repository. Absolute URLs, #anchors and mailto: links stay as they are.
# REPO overrides the repository (default dotlane/dotlane).
set -euo pipefail

version=${1:?usage: scripts/changelog-section.sh <version>}
ref=v${version#v} # the tag the links point at
version=${version#v}
repo=${REPO:-dotlane/dotlane}
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
# Without leading blank lines; relative link targets (no scheme, not an anchor) made absolute.
printf '%s\n' "$notes" | sed -e '/./,$!d' \
  -E -e "s#\]\((\./)?([^):#][^):]*)\)#](https://github.com/${repo}/blob/${ref}/\2)#g"
