#!/usr/bin/env python3
"""`tests/manifest/suites.yaml` 的生成与自检（system_assurance · T-…-006，合同 D4）。

`suites.yaml` 是 runner 的**唯一**输入，因此它自己必须是可核对的，否则"清单驱动"
只是把硬编码搬了个地方。本工具提供三件事：

  --write      从盘面（inventory.tsv + 元层可执行资产）重建清单骨架，
               **保留**人工决策字段（profiles / required / not_run_when / issue /
               exclude_cases / timeout_s / evidence_re / args / min_cases）。
  --check      双向核对 + schema 自检（manifest profile 消费）：
                 1. inventory.tsv 里每个 suite_id 恰有一条 origin=inventory 的登记
                    （孤儿：在盘未登记；幽灵：登记不在盘）；
                 2. 每个**元层可执行资产**（tools/mutation/fuzz 下的 .py/.sh）恰被
                    一条 origin=tool 的 suite 认领 —— 新增一支工具却不登记即非零；
                 3. layer / capability 与 inventory 一致（不许在清单里改写归属）；
                 4. 复用 runner 的 validate()：id 唯一、profile 合法、required/NOT_RUN
                    语义、min_cases 与盘面用例数一致、幽灵 target、阶段聚合禁令；
                 5. coverage.tsv 的每个能力至少有一个 suite（D9 覆盖不减）；
                 6. 每个 profile 至少有一个 suite（死信 profile 也是漂移）。
  --calibrate  从一次真实 run 的 summary.json 回填 shell/python suite 的 min_cases
               下限（= 该次实测证据标记数）。**只回填、不放宽**：新值低于旧值时保留旧值，
               否则一次偶发少跑就会把门槛调低——那正是 D6.4 禁止的"调低门槛换绿"。

Go suite 的 min_cases 不是人工填的：由 `tests/_staged/<pkg>/*_test.go` 里
`func Test…/Fuzz…` 的数量减去 `exclude_cases` 现场推导，因此新增用例不同步清单会直接非零。
"""
from __future__ import annotations

import argparse
import csv
import json
import re
import subprocess
import sys
from collections import Counter, defaultdict
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
MANIFEST = REPO / "tests/manifest"
SUITES_YAML = MANIFEST / "suites.yaml"
sys.path.insert(0, str(REPO / "tests/runner"))
import run as runner  # noqa: E402  （复用 runner 的 schema 校验，避免两套判据漂移）

CAP_LAYERS = ("unit", "integration", "e2e", "contract", "perf")
# 元层可执行资产 → suite 登记。左边是盘面事实（必须在盘），右边是**唯一**认领者。
# 这张表就是"元层资产的登记处"：新增一支工具而不在这里登记，--check 会报未认领。
META_SUITES = [
    # id                        target                              kind      layer      args
    ("manifest.inventory", "tests/tools/inventory.py", "python", "manifest", ["--check"]),
    ("manifest.migration", "tests/tools/check_migration.py", "python", "manifest", []),
    ("manifest.traceability", "tests/tools/traceability.py", "python", "manifest", ["--check"]),
    ("manifest.legacy-gates", "tests/tools/legacy_gates.py", "python", "manifest", ["--check"]),
    ("manifest.suites", "tests/tools/suites.py", "python", "manifest", ["--check"]),
    # 规模棘轮：承接历史阶段「冻结算术等式」的意图（规模不得静默缩水），
    # 修 I-evergreen.system_assurance-158614-005。判据见 tests/tools/scale_ratchet.py 头注释。
    ("manifest.scale-ratchet", "tests/tools/scale_ratchet.py", "python", "manifest", ["--check"]),
    ("mutation.mutants", "tests/mutation/run_mutation.py", "python", "mutation", ["--set", "all"]),
    ("fuzz.go-targets", "tests/fuzz/run_fuzz.sh", "shell", "fuzz", []),
]
DEFAULT_TIMEOUT = {"manifest": 600, "contract": 900, "unit": 1200, "integration": 1800,
                   "e2e": 900, "perf": 5400, "fuzz": 3600, "mutation": 5400}
DEFAULT_PROFILES = {"unit": ["unit", "full", "race"], "integration": ["integration", "full", "race"],
                    "e2e": ["e2e", "full"], "contract": ["contract", "core", "full"],
                    "perf": ["perf"], "manifest": ["manifest", "contract", "core", "full"],
                    "fuzz": ["fuzz"], "mutation": ["mutation"]}
