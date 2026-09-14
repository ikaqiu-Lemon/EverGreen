#!/usr/bin/env python3
"""需求 / 命令 → suite 的**静态覆盖映射**（system_assurance · T-…-002 交付项）。

# 这张表是什么，不是什么

**是**：22 条 `eg` 命令与六条冻结合同 F1–F6 各自被哪些**可执行 suite** 引用，并给出一个
可点开的**具体断言锚点**（`路径:行号`）。它回答「需求 / 命令有没有落进测试资产」。

**不是**：执行证据。静态命中只能叫**覆盖映射**（列名 `mapping_status`），不能叫「已验证 /
已通过」。「跑没跑、绿不绿」由 **T-…-007 的统一验收报告**（full / race profile 实跑）证明，
本表不越权、不预告结论。`coverage.tsv` 只有文件数量，答不了「命令有没有被引用」；
本表补的正是这一格，仅此一格。

**也不是**：精确的"有效覆盖"计数。`suite_count` 是文本层命中数，会把写在字符串字面量里的
命令名一并计入（例如门禁自带的合成反例 `printf '"${EG}" search … '` 会让 CMD-search 多算一个
contract 层 suite）。这是刻意保留的宽松侧：宁可虚高一格，也不引入"哪些文件不算"的具名豁免。
需要"有效覆盖"口径时，以 T-…-007 的实跑用例数为准，不要拿本列当门槛。

# 两个被映射的面（都从事实派生，不手写清单）

  command  22 条 `eg` 命令 —— 从产品代码 `internal/cli/*.go` 的注册表（`Name:`）派生，
           并与挂载面（`Wire("…")`）双向比对：注册未挂载 / 挂载未注册都判漂移。
           匹配式由 `command_exec_regex()` 按命令名**机器生成**（shell 的 `eg … <cmd>`、
           Go 的 `run*( … "<cmd>" … )`），结构上宽不起来，不做反作弊限幅。
  frozen   六条冻结合同 F1–F6 —— 从**包内合同快照**（D8 单仓自足，不依赖 sibling
           teamwork 仓）解析，判据式逐条写在 `tests/manifest/requirements.tsv`。

# 反作弊（只针对手写判据）

  frozen 行的 matcher 是手写的，若命中超过 `OVER_BROAD_RATIO` 的 suite 判 `over-broad` 失败
  —— 防止用 `.` 之类宽正则把表刷满。命中为 0 只能靠 requirements.tsv 显式登记
  `gap_issue` 放行，状态写 `gap:<id>` 长期可见；没登记就是失败。

# 不在本表的东西

  `teamwork/projects/evergreen/s1_main_flow/tools/` 下 21 个历史门禁 / mutation / validate
  脚本由 `tests/tools/legacy_gates.py` 单独盘点（分类 / 依据 / 处置），不混进需求追溯。

用法：
  python3 tests/tools/traceability.py --write
  python3 tests/tools/traceability.py --check
"""
from __future__ import annotations

import argparse
import csv
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
MANIFEST = REPO / "tests/manifest"
INVENTORY = MANIFEST / "inventory.tsv"
REQUIREMENTS = MANIFEST / "requirements.tsv"
TRACEABILITY = MANIFEST / "traceability.tsv"

TECH_DESIGN = ("tests/fixtures/contracts/projects/evergreen/s1_main_flow/docs/specs/"
               "2026-08-31-evergreen-s1-tech-design.md")

EXEC_LAYERS = ("unit", "integration", "e2e", "contract", "perf")
OVER_BROAD_RATIO = 0.6

HEADER = ["req_id", "kind", "surface", "source_ref", "mapping_status", "suite_count",
          "layers", "anchor", "matcher", "suites"]


def read_tsv(path: Path) -> list[dict]:
    if not path.exists():
        return []
    with path.open() as fh:
        # QUOTE_NONE：字段里的 `"` 是**判据正则的字面量部分**（例如关系类型枚举面
        # `"support"`），被 csv 当引号剥掉会让判据从 21 个 suite 静默放宽到 61 个。
        return list(csv.DictReader(fh, delimiter="\t", quoting=csv.QUOTE_NONE))


def render(rows: list[list[str]]) -> str:
    return "\n".join("\t".join(r) for r in rows) + "\n"


# ---------------------------------------------------------------- 面的派生


