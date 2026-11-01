#!/usr/bin/env bash
# Puts a published release's cask (dotlane.rb, attached to the release by release.yml) into the
# Homebrew tap, dotlane/homebrew-tap, as Casks/dotlane.rb:
#
#   scripts/tap-update.sh v0.1.0
#
# tap.yml runs it when a release is published, with the tap's deploy key in
# HOMEBREW_TAP_DEPLOY_KEY; by hand it uses your own SSH access to the tap. Nothing is committed
# when the cask is unchanged.
set -euo pipefail

tag=${1:?usage: scripts/tap-update.sh <tag>}
repo=${GITHUB_REPOSITORY:-dotlane/dotlane}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

gh release download "$tag" -R "$repo" -p dotlane.rb -D "$WORK"
grep -q "^  version \"${tag#v}\"$" "$WORK/dotlane.rb" || { echo "dotlane.rb is not for $tag" >&2; exit 1; }

if [[ -n "${HOMEBREW_TAP_DEPLOY_KEY:-}" ]]; then
  # The deploy key, and GitHub's SSH host keys from its API over HTTPS rather than trusted on
  # first use.
  printf '%s\n' "$HOMEBREW_TAP_DEPLOY_KEY" > "$WORK/key"
  chmod 600 "$WORK/key"
  curl -fsS https://api.github.com/meta | jq -r '.ssh_keys[] | "github.com " + .' > "$WORK/known_hosts"
  export GIT_SSH_COMMAND="ssh -i $WORK/key -o IdentitiesOnly=yes -o UserKnownHostsFile=$WORK/known_hosts -F /dev/null"
  export GIT_AUTHOR_NAME="github-actions[bot]" GIT_AUTHOR_EMAIL="41898282+github-actions[bot]@users.noreply.github.com"
  export GIT_COMMITTER_NAME=$GIT_AUTHOR_NAME GIT_COMMITTER_EMAIL=$GIT_AUTHOR_EMAIL
fi

git clone -q --depth 1 git@github.com:dotlane/homebrew-tap.git "$WORK/tap"
mkdir -p "$WORK/tap/Casks"
cp "$WORK/dotlane.rb" "$WORK/tap/Casks/dotlane.rb"
cd "$WORK/tap"
git add Casks/dotlane.rb
if git diff --cached --quiet; then
  echo "the tap already has dotlane $tag"
  exit 0
fi
git commit -q -m "dotlane ${tag}"
git push -q origin HEAD
echo "dotlane/homebrew-tap: dotlane $tag"
