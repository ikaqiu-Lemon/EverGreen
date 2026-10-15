#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
manifest="$repo_root/siyuan/.evergreen-source.json"
import_commit="83cf949fcde5293eba8c6e7423fe3baa2242301c"
source_tree="5fcc6ba8f4b422b879ff477642f4cbf883fc1436"
if command -v pnpm >/dev/null 2>&1; then
  pnpm_cmd=(pnpm)
elif command -v corepack >/dev/null 2>&1; then
  pnpm_cmd=(corepack pnpm)
else
  pnpm_cmd=()
fi

fail() {
  printf 'siyuan monorepo verification failed: %s\n' "$*" >&2
  exit 1
}

[[ -f "$manifest" ]] || fail "missing $manifest"
[[ -f "$repo_root/siyuan/LICENSE" ]] || fail "missing siyuan/LICENSE"
[[ -f "$repo_root/siyuan/THIRD_PARTY_NOTICES.md" ]] || fail "missing third-party notices"
[[ -f "$repo_root/siyuan/SOURCE_CODE.md" ]] || fail "missing corresponding-source statement"

actual_root="$(git -C "$repo_root" rev-parse --show-toplevel)"
[[ "$actual_root" == "$repo_root" ]] || fail "script is not running in the Evergreen repository"
git -C "$repo_root" merge-base --is-ancestor "$import_commit" HEAD ||
  fail "pure SiYuan import commit is not an ancestor of HEAD"

import_tree="$(git -C "$repo_root" rev-parse "$import_commit:siyuan")"
[[ "$import_tree" == "$source_tree" ]] ||
  fail "import subtree $import_tree does not match source tree $source_tree"

import_count="$(git -C "$repo_root" ls-tree -r --name-only "$import_commit:siyuan" | wc -l | tr -d ' ')"
[[ "$import_count" == "4935" ]] || fail "import contains $import_count entries, expected 4935"

[[ ! -e "$repo_root/siyuan/.git" ]] || fail "siyuan/.git must not exist"
if git -C "$repo_root" ls-files -- siyuan |
  grep -Eq '(^|/)\.git($|/)|(^|/)\.gitmodules$'; then
  fail "tracked nested Git metadata found under siyuan/"
fi

if git -C "$repo_root" ls-files --stage -- siyuan |
  awk '$1 == "160000" { found = 1 } END { exit found ? 0 : 1 }'; then
  fail "gitlink found under siyuan/"
fi

untracked="$(git -C "$repo_root" ls-files --others --exclude-standard -- siyuan)"
[[ -z "$untracked" ]] || fail "untracked files found under siyuan/: $untracked"

python3 - "$manifest" "$repo_root" <<'PY'
import json
import pathlib
import re
import subprocess
import sys

manifest_path = pathlib.Path(sys.argv[1])
repo_root = pathlib.Path(sys.argv[2])
data = json.loads(manifest_path.read_text(encoding="utf-8"))

expected = {
    "schema": "evergreen.siyuan-source/v1",
    "component_path": "siyuan",
}
for key, value in expected.items():
    if data.get(key) != value:
        raise SystemExit(f"invalid manifest {key}: {data.get(key)!r}")

source = data.get("source", {})
materialization = data.get("materialization", {})
checks = {
    "source.commit": (
        source.get("commit"),
        "dd67e7c98a2b4cf39d3029d50e8d1a069f7326bd",
    ),
    "source.tree": (
        source.get("tree"),
        "5fcc6ba8f4b422b879ff477642f4cbf883fc1436",
    ),
    "source.license": (source.get("license"), "AGPL-3.0-or-later"),
    "materialization.tracked_entries": (
        materialization.get("tracked_entries"),
        4935,
    ),
    "materialization.ls_tree_sha256": (
        materialization.get("ls_tree_sha256"),
        "e0e81065b1203db31ca3968f496f7fbfd4cf704dfe9031dea547355c956f198f",
    ),
    "materialization.path_list_sha256": (
        materialization.get("path_list_sha256"),
        "ae29a0ed9ce165f5ad75fa3d663ede292d3369395b339e02845f433908a4d5ce",
    ),
    "materialization.evergreen_import_commit": (
        materialization.get("evergreen_import_commit"),
        "83cf949fcde5293eba8c6e7423fe3baa2242301c",
    ),
    "materialization.evergreen_import_subtree": (
        materialization.get("evergreen_import_subtree"),
        "5fcc6ba8f4b422b879ff477642f4cbf883fc1436",
    ),
}
for name, (actual, expected_value) in checks.items():
    if actual != expected_value:
        raise SystemExit(f"invalid manifest {name}: {actual!r}")

