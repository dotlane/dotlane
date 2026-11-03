#!/usr/bin/env bash
# Changelog fragments (docs/branching.md, Changelog). A pull request a user would notice adds one
# file under changes/ instead of editing CHANGELOG.md, so pull requests in parallel never touch the
# same lines. A release collects them:
#
#   scripts/changelog.sh check            # every fragment is well formed (scripts/check.sh runs it)
#   scripts/changelog.sh preview          # the next release's notes, from the fragments
#   scripts/changelog.sh release v0.3.0   # writes them into CHANGELOG.md as [0.3.0], deletes them
#
# A fragment is Markdown holding only Keep a Changelog sections (### Added, Changed, Deprecated,
# Removed, Fixed, Security), each with its bullets. The notes list each section once, in that
# order, with the bullets of every fragment in file-name order. CHANGES overrides the folder
# (default changes), CHANGELOG the file.
set -euo pipefail

dir=${CHANGES:-changes}
file=${CHANGELOG:-CHANGELOG.md}
repo=${REPO:-dotlane/dotlane}

fragments() { find "$dir" -maxdepth 1 -name '*.md' ! -name README.md 2>/dev/null | LC_ALL=C sort; }

# Prints the merged sections, or names every malformed fragment on stderr and exits 1.
collect() {
  local files=() f
  while IFS= read -r f; do files+=("$f"); done < <(fragments) # no mapfile: macOS ships Bash 3.2
  (( ${#files[@]} )) || return 0
  awk '
    BEGIN {
      n = split("Added Changed Deprecated Removed Fixed Security", order, " ")
      for (i = 1; i <= n; i++) known[order[i]] = 1
    }
    FNR == 1 { flush(); sec = ""; cur = FILENAME; got[cur] = 0 }
    /^### / {
      flush(); sec = substr($0, 5)
      if (!(sec in known)) { printf "%s:%d: unknown section \"%s\"\n", FILENAME, FNR, sec > "/dev/stderr"; bad = 1 }
      next
    }
    /^#/ { printf "%s:%d: only ### sections go in a fragment\n", FILENAME, FNR > "/dev/stderr"; bad = 1; next }
    {
      if (sec == "") {
        if ($0 ~ /[^ \t]/) { printf "%s:%d: text before the first ### section\n", FILENAME, FNR > "/dev/stderr"; bad = 1 }
        next
      }
      chunk = chunk $0 "\n"
    }
    # One fragment section, without its leading and trailing blank lines, appended to its bucket.
    function flush(  c) {
      if (sec == "") return
      c = chunk; sub(/^([ \t]*\n)+/, "", c); sub(/(\n[ \t]*)+$/, "", c); sub(/\n*$/, "", c)
      if (c != "") { body[sec] = body[sec] c "\n"; got[cur] = 1 }
      chunk = ""
    }
    END {
      flush()
      for (f in got) if (!got[f]) { printf "%s: no bullets under any section\n", f > "/dev/stderr"; bad = 1 }
      if (bad) exit 1
      sep = ""
      for (i = 1; i <= n; i++) if (order[i] in body) { printf "%s### %s\n\n%s", sep, order[i], body[order[i]]; sep = "\n" }
    }
  ' "${files[@]}"
}

case ${1:-} in
  check)
    collect >/dev/null
    ;;
  preview)
    collect
    ;;
  release)
    version=${2:?usage: scripts/changelog.sh release <version>}
    version=${version#v}
    notes=$(collect)
    [[ -n $notes ]] || { echo "changelog: no fragments in $dir/" >&2; exit 1; }
    if grep -qF "## [$version]" "$file"; then
      echo "changelog: $file already has a section for $version" >&2
      exit 1
    fi
    tmp=$(mktemp)
    # The new section goes above the newest version, its link above the newest link.
    # The notes go through the environment: awk -v would expand backslashes in them.
    NOTES=$notes awk -v v="$version" -v d="$(date +%F)" -v repo="$repo" '
      BEGIN { notes = ENVIRON["NOTES"] }
      !sec && /^## \[/ { printf "## [%s] - %s\n\n%s\n\n", v, d, notes; sec = 1 }
      !link && /^\[[^]]+\]: / { printf "[%s]: https://github.com/%s/releases/tag/v%s\n", v, repo, v; link = 1 }
      { print }
      END {
        if (!sec) { printf "\n## [%s] - %s\n\n%s\n", v, d, notes }
        if (!link) { printf "\n[%s]: https://github.com/%s/releases/tag/v%s\n", v, repo, v }
      }
    ' "$file" >"$tmp"
    mv "$tmp" "$file"
    fragments | xargs rm -f
    echo "changelog: wrote [$version] into $file and removed the fragments; review it, then commit"
    ;;
  *)
    echo "usage: scripts/changelog.sh check | preview | release <version>" >&2
    exit 2
    ;;
esac
