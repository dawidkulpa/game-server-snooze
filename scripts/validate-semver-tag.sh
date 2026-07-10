#!/usr/bin/env bash
set -euo pipefail

tag="${1:-}"
identifier='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
regex="^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-${identifier}(\\.${identifier})*)?$"
[[ "$tag" =~ $regex ]] || {
  printf 'invalid release tag: %s\n' "$tag" >&2
  exit 1
}
