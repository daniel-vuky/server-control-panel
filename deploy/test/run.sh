#!/usr/bin/env bash
# Runs every deploy/test/*_test.sh and fails if any of them fails. lib.sh and
# fake-docker are helpers, not tests, and are never run from here.
# Run it through `make deploy-test` (a Linux container), never on the host.
set -uo pipefail

if [[ "$(uname -s)" != Linux ]]; then
  echo "run.sh needs Linux; run it with 'make deploy-test'" >&2
  exit 2
fi

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# Workspace roots under /tmp are refused, so tests make their fixtures under
# /srv and /var/www.
mkdir -p /srv /var/www

shopt -s nullglob
tests=("$here"/*_test.sh)
if [[ ${#tests[@]} -eq 0 ]]; then
  echo "no tests found in $here" >&2
  exit 1
fi

failed=()
for t in "${tests[@]}"; do
  echo "== $(basename "$t")"
  if ! bash "$t"; then
    failed+=("$(basename "$t")")
  fi
done

echo
if [[ ${#failed[@]} -gt 0 ]]; then
  echo "FAILED: ${failed[*]}"
  exit 1
fi
echo "all ${#tests[@]} test files passed"