expected_patches = sorted(data.get("integration_patches", []))
actual_patches = subprocess.check_output(
    ["git", "-C", str(repo_root), "diff", "--name-only",
     materialization["evergreen_import_commit"], "--", "siyuan"],
    text=True,
).splitlines()
if sorted(actual_patches) != expected_patches:
    raise SystemExit(
        "undeclared SiYuan integration patches:\n"
        f"expected={expected_patches}\nactual={sorted(actual_patches)}"
    )

for module_dir in (repo_root, repo_root / "siyuan" / "kernel"):
    module = json.loads(subprocess.check_output(
        ["go", "mod", "edit", "-json"], cwd=module_dir, text=True
    ))
    for replacement in module.get("Replace") or []:
        path = replacement["New"]["Path"]
        if path.startswith((".", "/")) or re.match(r"^[A-Za-z]:[\\/]", path):
            raise SystemExit(
                f"local module replace is forbidden in {module_dir}: {path}"
            )
PY

license_digest="$(shasum -a 256 "$repo_root/siyuan/LICENSE" | awk '{print $1}')"
[[ "$license_digest" == "4df3c306dddaaf4baffdff5ca820cc679ac8cd6dc263c6a74517783e42fa7a3b" ]] ||
  fail "siyuan/LICENSE differs from the fixed snapshot"

if grep -Eq 'github\.com/siyuan-note/siyuan/(releases/download|tree)/v' \
  "$repo_root/siyuan/SOURCE_CODE.md"; then
  fail "SOURCE_CODE.md still points at an independent upstream SiYuan release"
fi

gowork="$(cd "$repo_root" && go env GOWORK)"
[[ "$gowork" == "$repo_root/go.work" ]] || fail "Go did not select the root go.work"

core_dir="$(cd "$repo_root" &&
  go list -m -f '{{.Dir}}' github.com/ikaqiu-Lemon/EverGreen)"
kernel_dir="$(cd "$repo_root" &&
  go list -m -f '{{.Dir}}' github.com/siyuan-note/siyuan/kernel)"
[[ "$core_dir" == "$repo_root" ]] || fail "Evergreen module resolves outside the checkout: $core_dir"
[[ "$kernel_dir" == "$repo_root/siyuan/kernel" ]] ||
  fail "SiYuan kernel module resolves outside the checkout: $kernel_dir"

case "${1:-structure}" in
  structure)
    ;;
  --go)
    (cd "$repo_root" && go test ./pkg/evergreencore ./internal/client ./internal/txn)
    (cd "$repo_root/siyuan/kernel" &&
      go test -tags 'fts5 sqlcipher' ./evergreen ./filesys ./apicontract)
    ;;
  --frontend)
    ((${#pnpm_cmd[@]} > 0)) || fail "pnpm/corepack is required for frontend checks"
    "${pnpm_cmd[@]}" --dir "$repo_root/siyuan/app" run typecheck
    "${pnpm_cmd[@]}" --dir "$repo_root/siyuan/app" test
    ;;
  --focused)
    "$0" --go
    "$0" --frontend
    ;;
  *)
    fail "unknown mode $1 (expected structure, --go, --frontend, or --focused)"
    ;;
esac

printf 'siyuan monorepo verification: PASS (%s)\n' "${1:-structure}"
