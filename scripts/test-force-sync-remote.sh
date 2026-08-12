#!/usr/bin/env bash
set -Eeuo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf -- "$test_root"' EXIT

git init --bare --initial-branch=main "$test_root/remote.git" >/dev/null
git clone "$test_root/remote.git" "$test_root/source" >/dev/null 2>&1
git -C "$test_root/source" config user.name "FOCUS test"
git -C "$test_root/source" config user.email "focus-test@example.invalid"
printf 'remote version 1\n' >"$test_root/source/tracked.txt"
git -C "$test_root/source" add tracked.txt
git -C "$test_root/source" commit -m initial >/dev/null
git -C "$test_root/source" push origin main >/dev/null 2>&1

git clone "$test_root/remote.git" "$test_root/checkout" >/dev/null 2>&1
printf 'local modification\n' >"$test_root/checkout/tracked.txt"
printf 'preserve me\n' >"$test_root/checkout/untracked.txt"

printf 'remote version 2\n' >"$test_root/source/tracked.txt"
git -C "$test_root/source" commit -am update >/dev/null
git -C "$test_root/source" push origin main >/dev/null 2>&1

cp "$repo_dir/scripts/force-sync-remote.sh" "$test_root/checkout/force-sync-remote.sh"
(
  cd -- "$test_root/checkout"
  bash ./force-sync-remote.sh --yes
) >/dev/null

grep -qx 'remote version 2' "$test_root/checkout/tracked.txt"
grep -qx 'preserve me' "$test_root/checkout/untracked.txt"
test "$(git -C "$test_root/checkout" rev-parse HEAD)" = \
  "$(git -C "$test_root/source" rev-parse HEAD)"
