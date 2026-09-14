#!/usr/bin/env python3
"""迁移完备性机器校验（system_assurance · T-…-002 / 合同 D9）。

对 tests/manifest/migration.tsv 做「无遗漏、无幽灵、无重复、决策可核」的判据检查，
并顺带校验派生表（inventory / runtime_map / coverage）与盘面一致。

判据（任一不满足即退出 1）：
  C1 基线覆盖：基线提交下的每个测试资产都在 migration.tsv 有且只有一条决策行；
  C2 决策合法：decision ∈ {keep, merge, archive, relocate-product}；
  C3 落点存在：keep / archive / relocate-product 的 new_path 必须在盘面存在；
  C4 合并可核：merge 行的 new_path 必须存在，且目标文件里能检索到来源文件名（证明真的并进去了）；
  C5 旧位清空：所有 old_path 在盘面均不存在（历史 test/ 已退役）；
  C6 无重复落点：keep / relocate-product 的 new_path 唯一；
  C7 产品树纯净：cmd/ internal/ skill/ 零 _test.go；
  C8 归档有据：archive / merge 行 reason 非空；
  C9 派生表一致：inventory.py --check 通过。

基线来源：
  · tests/manifest/baseline_assets.tsv（包内快照，单仓和浅克隆均可跑）；
  · 快照缺失时直接失败，不依赖公开仓库之外的历史提交。

用法：cd evergreen && python3 tests/tools/check_migration.py [--json]
"""
from __future__ import annotations
import argparse, csv, json, re, subprocess, sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
MANIFEST = REPO / "tests/manifest"
VALID = {"keep", "merge", "archive", "relocate-product"}

def git(*a: str, check: bool = True) -> str:
    r = subprocess.run(["git", "-C", str(REPO), *a], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise RuntimeError(r.stderr.strip())
    return r.stdout


def baseline_assets() -> tuple[list[str], str]:
    snap = MANIFEST / "baseline_assets.tsv"
    if not snap.exists():
        raise SystemExit(f"[FAIL] 基线快照缺失：{snap}")
    rows = [l.split("\t")[0] for l in snap.read_text().splitlines()[1:] if l.strip()]
    return rows, "snapshot"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    rows = list(csv.DictReader((MANIFEST / "migration.tsv").read_text().splitlines(), delimiter="\t"))
    base, base_src = baseline_assets()
    # 与 inventory.py 同口径：盘面 = 工作树（已跟踪 + 未跟踪未 ignore）。
    # 裸 `ls-files` 会让 C5（旧位清空）/ C7（产品树纯净）在 `git add` 前后给出不同结论。
    on_disk = set(f for f in git("ls-files", "-c", "-o", "--exclude-standard").splitlines() if f)

    fails: list[str] = []
    notes: list[str] = []

    # C1 基线覆盖
    declared = [r["old_path"] for r in rows]
    missing = sorted(set(base) - set(declared))
    extra = sorted(set(declared) - set(base))
    if missing:
        fails.append(f"C1 基线资产未登记 {len(missing)} 条：{missing[:5]}")
    if extra:
        fails.append(f"C1 清单含非基线幽灵行 {len(extra)} 条：{extra[:5]}")

    # C1b 登记行去重：同一 (old_path,new_path) 对只能出现一次
    pairs = [(r["old_path"], r["new_path"]) for r in rows]
    dup_pair = sorted({p for p in pairs if pairs.count(p) > 1})
    if dup_pair:
        fails.append(f"C1b 重复登记 {len(dup_pair)} 条：{dup_pair[:3]}")

    # C1c 一个旧资产允许 1→N，但只能以「原文归档 + 有效断言并入」的形态展开：
    #     多行时每行 decision 必须 ∈ {archive, merge}，且 archive 至多一行；
    #     keep / relocate-product 仍是严格 1→1，不得与其它行共存。
    by_old: dict[str, list[str]] = {}
    for r in rows:
        by_old.setdefault(r["old_path"], []).append(r["decision"])
    for old, decs in sorted(by_old.items()):
        if len(decs) == 1:
            continue
        bad = sorted({d for d in decs if d not in ("archive", "merge")})
        if bad:
            fails.append(f"C1c {old} 有 {len(decs)} 行登记，但含 1→1 决策 {bad}（只允许 archive/merge 展开）")
        if decs.count("archive") > 1:
            fails.append(f"C1c {old} 出现 {decs.count('archive')} 行 archive（原文归档只能一处）")

    seen_new: dict[str, str] = {}
    for r in rows:
        old, new, dec = r["old_path"], r["new_path"], r["decision"]
        # C2
        if dec not in VALID:
            fails.append(f"C2 非法 decision={dec}（{old}）")
            continue
        # C3
        if dec in ("keep", "archive", "relocate-product") and new not in on_disk:
            fails.append(f"C3 落点不存在：{old} → {new}（{dec}）")
        # C4
        if dec == "merge":
            if new not in on_disk:
                fails.append(f"C4 合并目标不存在：{old} → {new}")
            else:
                tgt = (REPO / new).read_text(errors="ignore")
                stem = Path(old).name
                if stem not in tgt and Path(old).stem not in tgt:
                    fails.append(f"C4 合并目标未引用来源（无法自证并入）：{new} ← {stem}")
        # C5
        if old in on_disk:
            fails.append(f"C5 旧位仍在盘面：{old}")
        # C6
        if dec in ("keep", "relocate-product"):
            if new in seen_new:
                fails.append(f"C6 落点重复：{new}（{seen_new[new]} 与 {old}）")
            seen_new[new] = old
        # C8
        if dec in ("archive", "merge") and not r.get("reason", "").strip().strip("-"):
            fails.append(f"C8 {dec} 行缺少 reason：{old}")

    # C7 产品树纯净
    stray = [f for f in on_disk if f.endswith("_test.go") and f.split("/")[0] in ("cmd", "internal", "skill")]
    if stray:
        fails.append(f"C7 产品树仍有 _test.go：{stray[:5]}")

    # C9 派生表一致
    rc = subprocess.run([sys.executable, str(REPO / "tests/tools/inventory.py"), "--check"],
                        cwd=REPO, capture_output=True, text=True)
    if rc.returncode != 0:
        fails.append("C9 派生表与盘面漂移：" + rc.stdout.strip().replace("\n", "; "))

    stats = {
        "baseline_source": base_src,
        "baseline_assets": len(base),
        "migration_rows": len(rows),
        "decisions": {d: sum(1 for r in rows if r["decision"] == d) for d in sorted(VALID)},
        "failures": len(fails),
    }
    if args.json:
        print(json.dumps({**stats, "detail": fails}, ensure_ascii=False))
    else:
        print(f"基线资产 {len(base)}（来源 {base_src}）｜清单 {len(rows)} 行｜决策 {stats['decisions']}")
        for f in fails:
            print("  [FAIL] " + f, file=sys.stderr)
        for n in notes:
            print("  [note] " + n)
        print("[PASS] 迁移完备性 C1~C9 全部满足" if not fails else f"[FAIL] {len(fails)} 条判据未满足")
    return 1 if fails else 0


if __name__ == "__main__":
    sys.exit(main())