OWNER_BY_LAYER = {"unit": "T-evergreen.system_assurance-158614-003",
                  "integration": "T-evergreen.system_assurance-158614-003",
                  "e2e": "T-evergreen.system_assurance-158614-004",
                  "contract": "T-evergreen.system_assurance-158614-005",
                  "perf": "T-evergreen.system_assurance-158614-005",
                  "fuzz": "T-evergreen.system_assurance-158614-005",
                  "mutation": "T-evergreen.system_assurance-158614-005",
                  "manifest": "T-evergreen.system_assurance-158614-002"}
CURATED = ("profiles", "timeout_s", "required", "platform_required", "not_run_when", "issue",
           "exclude_cases", "evidence_re", "done_re", "args", "owner_task")


def read_tsv(p: Path) -> list[dict]:
    return list(csv.DictReader(p.read_text().splitlines(), delimiter="\t"))


def worktree_files() -> set[str]:
    """盘面口径 = 工作树（合同 D12）：已跟踪 + 未跟踪未 ignore。"""
    out = subprocess.run(["git", "-C", str(REPO), "ls-files", "-c", "-o",
                          "--exclude-standard", "tests"],
                         capture_output=True, text=True, check=True).stdout
    return {l for l in out.splitlines() if l}


def inventory_suites() -> dict[str, dict]:
    """按 suite_id 汇总 inventory：layer / capability / kind / 成员路径。

    go suite 的 layer 与 capability 取**用例数加权最多**的成员：一个 go 包就是一个测试
    二进制，包内文件的 layer 可以不同（`test/e2e` 里既有 integration 级验收用例，
    也有 e2e 级静态判据），但可执行单位只有一个，必须给出唯一归属。
    """
    rows = [r for r in read_tsv(MANIFEST / "inventory.tsv") if r["suite_id"] != "-"]
    g: dict[str, list[dict]] = defaultdict(list)
    for r in rows:
        g[r["suite_id"]].append(r)
    out = {}
    for sid, rs in g.items():
        kinds = {r["kind"] for r in rs}
        # 入口 kind 取**驱动方**：perf suite 由 bench_p95.sh 驱动，包里的 corpus_gen.go
        # 是它 `go run` 的语料生成器，不是独立可执行单位。按 shell > python > go 定序，
        # 否则 perf.bench-p95 会被判成 go 包 suite，min_cases 现场推导为 0（实测）。
        kind = "shell" if "shell" in kinds else "python" if "python" in kinds else "go"

        weight = Counter()
        capw = Counter()
        for r in rs:
            w = int(r["cases"]) + 1
            weight[r["layer"]] += w
            capw[r["capability"]] += w
        layer = weight.most_common(1)[0][0]
        cap = capw.most_common(1)[0][0]
        if kind == "go":
            pkg = "/".join(rs[0]["path"].split("/")[2:-1])
            target = "./" + pkg
            source = f"tests/_staged/{pkg}/*_test.go"
        else:
            entry = sorted(r["path"] for r in rs if r["kind"] == kind)[0]
            target = entry
            source = ("，".join(sorted(r["path"] for r in rs)) if len(rs) > 1 else entry)
        out[sid] = dict(layer=layer, capability=cap, kind=kind, target=target, source=source,
                        members=[r["path"] for r in rs])
    return out


def meta_executables() -> set[str]:
    """元层可执行资产：tools / mutation / fuzz 下的 .py / .sh。

    `tests/runner/` 不在此列：materializer 与 runner 自身是**执行机制**，不是被执行的
    判据；它们的正确性由 contract/staging-isolation 与本 runner 的反例门禁负责。
    """
    inv = read_tsv(MANIFEST / "inventory.tsv")
    return {r["path"] for r in inv
            if r["layer"] in ("tools", "mutation", "fuzz") and r["kind"] in ("python", "shell")}


def load_existing() -> dict[str, dict]:
    if not SUITES_YAML.exists():
        return {}
    import yaml
    data = yaml.safe_load(SUITES_YAML.read_text()) or {}
    return {s["id"]: s for s in data.get("suites", [])}


