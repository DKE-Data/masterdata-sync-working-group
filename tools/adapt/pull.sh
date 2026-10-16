#!/usr/bin/env bash
#
# Downloads the ADAPT schema files at a release tag into adapt/ and records
# them in adapt/adapt.lock.yaml.
#
# Run from the repository root:
#
#   tools/adapt/pull.sh 2.0.2
#
# Files are fetched by the commit the tag points to, not by the tag, so a
# moved tag cannot change them unnoticed. They are committed unmodified: every
# deviation from ADAPT lives in adapt/profile.yaml, and tools/adapt/generate.sh
# refuses to run when a file no longer matches its checksum in the lock.
#
# After a pull, run tools/oapi/generate.sh. It fails on every ADAPT change the
# profile does not account for yet.

set -euo pipefail

REPO="AgGateway-ADAPT/Standard"
FILES=(adapt-root-schema.json adapt-data-type-definitions.json)

tag="${1:?usage: tools/adapt/pull.sh <tag>}"

cd "$(dirname "$0")/../.."

# An annotated tag is listed twice; the peeled ^{} entry is the commit.
refs="$(git ls-remote "https://github.com/$REPO.git" "refs/tags/$tag" "refs/tags/$tag^{}")"
commit="$(awk '$2 ~ /\^\{\}$/ { print $1; exit }' <<<"$refs")"
commit="${commit:-$(awk 'NR == 1 { print $1 }' <<<"$refs")}"
if [[ -z "$commit" ]]; then
  echo "pull.sh: no tag $tag in $REPO" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

for f in "${FILES[@]}"; do
  curl -fsSL "https://raw.githubusercontent.com/$REPO/$commit/$f" -o "$tmp/$f"
done

mkdir -p adapt
{
  echo "# Written by tools/adapt/pull.sh. Do not edit."
  echo "repo: $REPO"
  echo "tag: $tag"
  echo "commit: $commit"
  echo "files:"
  for f in "${FILES[@]}"; do
    echo "  $f: $(sha256sum "$tmp/$f" | cut -d' ' -f1)"
  done
} >"$tmp/adapt.lock.yaml"

for f in "${FILES[@]}" adapt.lock.yaml; do
  mv "$tmp/$f" "adapt/$f"
done

echo "Pulled ADAPT $tag ($commit) into adapt/."
