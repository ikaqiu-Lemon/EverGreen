#!/usr/bin/env python3
"""规模棘轮门禁（defect_zeroing 批次 1 · 修 I-evergreen.system_assurance-158614-005）。

# 这条门禁在补什么洞

M2 / M3 / M4 阶段验收脚本里有一批「冻结基线算术等式」形式的断言（阶段内门禁条数、用例
条数、映射行数等于某个当时冻结的常数）。能力化重组后，那些等式的被计数对象已经没有同一
指称（阶段目录 → 能力目录、阶段聚合脚本 → 单支场景脚本），照搬只会得到与产品无关的红，
所以 `tests/archive/history/stage-acceptance/` 只做原文归档、不重放。

但「不重放」留下了一个真实缺口：**规模可以静默缩水**。四张清单表（inventory / coverage /
traceability / legacy_gates）只校验彼此自洽 —— 有人删掉 20 支 e2e，四张表照样自洽全绿。
历史等式的**意图**（规模不得静默下降）因此无人承接。

本工具把那个意图迁移到当前判据面，并换成**棘轮**（ratchet）而不是等式：

  * 等式（`== 常数`）在活跃演进的仓里必然产生与产品无关的红 —— 这正是历史等式被搁置的原因；
  * 棘轮（`>= 历史最高水位`）只在**缩水**时变红：新增测试自然通过（跑一次 `--write` 抬高
    水位线即可），删测试 / 降覆盖必须显式解释。

# 判据（任一不满足即非零）

  ① 盘面每个规模指标 >= `tests/manifest/scale_baseline.tsv` 的 `high_water`；
  ② 盘面出现清单里没有的新指标 → 红（新增维度必须入账，不能悄悄游离在棘轮之外）；
  ③ **已冻结维度从盘面消失 → 红**（`--check` 与 `--write` 都非零，且 `--write` 拒绝落盘）。
     这条曾是本工具最严重的假绿：初版只打印 `[warn] 盘面未测到该指标` 然后照样 PASS，
     于是"把一整类维度删空"（例如清空所有 fuzz suite，使 `suites_layer_fuzz` 键不再产生）
     可以零成本绕过棘轮 —— 缩水从"低于水位"变形成"维度消失"就不判了。
     现在维度消失与维度缩水同权：都判红，且 `--write` 不允许用落盘把消失固化下来。
  ④ 清单里 `high_water` < `initial`（冻结初值）→ 红；
  ⑤ `--write` 只能**抬高** high_water，遇到任何缩水值直接非零且**不写文件**。

# 能力边界（不夸大：哪些是机器判据，哪些只是治理约束）

**机器判据**（本工具真的会红）：上面 ①~⑤ 全部。特别是：命令行没有任何下调入口，
`--write` 在检测到缩水或维度消失时**原子地放弃写入**（不做部分落盘）。

**治理约束（非机器判据，靠 review 兜底）**：`scale_baseline.tsv` 是仓内纯文本，
有权限的人**可以手改** `high_water` / `initial`，本工具无法阻止 —— 任何"防篡改"记录
（校验和、锁文件、影子副本）都同样可被同一只手改掉，声称能防等于自欺。
本工具能做到的是让手改**不可能悄无声息**：
  * 改 `high_water` 到 `initial` 以下 → 判据 ④ 当场红；
  * 改 `initial` 本身 → 不红，但它是一行留在 `git diff` 里的显式修改，
    review 时必须要求给出理由（正当理由只有一种：度量口径本身重定义，且需在
    `note` 列写明并同批提交对应的口径变更）。
因此**不要**把本工具描述成"基线不可下降"；准确表述是"下降不可能静默发生"。

# 与历史结论的关系

历史阶段脚本、历史验收结论、历史 Task 一字不改（EPIC 边界）。本工具是**当前判据面**的
新增判据，不声称"历史阶段结论已在新体系复现"。

反证（永久、可复跑）：`tests/contract/scale-ratchet/negatives.sh`，覆盖
删除整类维度 / 换类补总数 / 正常新增抬水位 / 下调数值 / 手改水位线 / 新增未登记维度。

用法：
  python3 tests/tools/scale_ratchet.py --check    # 门禁（manifest / contract / core / full）
  python3 tests/tools/scale_ratchet.py --write    # 抬高水位线（新增测试后跑一次）
"""
from __future__ import annotations

import argparse
import csv
import sys
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parents[2]
MANIFEST = REPO / "tests/manifest"
BASELINE = MANIFEST / "scale_baseline.tsv"
HEADER = ["metric", "high_water", "initial", "source", "note"]

EXECUTABLE_LAYERS = ("unit", "integration", "e2e", "contract", "perf")


def read_tsv(path: Path) -> list[dict]:
    if not path.exists():
        return []
    with path.open() as fh:
        return list(csv.DictReader(fh, delimiter="\t", quoting=csv.QUOTE_NONE))