def build(existing: dict[str, dict]) -> list[dict]:
    suites: list[dict] = []
    inv = inventory_suites()
    for sid, meta in sorted(inv.items()):
        old = existing.get(sid, {})
        s = dict(id=sid, layer=meta["layer"], capability=meta["capability"], kind=meta["kind"],
                 target=meta["target"], source=meta["source"], origin="inventory",
                 owner_task=old.get("owner_task") or OWNER_BY_LAYER.get(meta["layer"], "-"),
                 profiles=old.get("profiles") or list(DEFAULT_PROFILES[meta["layer"]]),
                 timeout_s=old.get("timeout_s") or DEFAULT_TIMEOUT[meta["layer"]],
                 min_cases=max(1, int(old.get("min_cases", 1))),
                 required=old.get("required", True),
                 platform_required=old.get("platform_required", "linux/amd64"),
                 not_run_when=old.get("not_run_when"), issue=old.get("issue"))
        if meta["kind"] == "go":
            excl = list(old.get("exclude_cases") or [])
            names = runner.go_case_names(runner.staged_pkg_dir(meta["target"]))
            s["exclude_cases"] = excl
            s["min_cases"] = len([n for n in names if n not in excl])
        for k in ("evidence_re", "done_re", "args"):
            if old.get(k):
                s[k] = old[k]
        suites.append(s)

    for sid, target, kind, layer, args in META_SUITES:
        old = existing.get(sid, {})
        s = dict(id=sid, layer=layer, capability="-" if layer == "manifest" else layer,
                 kind=kind, target=target, source=target, origin="tool",
                 owner_task=old.get("owner_task") or OWNER_BY_LAYER.get(layer, "-"),
                 profiles=old.get("profiles") or list(DEFAULT_PROFILES[layer]),
                 timeout_s=old.get("timeout_s") or DEFAULT_TIMEOUT[layer],
                 min_cases=max(1, int(old.get("min_cases", 1))),
                 required=old.get("required", True),
                 platform_required=old.get("platform_required", "linux/amd64"),
                 not_run_when=old.get("not_run_when"), issue=old.get("issue"))
        if args:
            s["args"] = old.get("args") or args
        for k in ("evidence_re", "done_re"):
            if old.get(k):
                s[k] = old[k]
        suites.append(s)

    lint = existing.get("contract.make-lint", {})
    suites.append(dict(id="contract.make-lint", layer="contract", capability="arch-boundary",
                       kind="make", target="lint", source="Makefile",
                       origin="tool", owner_task=lint.get("owner_task") or OWNER_BY_LAYER["contract"],
                       profiles=lint.get("profiles") or ["contract", "core", "full"],
                       timeout_s=lint.get("timeout_s") or 900,
                       min_cases=max(1, int(lint.get("min_cases", 1))), required=lint.get("required", True),
                       platform_required=lint.get("platform_required", "linux/amd64"),
                       not_run_when=lint.get("not_run_when"), issue=lint.get("issue"),
                       evidence_re=lint.get("evidence_re") or r"通过|\[PASS\]"))
    return suites


def dump(suites: list[dict]) -> str:
    import yaml
    header = (
        "# tests/manifest/suites.yaml —— 全系统套件的唯一清单（合同 D4；由 tests/tools/suites.py 维护）\n"
        "#\n"
        "# 生成：python3 tests/tools/suites.py --write   （保留人工决策字段）\n"
        "# 自检：python3 tests/tools/suites.py --check   （孤儿 / 幽灵 / schema / 语义，manifest profile 消费）\n"
        "# 执行：bash tests/run.sh --profile <profile>   （runner 只从本文件派生计划）\n"
        "#\n"
        "# 字段语义（每条都有判据，见合同 D4/D5/D6）：\n"
        "#   origin        inventory=可执行测试资产（与 inventory.tsv 的 suite_id 一一对应）；\n"
        "#                 tool=元层判据入口（清单自检 / mutation / fuzz / make lint），不进覆盖矩阵\n"
        "#   target        go=包模式（在物化树里跑）；shell/python=仓库相对脚本路径；make=Makefile 目标\n"
        "#   min_cases     零执行下限。go 由盘面用例数现场推导（不许手填）；shell/python 由\n"
        "#                 `--calibrate` 从真实 run 回填，只升不降\n"
        "#   exclude_cases 仅用于**由 e2e 脚本驱动**的 go harness 用例：被排除的名字必须能在某支\n"
        "#                 已登记 shell suite 里找到 `-run <名字>`，否则就是私自减覆盖（--check 判据）\n"
        "#   required      true=必需（NOT_RUN 判 FAIL）；false=可选（NOT_RUN 单列、退出码 0）\n"
        "#   not_run_when  仅 required:false 可填，且必须绑 issue：no_sibling_teamwork /\n"
        "#                 no_go_toolchain / non_native_platform\n"
        "#   evidence_re   shell/python suite 的证据标记正则（默认 `\\[(?:ok|PASS)\\]`）：\n"
        "#   done_re       shell/python suite 的收尾标记正则（默认 `\\[PASS\\]` / `全部…通过`）：\n"
        "#                 历史脚本收尾行形态不统一时逐条声明，禁止放宽默认族。\n"
        "#                 命中次数即 case_count，用于「退出码 0 但什么都没跑」的判红\n"
    )
    body = yaml.safe_dump({"suites": suites}, allow_unicode=True, sort_keys=False, width=100)
    return header + body


