#!/usr/bin/env bash
set -euo pipefail
validator="$(dirname "$0")/validate-semver-tag.sh"
valid=(0.0.0 1.2.3 1.2.3-rc.1 1.2.3-alpha-beta 1.2.3-0)
invalid=(v1.2.3 01.2.3 1.02.3 1.2.03 1.2 1.2.3- 1.2.3-01 1.2.3-rc..1 1.2.3-rc. 1.2.3+build)
for tag in "${valid[@]}"; do "$validator" "$tag"; done
for tag in "${invalid[@]}"; do
  if "$validator" "$tag" >/dev/null 2>&1; then
    printf 'accepted invalid tag: %s\n' "$tag" >&2
    exit 1
  fi
done
