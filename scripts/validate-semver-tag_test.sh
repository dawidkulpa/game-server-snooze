#!/usr/bin/env bash
set -euo pipefail
validator="$(dirname "$0")/validate-semver-tag.sh"
valid=(v0.0.0 v1.2.3 v1.2.3-rc.1 v1.2.3-alpha-beta v1.2.3-0)
invalid=(1.2.3 v01.2.3 v1.02.3 v1.2.03 v1.2 v1.2.3- v1.2.3-01 v1.2.3-rc..1 v1.2.3-rc. v1.2.3+build)
for tag in "${valid[@]}"; do "$validator" "$tag"; done
for tag in "${invalid[@]}"; do
  if "$validator" "$tag" >/dev/null 2>&1; then
    printf 'accepted invalid tag: %s\n' "$tag" >&2
    exit 1
  fi
done