def check() -> int:
    errs: list[str] = []
    if not SUITES_YAML.exists():
        print(f"[FAIL] 缺 {SUITES_YAML}", file=sys.stderr)
        return 1
    suites = runner.load_suites(SUITES_YAML)
    errs += runner.validate(suites, SUITES_YAML)

    by_id = {s["id"]: s for s in suites if isinstance(s, dict) and "id" in s}
    inv = inventory_suites()
    yaml_inv = {sid for sid, s in by_id.items() if s.get("origin") == "inventory"}
    for sid in sorted(set(inv) - yaml_inv):
        errs.append(f"孤儿：inventory.tsv 有 suite_id={sid}（{inv[sid]['members'][0]} …）但 suites.yaml 未登记")
    for sid in sorted(yaml_inv - set(inv)):
        errs.append(f"幽灵：suites.yaml 登记 {sid}，inventory.tsv 里没有这个 suite_id")
    for sid in sorted(yaml_inv & set(inv)):
        s, m = by_id[sid], inv[sid]
        for key in ("layer", "capability", "kind", "target"):
            if s[key] != m[key]:
                errs.append(f"{sid}: {key}={s[key]!r} 与盘面推导 {m[key]!r} 不一致"
                            f"（归属只能改盘面/迁移表，不能在清单里改写）")

    claimed = Counter(s["target"] for s in suites if s.get("origin") == "tool")
    for path in sorted(meta_executables()):
        if claimed[path] == 0:
            errs.append(f"未认领的元层可执行资产：{path}（新增工具必须在 suites.py 的 META_SUITES 登记）")
        elif claimed[path] > 1:
            errs.append(f"元层资产被多条 suite 认领：{path}")
    wt = worktree_files()
    for s in suites:
        if s.get("origin") == "tool" and s["kind"] in ("python", "shell") and s["target"] not in wt:
            errs.append(f"{s['id']}: target 不在工作树盘面：{s['target']}")

    caps = {r["capability"] for r in read_tsv(MANIFEST / "coverage.tsv")}
    have = {s["capability"] for s in suites}
    for cap in sorted(caps - have):
        errs.append(f"能力 {cap} 在 coverage.tsv 有资产，但没有任何 suite 承载（D9 覆盖不减）")
    for prof in runner.PROFILES:
        if not any(prof in s["profiles"] for s in suites):
            errs.append(f"profile {prof} 没有任何 suite（死信 profile）")

    if errs:
        print(f"[FAIL] suites.yaml 自检不通过，共 {len(errs)} 条：", file=sys.stderr)
        for e in errs:
            print(f"  - {e}", file=sys.stderr)
        return 1
    layers = Counter(s["layer"] for s in suites)
    print(f"[ok] suites.yaml: {len(suites)} 个 suite 与盘面一致"
          f"（inventory {len(yaml_inv)} + 元层 {len(suites) - len(yaml_inv)}）")
    print("[ok] 层级分布：" + "，".join(f"{k}={layers[k]}" for k in runner.LAYERS if layers[k]))
    print("[PASS] 孤儿 / 幽灵 / schema / required-NOT_RUN 语义 / 覆盖不减 全部通过")
    return 0


def calibrate(summary: Path) -> int:
    data = json.loads(summary.read_text())
    observed = {r["id"]: r["case_count"] for r in data["suites"] if r["status"] == "PASS"}
    existing = load_existing()
    changed = []
    for sid, s in existing.items():
        if s["kind"] == "go" or sid not in observed:
            continue
        new = max(int(s.get("min_cases", 1)), int(observed[sid]))
        if new != s.get("min_cases"):
            changed.append((sid, s.get("min_cases"), new))
            s["min_cases"] = new
    suites = build(existing)
    SUITES_YAML.write_text(dump(suites))
    print(f"[ok] 依据 {data['run_id']}（profile={data['profile']}）回填 {len(changed)} 个下限（只升不降）")
    for sid, old, new in changed[:10]:
        print(f"     {sid}: {old} → {new}")
    if len(changed) > 10:
        print(f"     …… 其余 {len(changed) - 10} 条见 diff")
    return 0


def main() -> int:
    ap = argparse.ArgumentParser()
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--write", action="store_true")
    g.add_argument("--check", action="store_true")
    g.add_argument("--calibrate", metavar="SUMMARY_JSON")
    a = ap.parse_args()
    if a.write:
        suites = build(load_existing())
        SUITES_YAML.write_text(dump(suites))
        print(f"[ok] 写入 {SUITES_YAML.relative_to(REPO)}：{len(suites)} 个 suite")
        return 0
    if a.calibrate:
        return calibrate(Path(a.calibrate))
    return check()


if __name__ == "__main__":
    sys.exit(main())