def derive_commands() -> list[str]:
    registry: set[str] = set()
    wired: set[str] = set()
    for p in sorted((REPO / "internal/cli").glob("*.go")):
        if p.name.endswith("_test.go"):
            continue
        text = p.read_text()
        registry |= set(re.findall(r"^\s*Name:\s*\"([a-z][a-z-]*)\"", text, re.M))
        wired |= set(re.findall(r"Wire\(\"([a-z][a-z-]*)\"", text))
    if registry != wired:
        raise SystemExit("[FAIL] 命令面漂移：注册未挂载 = "
                         f"{sorted(registry - wired)}；挂载未注册 = {sorted(wired - registry)}")
    if not registry:
        raise SystemExit("[FAIL] 命令注册表零命中：internal/cli 结构变了，判据失效")
    return sorted(registry)


def derive_frozen() -> list[tuple[str, str]]:
    doc = REPO / TECH_DESIGN
    if not doc.exists():
        raise SystemExit(f"[FAIL] 包内合同快照缺失：{TECH_DESIGN}（D8 必需真源）")
    rows = re.findall(r"^\|\s*(F\d)\s*\|\s*(.+?)\s*\|\s*$", doc.read_text(), re.M)
    if len(rows) != 6:
        raise SystemExit(f"[FAIL] 冻结合同解析到 {len(rows)} 条，应为 6（F1–F6）")
    return rows


# ---------------------------------------------------------------- 资产索引


class Assets:
    """可执行层级的 go / shell 资产正文索引（suite 去重 + 断言锚点定位）。"""

    def __init__(self) -> None:
        self.items: list[tuple[str, str, str, str]] = []  # (suite, layer, path, text)
        for row in read_tsv(INVENTORY):
            if row["layer"] not in EXEC_LAYERS or row["kind"] not in ("go", "shell"):
                continue
            p = REPO / row["path"]
            if not p.is_file():
                continue
            try:
                self.items.append((row["suite_id"], row["layer"], row["path"], p.read_text()))
            except UnicodeDecodeError:
                continue
        self.items.sort(key=lambda i: i[2])
        self.total_suites = len({i[0] for i in self.items})

    def match(self, pattern: str) -> tuple[list[str], list[str], str]:
        """返回 (suites, layers, anchor)。

        anchor 取**第一个落在非注释行上的命中**（`路径:行号`）——注释里的提及不能当
        判据锚点；确实只在注释里出现时才退回首个命中，并由调用方的状态列自证。
        """
        rx = re.compile(pattern, re.M | re.S)
        suites, layers = set(), set()
        anchor, fallback = "", ""
        for suite, layer, path, text in self.items:
            hit = None
            for m in rx.finditer(text):
                line_no = text.count(chr(10), 0, m.start()) + 1
                line = text.split(chr(10))[line_no - 1].strip()
                if not fallback:
                    fallback = f"{path}:{line_no}"
                if line.startswith("//") or line.startswith("#"):
                    if hit is None:
                        hit = (path, line_no, False)
                    continue
                hit = (path, line_no, True)
                break
            if hit is None:
                continue
            suites.add(suite)
            layers.add(layer)
            if not anchor and hit[2]:
                anchor = f"{hit[0]}:{hit[1]}"
        return sorted(suites), sorted(layers), anchor or fallback or "-"


def command_exec_regex(cmd: str) -> str:
    """命令的引用形态：shell 的 `eg … <cmd>` 与 Go 的 `run*( … "<cmd>" … )`。"""
    c = re.escape(cmd)
    shell = (r"(?:^|[\s;&|(`$])(?:eg|eg_code|eg_json|eg_run|run_eg|\"\$\{EG\}\")"
             rf"(?:\s+-{{1,2}}[^\s]+(?:\s+[^\s-][^\s]*)?)*\s+{c}(?![\w-])")
    go = rf"run[A-Za-z]*\((?:[^()]|\([^()]*\)){{0,400}}?\"{c}\""
    return f"(?:{shell})|(?:{go})"


# ---------------------------------------------------------------- 组装