def measure() -> dict[str, tuple[int, str]]:
    """现场测量规模指标 → {metric: (value, source)}。全部从盘面派生，零硬编码常数。"""
    out: dict[str, tuple[int, str]] = {}

    suites = yaml.safe_load((MANIFEST / "suites.yaml").read_text())["suites"]
    out["suites_total"] = (len(suites), "tests/manifest/suites.yaml")
    out["suites_executable"] = (
        len([s for s in suites if s.get("layer") in EXECUTABLE_LAYERS]),
        "tests/manifest/suites.yaml")
    out["suites_required"] = (len([s for s in suites if s.get("required")]),
                              "tests/manifest/suites.yaml")
    per_layer: dict[str, int] = {}
    for s in suites:
        per_layer[s.get("layer", "-")] = per_layer.get(s.get("layer", "-"), 0) + 1
    for layer in sorted(per_layer):
        out[f"suites_layer_{layer}"] = (per_layer[layer], "tests/manifest/suites.yaml")

    inv = read_tsv(MANIFEST / "inventory.tsv")
    out["inventory_rows"] = (len(inv), "tests/manifest/inventory.tsv")
    out["inventory_cases"] = (sum(int(r["cases"]) for r in inv), "tests/manifest/inventory.tsv")

    trace = read_tsv(MANIFEST / "traceability.tsv")
    out["requirements_rows"] = (len(trace), "tests/manifest/traceability.tsv")
    out["requirements_mapped"] = (
        len([r for r in trace if r.get("mapping_status") == "mapped"]),
        "tests/manifest/traceability.tsv")
    for kind in sorted({r.get("kind", "-") for r in trace}):
        out[f"requirements_kind_{kind}"] = (
            len([r for r in trace if r.get("kind") == kind]),
            "tests/manifest/traceability.tsv")

    out["legacy_gate_assets"] = (len(read_tsv(MANIFEST / "legacy_gates.tsv")),
                                 "tests/manifest/legacy_gates.tsv")
    out["migration_rows"] = (len(read_tsv(MANIFEST / "migration.tsv")),
                             "tests/manifest/migration.tsv")
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--write", action="store_true")
    g.add_argument("--check", action="store_true")
    args = ap.parse_args()

    cur = measure()
    rows = {r["metric"]: r for r in read_tsv(BASELINE)}
    failures: list[str] = []

    # 已冻结维度从盘面消失 —— 两种模式下都是硬失败。
    # 缩水有两种形态：数值变小，和**维度整体不见**（把一整类 suite 删空，键就不再产生）。
    # 只判前者等于给后者留了一条零成本绕过路径，因此这里同权处理。
    vanished = [m for m in sorted(rows) if m not in cur]

    if args.write:
        if vanished:
            print("[FAIL] --write 拒绝落盘：以下已冻结维度在盘面消失（缩水的变形，不是缺表）：\n  "
                  + "\n  ".join(f"{m}（high_water={rows[m]['high_water']}，"
                                f"source={rows[m].get('source', '-')}）" for m in vanished)
                  + "\n  维度确需下线时，必须先在 review 里说明口径变更，再手工删除对应行；"
                    "本命令不提供把'消失'固化成新基线的入口。", file=sys.stderr)
            return 1
        merged: list[list[str]] = []
        lowered: list[str] = []
        for m in sorted(cur):
            value, src = cur[m]
            old = rows.get(m)
            if old is None:
                merged.append([m, str(value), str(value), src, "首次冻结（defect_zeroing 批次1）"])
                print(f"[新] {m}: initial=high_water={value}")
                continue
            hw, init = int(old["high_water"]), int(old["initial"])
            if value < hw:
                lowered.append(f"{m}: 盘面 {value} < high_water {hw}")
            merged.append([m, str(max(hw, value)), str(init), src, old.get("note", "")])
            if value > hw:
                print(f"[抬] {m}: {hw} → {value}")
        if lowered:
            # 先判后写：任何一条缩水都放弃整次落盘，不做部分写入。
            print("[FAIL] --write 只能抬高水位线，拒绝写入缩水值（baseline 未改动）：\n  "
                  + "\n  ".join(lowered), file=sys.stderr)
            return 1
        merged.sort(key=lambda r: r[0])
        BASELINE.write_text("\n".join("\t".join(r) for r in [HEADER] + merged) + "\n")
        print(f"[写] scale_baseline.tsv: {len(merged)} 个指标")
        return 0

    if not rows:
        print("[FAIL] 缺少 tests/manifest/scale_baseline.tsv（规模棘轮的唯一水位线来源）",
              file=sys.stderr)
        return 1

    for m in sorted(cur):
        value, _src = cur[m]
        old = rows.get(m)
        if old is None:
            failures.append(f"{m}: 盘面有该指标但棘轮清单未登记（新增维度必须入账，"
                            f"跑 --write 冻结初值；当前值 {value}）")
            continue
        hw, init = int(old["high_water"]), int(old["initial"])
        if hw < init:
            failures.append(f"{m}: high_water {hw} < initial {init}（水位线不得低于冻结初值）")
        if value < hw:
            failures.append(f"{m}: 盘面 {value} < 历史最高水位 {hw} —— 规模缩水必须显式解释，"
                            "不接受静默下降")
        else:
            print(f"  [ok] {m}: {value} >= {hw}（initial {init}）")
    for m in vanished:
        failures.append(f"{m}: 已冻结维度在盘面消失（high_water={rows[m]['high_water']}）"
                        " —— 整类维度被删空与数值缩水同权判红，不是 warning")

    if failures:
        for f in failures:
            print("[FAIL] " + f, file=sys.stderr)
        return 1
    print(f"[PASS] 规模棘轮通过（{len(cur)} 个指标全部不低于历史最高水位，零维度消失）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
