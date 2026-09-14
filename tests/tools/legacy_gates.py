#!/usr/bin/env python3
"""teamwork 侧 21 个 Evergreen 历史门禁 / mutation / validate 资产的盘点与处置
（system_assurance · T-…-002 盘点交付，迁移执行分工见各行 disposition）。

# 盘点对象（不是别的东西）

`teamwork/projects/evergreen/s1_main_flow/tools/*.py` —— Evergreen 自己的历史验证资产：
阶段门禁（`m2/m3/m4/m5/m6_final_gate.py`、`round3/4/5_final_gate.py`）、变异门禁
（`mutation_test.py`、`m4/m5/m6_mutation_test.py`）、规划校验（`validate_m*_tasks.py`、
`independent_schedule_check.py`、`derive_eg_baseline.py`、`gen_m4_tasks.py`）与共用模块
（`gate_common.py`）。

**明确不是** teamwork CLI 自身的 `tools/teamwork/teamwork/*.py`（那是协作平台工具，
与 Evergreen 测试体系无关），也不是 teamwork 自带 pytest。

# 三个分类由机器判据给出，不靠主观判断

  cross_repo_read     脚本是否跨仓读 Evergreen 工作树（`os.path.join("..","evergreen")`、
                      `EG = …/"evergreen"`、`../evergreen` glob / symlink）。
  product_path_hits   是否命中 Evergreen **产品面**路径（`internal/`、`cmd/eg`、`skill/`、
                      `SKILL.md`、`*.go`、`test/e2e/`）。
  harness             是否具备「注入变异 + 复跑」的行为形态：把 `.replace()` / `re.sub()`
                      的结果**写回文件**（`write(... .replace(...))`）且用 subprocess 复跑。
                      这是行为判据，不看文件名 —— 一次性生成器（写的是新文本、subprocess
                      只跑 `git diff`）因此不会被误判成变异 harness。

  分类规则（可复算，按处置归属分层）：
    mutation-harness    harness 为真 —— 注入变异并复跑，整体归 T-…-005。
    product-assertions  cross_repo_read 且 product_path_hits > 0
                        —— 断言面伸进 Evergreen 产品树，含**有效产品断言**，要提取。
    governance-only     其余（只读 teamwork 规划产物：task / EPIC / milestone / 排期）

# 处置（curated 输入 + 机器校验）

`tests/manifest/legacy_gate_disposition.tsv` 逐条登记 disposition / target_suites /
rationale，本工具校验：

  * 21 个脚本一个不漏（缺一即失败）；
  * disposition ∈ {assertions-migrated, defer-005, archive-in-place}；
  * `assertions-migrated` 必须给 target_suites，且每个 suite 都在 inventory.tsv 的
    suite 集合里（写一个不存在的 suite 换绿会当场失败）；
  * `mutation-harness` 只能 defer-005（变异 / perf 门禁归 T-…-005）；
  * `governance-only` 只能 archive-in-place 且不得挂 target_suites
    —— 纯治理材料**原地保留原文**（不复制进 evergreen 仓、不改写、不删除），
    它们校验的是 teamwork 规划产物，迁进 evergreen 测试树没有意义。

# 单仓自足（D8）

sibling teamwork 在场时**现场派生**并与包内快照逐字比对（漂移即失败）；不在场时以包内
快照 `tests/manifest/legacy_gates.tsv` 为必需真源（含 sha256 / 行数，可验证一致性）。

用法：
  python3 tests/tools/legacy_gates.py --write     # sibling 在场时刷新快照
  python3 tests/tools/legacy_gates.py --check     # 盘面/快照比对（CI 与领域验收消费）
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
MANIFEST = REPO / "tests/manifest"
INVENTORY = MANIFEST / "inventory.tsv"
DISPOSITION = MANIFEST / "legacy_gate_disposition.tsv"
SNAPSHOT = MANIFEST / "legacy_gates.tsv"

TOOLS_REL = "projects/evergreen/s1_main_flow/tools"
EXPECTED_COUNT = 21

CROSS_REPO_RE = re.compile(r"""os\.path\.join\(\s*["']\.\.["']\s*,\s*["']evergreen["']|"""
                           r"""["']\.\./evergreen|\.\.[/\\]evergreen|"""
                           r"""=\s*os\.path\.join\([^\n]*["']evergreen["']\)""")
PRODUCT_PATH_RE = re.compile(r"internal/|cmd/eg|skill/|SKILL\.md|test/e2e/|\.go[\"'\s)]")
RUNS_GATE_RE = re.compile(r"subprocess\.(?:run|check_call|check_output|Popen)")
MUTATE_WRITE_RE = re.compile(r"(?:write_text|\.write)\([^)]*(?:\.replace\(|re\.sub\()")

VALID_DISPOSITIONS = {"assertions-migrated", "defer-005", "archive-in-place"}

# EPIC 的四类归属（T-…-005 DoR 第 1 条）。disposition 说"怎么处置"，epic_class 说"属于哪一类"，
# 两者不是一对一：c（规划结构自检）与 d（历史排期 / 冻结基线）**处置相同**（原地归档），
# 但性质不同 —— 前者是仍在用的规划工具，后者是已封存的历史结论。合成一列会把这个区别抹掉，
# A6 的"无未分类"也就无从校验。
VALID_CLASSES = {"a", "b", "c", "d"}
CLASS_DESC = {"a": "当前静态合同门禁（有效断言并入 tests/contract/**）",
              "b": "mutation 判据（机制继承到 tests/mutation/）",
              "c": "规划结构自检（留 teamwork 侧作规划工具）",
              "d": "历史排期 / 冻结基线 / 阶段结论（原地封存，仅作历史材料）"}
# 每一类允许的 disposition：交叉约束，防"改一列换绿"。
CLASS_DISPOSITION = {"a": {"assertions-migrated"}, "b": {"defer-005"},
                     "c": {"archive-in-place"}, "d": {"archive-in-place"}}

HEADER = ["script", "lines", "sha256", "klass", "cross_repo_read", "product_path_hits",
          "harness", "epic_class", "disposition", "target_suites", "rationale"]


def read_tsv(path: Path) -> list[dict]:
    if not path.exists():
        return []
    with path.open() as fh:
        return list(csv.DictReader(fh, delimiter="\t", quoting=csv.QUOTE_NONE))


def render(rows: list[list[str]]) -> str:
    return "\n".join("\t".join(r) for r in rows) + "\n"


def sibling_tools() -> Path | None:
    cand = REPO.parent / "teamwork" / TOOLS_REL
    return cand if cand.is_dir() else None


def classify(text: str) -> tuple[str, bool, int, bool]:
    cross = bool(CROSS_REPO_RE.search(text))
    hits = len(PRODUCT_PATH_RE.findall(text))
    harness = bool(RUNS_GATE_RE.search(text)) and bool(MUTATE_WRITE_RE.search(text))
    if harness:
        return "mutation-harness", cross, hits, harness
    if cross and hits > 0:
        return "product-assertions", cross, hits, harness
    return "governance-only", cross, hits, harness


def derive(tools: Path) -> list[list[str]]:
    rows = []
    for p in sorted(tools.glob("*.py")):
        text = p.read_text(errors="replace")
        klass, cross, hits, harness = classify(text)
        rows.append([p.name, str(text.count("\n")),
                     hashlib.sha256(p.read_bytes()).hexdigest(),
                     klass, "yes" if cross else "no", str(hits),
                     "yes" if harness else "no"])
    return rows


def suite_universe() -> set[str]:
    return {r["suite_id"] for r in read_tsv(INVENTORY) if r["suite_id"] != "-"}


def build() -> tuple[list[list[str]], list[str]]:
    notes: list[str] = []
    disp = {r["script"]: r for r in read_tsv(DISPOSITION)}
    tools = sibling_tools()
    snap = read_tsv(SNAPSHOT)

    if tools is not None:
        facts = derive(tools)
        notes.append(f"sibling teamwork 在场：{len(facts)} 个历史资产现场派生")
        if snap:
            snap_facts = [[r[k] for k in HEADER[:7]] for r in snap]
            if snap_facts != facts:
                drift = [f[0] for f, s in zip(facts, snap_facts) if f != s] or ["<行数/集合变化>"]
                raise SystemExit("[FAIL] 历史资产快照与 sibling 现场漂移："
                                 + "，".join(drift) + "（跑 --write 刷新快照）")
    elif snap:
        facts = [[r[k] for k in HEADER[:7]] for r in snap]
        notes.append(f"sibling teamwork 不在场：以包内快照为必需真源（{len(facts)} 个，D8）")
    else:
        raise SystemExit("[FAIL] 既无 sibling teamwork 也无包内快照，无法盘点历史门禁资产")

    if len(facts) != EXPECTED_COUNT:
        raise SystemExit(f"[FAIL] 历史资产 {len(facts)} 个，任务口径为 {EXPECTED_COUNT} 个："
                         "范围与 T-…-002 不一致，先对齐范围再盘点")

    universe = suite_universe()
    failures: list[str] = []
    rows: list[list[str]] = []
    for fact in facts:
        name, _lines, _sha, klass = fact[0], fact[1], fact[2], fact[3]
        d = disp.get(name)
        if d is None:
            failures.append(f"{name}: legacy_gate_disposition.tsv 未登记（21 个必须逐条有处置与依据）")
            continue
        cls = (d.get("epic_class") or "").strip()
        dv = (d.get("disposition") or "").strip()
        targets = [s for s in (d.get("target_suites") or "").split(";") if s and s != "-"]
        rationale = (d.get("rationale") or "").strip()
        if dv not in VALID_DISPOSITIONS:
            failures.append(f"{name}: disposition={dv!r} 非法（{sorted(VALID_DISPOSITIONS)}）")
        if cls not in VALID_CLASSES:
            failures.append(f"{name}: epic_class={cls!r} 非法或未分类"
                            f"（必须 ∈ {sorted(VALID_CLASSES)}，A6 不允许留空）")
        elif dv in VALID_DISPOSITIONS and dv not in CLASS_DISPOSITION[cls]:
            failures.append(f"{name}: 归属 {cls} 只能配 disposition"
                            f" {sorted(CLASS_DISPOSITION[cls])}，实为 {dv!r}")
        # 形状交叉校验：d 类必须是阶段 / 轮次终审门禁本体，c 类必须不是。
        # 这样"把一个仍在用的规划工具偷偷记成已封存历史结论"会当场失败。
        if cls == "d" and not name.endswith("_final_gate.py"):
            failures.append(f"{name}: 归属 d（历史阶段结论）但文件名不是 *_final_gate.py")
        if cls == "c" and name.endswith("_final_gate.py"):
            failures.append(f"{name}: 归属 c（规划结构自检）但文件名是 *_final_gate.py"
                            "（阶段终审属 a / d）")
        if not rationale:
            failures.append(f"{name}: rationale 为空 —— 处置必须逐条有依据")
        if dv == "defer-005" and "005" not in rationale:
            failures.append(f"{name}: defer-005 的 rationale 必须写明归属（引用 T-…-005）")
        if dv == "assertions-migrated":
            if not targets:
                failures.append(f"{name}: assertions-migrated 必须给 target_suites")
            for s in targets:
                if s not in universe:
                    failures.append(f"{name}: target_suite {s!r} 不在 inventory.tsv 的 suite 集合里")
        if klass == "mutation-harness":
            if dv != "defer-005":
                failures.append(f"{name}: 机器判据为 mutation-harness，只能 defer-005（归 T-…-005）")
            if cls != "b":
                failures.append(f"{name}: 机器判据为 mutation-harness，归属只能是 b，实为 {cls!r}")
        if klass == "governance-only":
            if dv != "archive-in-place":
                failures.append(f"{name}: 机器判据为 governance-only，只能 archive-in-place")
            if targets:
                failures.append(f"{name}: governance-only 不得挂 target_suites（纯治理材料原地保留）")
        rows.append(fact + [cls, dv, ";".join(targets) or "-", rationale])

    if failures:
        for f in failures:
            print("[FAIL] " + f, file=sys.stderr)
        raise SystemExit(1)
    return [HEADER] + rows, notes


def main() -> int:
    ap = argparse.ArgumentParser()
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--write", action="store_true")
    g.add_argument("--check", action="store_true")
    args = ap.parse_args()

    rows, notes = build()
    text = render(rows)
    bad = 0
    if args.write:
        SNAPSHOT.write_text(text)
        print(f"[写] legacy_gates.tsv: {len(rows)-1} 行")
    else:
        cur = SNAPSHOT.read_text() if SNAPSHOT.exists() else ""
        if cur != text:
            print("[FAIL] legacy_gates.tsv 与盘面漂移（重跑 tests/tools/legacy_gates.py --write）",
                  file=sys.stderr)
            bad = 1
        else:
            print(f"[ok] legacy_gates.tsv: {len(rows)-1} 行与盘面一致")
    ik, ic, idv = HEADER.index("klass"), HEADER.index("epic_class"), HEADER.index("disposition")
    dist: dict[str, int] = {}
    per_class: dict[str, int] = {}
    for r in rows[1:]:
        dist[f"{r[ik]}/{r[idv]}"] = dist.get(f"{r[ik]}/{r[idv]}", 0) + 1
        per_class[r[ic]] = per_class.get(r[ic], 0) + 1
    print("[分类/处置] " + "，".join(f"{k}={v}" for k, v in sorted(dist.items())))
    # 四类归属计数（T-…-005 DoD 要求写进 Activity Log 的那一行）
    print("[四类归属] " + "，".join(f"{k}={per_class.get(k, 0)}（{CLASS_DESC[k]}）"
                                   for k in sorted(VALID_CLASSES)))
    for n in notes:
        print("[注] " + n)
    # 收尾标记：统一 runner 用它证明"脚本真的跑到了最后"（合同 D6.2）
    if not bad:
        print(f"[PASS] 历史门禁资产分类 / 处置 / 四类归属三向一致（{len(rows)-1} 项）")
    return bad


if __name__ == "__main__":
    sys.exit(main())