def build() -> list[list[str]]:
    commands = derive_commands()
    frozen = derive_frozen()
    reqs = {r["req_id"]: r for r in read_tsv(REQUIREMENTS)}
    assets = Assets()
    if assets.total_suites == 0:
        raise SystemExit("[FAIL] 可执行 suite 集合为空：inventory.tsv 或盘面异常")

    rows: list[list[str]] = []
    failures: list[str] = []

    def emit(req_id, kind, surface, source_ref, suites, layers, anchor, matcher,
             min_suites, gap_issue, curated, weak_note=""):
        status = "mapped"
        if curated and len(suites) > OVER_BROAD_RATIO * assets.total_suites:
            status = "over-broad"
            failures.append(f"{req_id}: matcher 命中 {len(suites)}/{assets.total_suites} 个 suite，"
                            f"超过 {OVER_BROAD_RATIO:.0%} —— 判据过宽，等于没判")
        elif len(suites) < min_suites:
            if gap_issue:
                status = f"gap:{gap_issue}"
            else:
                status = "unmapped"
                failures.append(f"{req_id}({surface}): 映射到 {len(suites)} 个 suite < "
                                f"min={min_suites}" + (f"；{weak_note}" if weak_note else "")
                                + " —— 未登记 gap_issue，不放行")
        rows.append([req_id, kind, surface, source_ref, status, str(len(suites)),
                     ";".join(layers) or "-", anchor, matcher, ";".join(suites) or "-"])

    for cmd in commands:
        req_id = f"CMD-{cmd}"
        cfg = reqs.get(req_id, {})
        rx = command_exec_regex(cmd)
        suites, layers, anchor = assets.match(rx)
        weak = ""
        if not suites:
            m_suites, _, _ = assets.match(rf"\"{re.escape(cmd)}\"")
            if m_suites:
                weak = f"仅 {len(m_suites)} 个 suite 出现裸字面量（mention-only，不算引用）"
        emit(req_id, "command", cmd, "internal/cli/*.go registry+Wire", suites, layers,
             anchor, rx, int(cfg.get("min_suites") or 1), cfg.get("gap_issue") or "",
             curated=False, weak_note=weak)

    for fid, _stmt in frozen:
        req_id = f"FROZEN-{fid}"
        cfg = reqs.get(req_id)
        if cfg is None:
            failures.append(f"{req_id}: requirements.tsv 未登记（冻结合同必须逐条给判据）")
            rows.append([req_id, "frozen", fid, TECH_DESIGN, "unregistered",
                         "0", "-", "-", "-", "-"])
            continue
        rx = cfg["evidence_regex"]
        suites, layers, anchor = assets.match(rx)
        emit(req_id, "frozen", fid, cfg.get("source_ref") or TECH_DESIGN, suites, layers,
             anchor, rx, int(cfg.get("min_suites") or 1), cfg.get("gap_issue") or "",
             curated=True)

    rows.sort(key=lambda r: (r[1], r[0]))
    if failures:
        for f in failures:
            print("[FAIL] " + f, file=sys.stderr)
        raise SystemExit(1)
    return [HEADER] + rows


def main() -> int:
    ap = argparse.ArgumentParser()
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--write", action="store_true")
    g.add_argument("--check", action="store_true")
    args = ap.parse_args()

    rows = build()
    text = render(rows)
    bad = 0
    if args.write:
        TRACEABILITY.write_text(text)
        print(f"[写] traceability.tsv: {len(rows)-1} 行")
    else:
        cur = TRACEABILITY.read_text() if TRACEABILITY.exists() else ""
        if cur != text:
            print("[FAIL] traceability.tsv 与盘面漂移（重跑 tests/tools/traceability.py --write）",
                  file=sys.stderr)
            bad = 1
        else:
            print(f"[ok] traceability.tsv: {len(rows)-1} 行与盘面一致")
    kinds: dict[str, int] = {}
    for r in rows[1:]:
        kinds[r[1]] = kinds.get(r[1], 0) + 1
    print("[面] " + "，".join(f"{k}={v}" for k, v in sorted(kinds.items()))
          + "；语义 = 静态覆盖映射，执行结论见 T-…-007 统一验收报告")
    # 收尾标记：统一 runner 用它证明"脚本真的跑到了最后"（合同 D6.2；退出码 0 不够）
    if not bad:
        print(f"[PASS] 需求 ↔ suite 追溯一致（{len(rows)-1} 行，零漂移）")
    return bad


if __name__ == "__main__":
    sys.exit(main())
