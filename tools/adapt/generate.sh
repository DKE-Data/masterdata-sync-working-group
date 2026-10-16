#!/usr/bin/env bash
#
# Writes the ADAPT-based schemas of openapi.yaml from adapt/profile.yaml and
# the ADAPT release pinned in adapt/adapt.lock.yaml.
#
# Run from the repository root:
#
#   tools/adapt/generate.sh           # rewrite openapi.yaml
#   tools/adapt/generate.sh -check    # fail if openapi.yaml is out of date
#
# Edit a generated schema in adapt/profile.yaml, not in openapi.yaml. After
# tools/adapt/pull.sh, this fails on every ADAPT change the profile does not
# account for yet.

set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root/tools/adapt"
go run . -root "$root" "$@"
