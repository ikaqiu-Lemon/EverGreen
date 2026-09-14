#!/usr/bin/env python3
"""变异测试执行器（system_assurance · T-…-005 · 合同 D5 `mutation` profile）。

# 为什么需要它

门禁跑绿只证明"跑过了"，不证明"还在检查"。判据退化（断言被注释、期望常量被改软、
curated 交叉约束被绕过）不会让门禁自己变红。本执行器把历史 4 个 mutation harness
（`mutation_test.py` / `m4_` / `m5_` / `m6_mutation_test.py`，legacy_gate_disposition.tsv
里 disposition=defer-005 的那四条）的**四拍机制**继承下来，靶子换成当前系统的判据：

  ① 基线：在**未变异**沙箱里跑 judge，必须绿。基线红 = 判据本身坏了，直接失败，
     不允许用"反正它红了"冒充杀死。
  ② 注入：在沙箱副本里施加变异（replace / append / create / delete-line / delete）。
  ③ 裁决：重跑同一 judge，**必须转非零**才算 killed；仍绿即 survived = 失败。
  ④ 零副作用：收尾对真实仓做 `git status --porcelain` + 逐文件 sha256 双验，
     任何差异一律失败 —— 变异测试绝不能改到真实工作树。

# 沙箱怎么造（为什么不是 cp -a 整棵树）

以真实仓 `git ls-files` 的**已跟踪文件**为准复制，随后在沙箱内 `git init && git add -A`
造一个独立仓。两个原因：

  · 判据里有 `git ls-files`（product_tree_selfcontained.sh / check_migration.py），
    沙箱没有 .git 就跑不出基线；
  · 不跟踪的垃圾（.tests-staging / _report / dist / 本地二进制）不进沙箱，
    避免"上一轮残骸"污染裁决 —— 这正是 D3.7 要根治的那类不确定性。

注入后再 `git add -A`：新增 / 删除文件必须让 `ls-files` 看得见，否则 violation-injection
类变异会因"判据看不到文件"而假杀。

用法：
  python3 tests/mutation/run_mutation.py --set core      # 受影响面 / 核心子集（默认）
  python3 tests/mutation/run_mutation.py --set all
  python3 tests/mutation/run_mutation.py --only MUT-EXIT5-REMOVE,MUT-E2E-SWALLOW
  python3 tests/mutation/run_mutation.py --set all --json

退出码：0 全部 killed 且真实仓零副作用；1 有存活 / 基线红 / 真实仓被改 / 清单自身不合法。
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
MUTANTS = REPO / "tests/mutation/mutants.yaml"
VALID_OPS = {"replace", "append", "create", "delete-line", "delete"}
JUDGE_TIMEOUT = int(os.environ.get("EG_MUTATION_TIMEOUT", "900"))


# ---------------------------------------------------------------- 基础设施

def die(msg: str) -> None:
    print(f"[FAIL] {msg}", file=sys.stderr)
    raise SystemExit(1)


def sh(cmd: list[str] | str, cwd: Path, timeout: int = JUDGE_TIMEOUT) -> tuple[int, str]:
    """跑命令，回 (rc, 合并输出)。字符串形式走 bash -c（judge 是命令行字面量）。"""
    shell = isinstance(cmd, str)
    try:
        r = subprocess.run(cmd if not shell else ["bash", "-c", cmd], cwd=str(cwd),
                           capture_output=True, text=True, timeout=timeout)
    except subprocess.TimeoutExpired:
        return 124, f"<timeout {timeout}s>"
    return r.returncode, (r.stdout + r.stderr)


DISPOSITION = REPO / "tests/manifest/legacy_gate_disposition.tsv"


def check_legacy_mapping(muts: list[dict], mapping: list[dict]) -> list[str]:
    """A2 的可核对面：4 个历史 harness 的变异点族必须被当前清单逐族承接。

    判据故意从 `legacy_gate_disposition.tsv` 现场读 defer-005 全集，而不是把 4 个文件名
    写死在这里 —— 迁移表改了、这里没跟上，必须当场失败，否则"可核对"就是自说自话。
    """
    fails: list[str] = []
    if not DISPOSITION.exists():
        return [f"缺 {DISPOSITION.relative_to(REPO)}：无法核对与 4 个历史 harness 的对应关系"]
    deferred = set()
    rows = DISPOSITION.read_text().splitlines()
    hdr = rows[0].split("\t")
    ci_script, ci_disp = hdr.index("script"), hdr.index("disposition")
    for ln in rows[1:]:
        f = ln.split("\t")
        if len(f) > ci_disp and f[ci_disp].strip() == "defer-005":
            deferred.add(f[ci_script].strip())
    listed = {m.get("script") for m in mapping}
    if listed != deferred:
        fails.append(f"legacy_mapping 覆盖的脚本 {sorted(listed)} ≠ 迁移表 defer-005 全集 "
                     f"{sorted(deferred)}（漏一个就等于漏一族变异点）")
    by_id = {m["id"]: m for m in muts}
    referenced: set[str] = set()
    for entry in mapping:
        s = entry.get("script", "<no-script>")
        fams = entry.get("families") or []
        cov = entry.get("covered_by") or []
        if not fams:
            fails.append(f"{s}: 未写 families（变异点族是承接关系的依据）")
        if not cov:
            fails.append(f"{s}: covered_by 为空 —— 该 harness 的变异点族无人承接")
        for mid in cov:
            if mid not in by_id:
                fails.append(f"{s}: covered_by 引用了不存在的变异体 {mid}")
                continue
            referenced.add(mid)
        have = {by_id[mid]["family"] for mid in cov if mid in by_id}
        for fam in fams:
            if fam not in have:
                fails.append(f"{s}: family {fam!r} 无同族变异体承接"
                             f"（covered_by 实际提供 {sorted(have)}）")
    orphans = sorted(set(by_id) - referenced)
    if orphans:
        fails.append(f"孤儿变异体 {orphans}：不在任何 legacy_mapping 里，"
                     "承接关系说不清（要么补 mapping，要么说明它是新增族）")
    return fails


def load_mutants() -> list[dict]:
    try:
        import yaml  # PyYAML
    except ImportError:
        die("需要 PyYAML 解析 tests/mutation/mutants.yaml（pip install pyyaml）")
    doc = yaml.safe_load(MUTANTS.read_text()) or {}
    muts = doc.get("mutants") or []
    if not muts:
        die("mutants.yaml 里没有变异体：空清单不构成证据")
    seen: set[str] = set()
    for m in muts:
        mid = m.get("id", "<no-id>")
        for k in ("id", "family", "target", "op", "judge", "why"):
            if not str(m.get(k) or "").strip():
                die(f"{mid}: 缺字段 {k}（清单本身不合法）")
        if m["op"] not in VALID_OPS:
            die(f"{mid}: op={m['op']!r} 非法（{sorted(VALID_OPS)}）")
        if m["op"] == "replace" and not (m.get("find") and m.get("replace") is not None):
            die(f"{mid}: op=replace 必须给 find / replace")
        if m["op"] in ("append", "create") and not m.get("payload"):
            die(f"{mid}: op={m['op']} 必须给 payload")
        if m["op"] == "delete-line" and not m.get("find"):
            die(f"{mid}: op=delete-line 必须给 find")
        if mid in seen:
            die(f"{mid}: id 重复")
        seen.add(mid)

    fails = check_legacy_mapping(muts, doc.get("legacy_mapping") or [])
    if fails:
        for f in fails:
            print("[FAIL] " + f, file=sys.stderr)
        die("legacy_mapping 与迁移表 / 变异体清单不自洽（A2 要求可核对）")
    return muts


# ---------------------------------------------------------------- 真实仓指纹（零副作用双验）

def tracked_files() -> list[str]:
    """已跟踪 + 未跟踪但未被 ignore 的文件。

    刻意包含未跟踪文件：本轮新增的测试资产（尚未 commit）也必须进沙箱，
    否则清单类判据会因"沙箱少了几个文件"而基线红，制造假信号；
    同时它们也纳入零副作用指纹，改到工作树同样会被抓出来。
    """
    rc, out = sh(["git", "ls-files", "-c", "-o", "--exclude-standard"], REPO)
    if rc != 0:
        die("真实仓 git ls-files 失败：无法建立零副作用基线")
    return sorted({l for l in out.splitlines() if l})


def fingerprint(files: list[str]) -> dict[str, str]:
    fp = {}
    for rel in files:
        p = REPO / rel
        if p.is_file():
            fp[rel] = hashlib.sha256(p.read_bytes()).hexdigest()
    return fp


def porcelain() -> str:
    _rc, out = sh(["git", "status", "--porcelain"], REPO)
    return out.strip()


# ---------------------------------------------------------------- 沙箱

def build_pristine(files: list[str], root: Path) -> Path:
    """按已跟踪文件清单复制出一个干净沙箱，并在其中造独立 git 仓。"""
    box = root / "pristine"
    for rel in files:
        src = REPO / rel
        if not src.is_file():
            continue
        dst = box / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(src, dst)
    for cmd in (["git", "init", "-q"],
                ["git", "add", "-A"],
                ["git", "-c", "user.name=mut", "-c", "user.email=mut@local",
                 "commit", "-q", "-m", "mutation sandbox base"]):
        rc, out = sh(cmd, box)
        if rc != 0:
            die(f"沙箱 git 初始化失败（{' '.join(cmd)}）：{out.strip()[:400]}")
    return box


def clone_box(pristine: Path, dest: Path) -> Path:
    shutil.copytree(pristine, dest, symlinks=True)
    return dest


def apply_mutation(box: Path, m: dict) -> str:
    """在沙箱内施加变异；回一句人读的施加说明。锚点不存在 = 清单过期，直接失败。"""
    mid, op = m["id"], m["op"]
    tgt = box / m["target"]
    if op == "create":
        if tgt.exists():
            die(f"{mid}: op=create 但目标已存在（{m['target']}）—— 清单与盘面漂移")
        tgt.parent.mkdir(parents=True, exist_ok=True)
        tgt.write_text(m["payload"])
        return f"create {m['target']}（{len(m['payload'])}B）"
    if not tgt.is_file():
        die(f"{mid}: 变异目标不存在（{m['target']}）—— 清单与盘面漂移，先修清单")
    if op == "delete":
        tgt.unlink()
        return f"delete {m['target']}"
    if op == "append":
        with tgt.open("a") as fh:
            fh.write(m["payload"])
        return f"append → {m['target']}"
    text = tgt.read_text()
    if op == "replace":
        if m["find"] not in text:
            die(f"{mid}: 锚点未命中（{m['target']} 内找不到 {m['find']!r}）—— 清单过期")
        tgt.write_text(text.replace(m["find"], m["replace"], 1))
        return f"replace 首处 {m['find']!r} → {m['replace']!r}"
    # delete-line
    lines = text.splitlines(keepends=True)
    hit = [i for i, l in enumerate(lines) if m["find"] in l]
    if not hit:
        die(f"{mid}: 锚点未命中（{m['target']} 内无含 {m['find']!r} 的行）—— 清单过期")
    dropped = lines.pop(hit[0]).strip()
    tgt.write_text("".join(lines))
    return f"delete-line {dropped[:80]}"


def reindex(box: Path) -> None:
    """让沙箱 git 索引看见新增 / 删除：判据里有 git ls-files。"""
    sh(["git", "add", "-A"], box)


# ---------------------------------------------------------------- 主流程

def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--set", dest="subset", choices=["core", "all"], default="core",
                    help="core=受影响面 / 核心子集（默认）；all=全清单")
    ap.add_argument("--only", default="", help="逗号分隔的变异体 id，覆盖 --set")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--keep-sandbox", action="store_true", help="排障用：保留沙箱不删")
    args = ap.parse_args()

    muts = load_mutants()
    if args.only:
        want = [s.strip() for s in args.only.split(",") if s.strip()]
        known = {m["id"] for m in muts}
        for w in want:
            if w not in known:
                die(f"--only 里的 {w} 不在清单中")
        muts = [m for m in muts if m["id"] in want]
    elif args.subset == "core":
        muts = [m for m in muts if m.get("core")]

    files = tracked_files()
    before_fp, before_status = fingerprint(files), porcelain()

    root = Path(tempfile.mkdtemp(prefix="eg-mutation-"))
    results: list[dict] = []
    baseline_cache: dict[str, tuple[int, str]] = {}
    t0 = time.time()
    try:
        pristine = build_pristine(files, root)

        for m in muts:
            mid, judge = m["id"], m["judge"]
            # ① 基线：judge 在未变异沙箱必须绿（同一 judge 只跑一次）
            if judge not in baseline_cache:
                baseline_cache[judge] = sh(judge, pristine)
            brc, bout = baseline_cache[judge]
            if brc != 0:
                results.append({"id": mid, "verdict": "BASELINE-RED", "judge": judge,
                                "baseline_rc": brc, "tail": bout.strip()[-400:]})
                print(f"  [BASELINE-RED] {mid}  judge={judge}  rc={brc}")
                continue

            # ② 注入 → ③ 裁决
            box = clone_box(pristine, root / mid)
            how = apply_mutation(box, m)
            reindex(box)
            rc, out = sh(judge, box)
            verdict = "KILLED" if rc != 0 else "SURVIVED"
            results.append({"id": mid, "family": m["family"], "target": m["target"],
                            "op": m["op"], "judge": judge, "applied": how,
                            "baseline_rc": brc, "mutant_rc": rc, "verdict": verdict,
                            "tail": out.strip()[-400:]})
            mark = "✓" if verdict == "KILLED" else "✗"
            print(f"  [{verdict}] {mark} {mid}  rc {brc}→{rc}  judge={judge}")
            if verdict == "SURVIVED":
                print(f"      {how}")
            if not args.keep_sandbox:
                shutil.rmtree(box, ignore_errors=True)
    finally:
        if not args.keep_sandbox:
            shutil.rmtree(root, ignore_errors=True)
        else:
            print(f"  [note] 沙箱保留于 {root}")

    # ④ 真实仓零副作用双验
    after_fp, after_status = fingerprint(tracked_files()), porcelain()
    side: list[str] = []
    if after_status != before_status:
        side.append("git status 变化（真实仓工作树被改动）")
    changed = sorted(k for k in set(before_fp) | set(after_fp)
                     if before_fp.get(k) != after_fp.get(k))
    if changed:
        side.append(f"{len(changed)} 个已跟踪文件 sha256 变化：{changed[:5]}")

    killed = [r for r in results if r["verdict"] == "KILLED"]
    survived = [r for r in results if r["verdict"] == "SURVIVED"]
    baseline_red = [r for r in results if r["verdict"] == "BASELINE-RED"]
    ok = not survived and not baseline_red and not side and results

    summary = {"selected": len(muts), "run": len(results), "killed": len(killed),
               "survived": [r["id"] for r in survived],
               "baseline_red": [r["id"] for r in baseline_red],
               "real_repo_side_effects": side,
               "elapsed_s": round(time.time() - t0, 1),
               "subset": args.only or args.subset,
               "pass": bool(ok)}
    if args.json:
        print(json.dumps({**summary, "detail": results}, ensure_ascii=False, indent=2))
    else:
        print(f"\n变异 {len(results)} 个｜killed {len(killed)}｜survived {len(survived)}"
              f"｜baseline-red {len(baseline_red)}｜{summary['elapsed_s']}s")
        print(f"真实仓零副作用：{'OK' if not side else '违规 → ' + '; '.join(side)}")
        for r in survived:
            print(f"  [FAIL] {r['id']} 存活：{r['judge']} 在变异后仍退 0 —— 该判据已退化",
                  file=sys.stderr)
        for r in baseline_red:
            print(f"  [FAIL] {r['id']} 基线红：{r['judge']} 在未变异沙箱即退"
                  f" {r['baseline_rc']} —— 先修判据本身", file=sys.stderr)
        for s in side:
            print(f"  [FAIL] 真实仓副作用：{s}", file=sys.stderr)
        print("[PASS] 全部变异被杀死，真实仓零副作用" if ok
              else f"[FAIL] mutation profile 未通过")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
