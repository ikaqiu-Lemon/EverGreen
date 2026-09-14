#!/usr/bin/env python3
"""测试资产盘点与派生清单生成（system_assurance · T-…-002）。

生成 / 校验三张派生表（全部由盘面事实推导，禁止手改）：
  tests/manifest/inventory.tsv    每个测试资产一行：路径、层级、能力、kind、用例数、字节数
  tests/manifest/runtime_map.tsv  权威路径 → 运行期路径（materializer 的唯一输入）
  tests/manifest/coverage.tsv     能力 × 层级覆盖矩阵（只含 5 个能力层，元层不入表）

盘面口径（合同 D12，起因 I-…-004）：**工作树** = 已跟踪 + 未跟踪且未被 ignore。
不用裸 `git ls-files`（只看索引）——那会让新资产在 `git add` 之前对判据隐形，
出现"提交前绿、提交后红"，漂移被夹带进提交。ignore 规则仍生效，生成物不进表。

本工具是**开发侧派生 / 校验工具**，显式依赖 git（需要 ignore 语义），这与 D10
「物化与必需 suite 不得硬依赖 git ls-files」不冲突：分发包场景由 materializer 的
walk 列举 + 包内快照承担。

用法：
  python3 tests/tools/inventory.py --write    重新生成三张表
  python3 tests/tools/inventory.py --check    与盘面比对，漂移即非零（manifest profile 消费）
"""
from __future__ import annotations
import argparse, csv, re, subprocess, sys
from collections import Counter, defaultdict
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
MANIFEST = REPO / "tests/manifest"

GO_CASE_RE = re.compile(r"^func (Test|Fuzz|Benchmark|Example)[A-Z_0-9]", re.M)
SH_ASSERT_RE = re.compile(r"\b(die|fail|assert|sub|ok)\b\s", re.M)


def git(*a: str) -> str:
    r = subprocess.run(["git", "-C", str(REPO), *a], capture_output=True, text=True, check=True)
    return r.stdout


def read_tsv(path: Path) -> list[dict]:
    if not path.exists():
        return []
    return list(csv.DictReader(path.read_text().splitlines(), delimiter="\t"))


def cases_of(rel: str, text: str) -> int:
    if rel.endswith("_test.go"):
        return len(GO_CASE_RE.findall(text))
    if rel.endswith(".sh"):
        return len(SH_ASSERT_RE.findall(text))
    return 0


def build() -> dict[str, list[list[str]]]:
    mig = read_tsv(MANIFEST / "migration.tsv")
    layer_of = {r["new_path"]: r["layer"] for r in mig if r["decision"] in ("keep", "archive")}
    cap_of = {r["new_path"]: r["capability"] for r in mig if r["decision"] in ("keep", "archive")}
    suite_of = {r["new_path"]: r["suite_id"] for r in mig if r["decision"] in ("keep", "archive")}

    # 盘面口径 = **工作树**：已跟踪 + 未跟踪但未被 ignore（`-c -o --exclude-standard`）。
    # 只看已跟踪（裸 `ls-files`）会让判据依赖 git 索引状态：新资产在 `git add` 之前隐形，
    # 门禁跑绿；`git add -A && commit` 之后同一条命令立刻变红 —— 提交里带着漂移的清单
    # （I-…-004 就是这么发生的）。ignore 规则仍然生效，_report / .tests-staging 不会进表。
    files = sorted({f for f in git("ls-files", "-c", "-o", "--exclude-standard",
                                   "tests").splitlines() if f})
    # 自身派生物不进盘点，避免自指漂移。suites.yaml 同样在列：它由 tests/tools/suites.py
    # 从本表派生，若把它的字节数记进本表，`suites.py --write` 一执行就必然让
    # `inventory.py --check` 变红（实测），形成"两张表互相把对方判红"的死循环。
    derived = {"tests/manifest/inventory.tsv", "tests/manifest/coverage.tsv",
               "tests/manifest/runtime_map.tsv", "tests/manifest/suites.yaml"}

    # ---- inventory
    inv = []
    for rel in files:
        p = REPO / rel
        if rel in derived or not p.is_file():
            continue
        kind = ("go" if rel.endswith(".go") else "shell" if rel.endswith(".sh")
                else "python" if rel.endswith(".py") else "data")
        try:
            text = p.read_text()
        except UnicodeDecodeError:
            text = ""
        layer = layer_of.get(rel) or infer_layer(rel)
        inv.append([rel, layer, kind, cap_of.get(rel, infer_cap(rel)),
                    suite_of.get(rel) or infer_suite(rel, layer, kind),
                    str(cases_of(rel, text)), str(p.stat().st_size)])
    inv.sort()

    # ---- go suite 归属：**一个 go 包 = 一个测试二进制 = 一个 suite**
    # 同包内的文件不能各自成 suite：`go test` 无法只编译半个包，硬拆出来的 suite 号
    # 在 runner 里根本没有对应的可执行单位（实测 tests/_staged/test/e2e/ 曾被拆成
    # integration.e2e-go + e2e.go.layout + e2e.go.index_symbol_judge 三个，其中
    # layout_test.go 连一个 Test 函数都没有——它是包内 helper）。
    # 归属取**同包里用例数加权最多**的那个 suite_id；layer / capability 逐文件保持不变，
    # 因此 coverage.tsv（按文件计能力矩阵）不受影响。
    pkg_weight: dict[str, Counter] = defaultdict(Counter)
    pkg_cap: dict[str, Counter] = defaultdict(Counter)
    for rel, layer, kind, cap, suite, cases, size in inv:
        if kind == "go" and rel.startswith("tests/_staged/") and suite != "-":
            pkg_weight["/".join(rel.split("/")[:-1])][suite] += int(cases) + 1
            if rel in cap_of:  # 只让**迁移表登记过**的成员参与能力归属投票
                pkg_cap["/".join(rel.split("/")[:-1])][cap] += int(cases) + 1
    for row in inv:
        rel, kind, suite = row[0], row[2], row[4]
        if kind == "go" and rel.startswith("tests/_staged/") and suite != "-":
            pkgdir = "/".join(rel.split("/")[:-1])
            row[4] = pkg_weight[pkgdir].most_common(1)[0][0]
            # 能力归属同理走包级：未登记文件的 infer_cap 只能拿文件名当能力
            # （实测产出过 `layout` / `index_symbol_judge` 这种伪能力，它们不在 D2 能力表里，
            #  还会在 coverage.tsv 里各占一行、却没有任何 suite 承载）。
            # 改为取同包内已登记成员的加权多数，平票取字典序最小 —— 结果与 git 状态、
            # 文件遍历顺序无关，可复算。
            if rel not in cap_of and pkg_cap[pkgdir]:
                top = max(pkg_cap[pkgdir].values())
                row[3] = sorted(c for c, w in pkg_cap[pkgdir].items() if w == top)[0]


    # ---- runtime_map
    rmap = []
    for rel in files:
        if rel.startswith("tests/_staged/"):
            rt = rel[len("tests/_staged/"):]
            dflt = "e2e" if rt.startswith("test/e2e/") else "unit"
            rmap.append([rel, rt, "evergreen", layer_of.get(rel, dflt), "go"])
        elif rel.startswith("tests/lib/txnctl/"):
            rmap.append([rel, "test/txnctl/" + rel.split("/")[-1], "evergreen", "lib", "go"])
        elif rel.startswith("tests/fixtures/e2e/"):
            rmap.append([rel, "test/e2e/testdata/" + rel[len("tests/fixtures/e2e/"):],
                         "evergreen", "fixtures", "data"])
        elif rel.startswith("tests/perf/") and rel.endswith(".go"):
            rmap.append([rel, "test/perf/" + rel[len("tests/perf/"):], "evergreen", "perf", "go"])
        elif rel.startswith("tests/fixtures/contracts/") and not rel.endswith("SHA256SUMS"):
            rmap.append([rel, "teamwork/" + rel[len("tests/fixtures/contracts/"):],
                         "staging", "contract-snapshot", "data"])
    rmap.sort()

    # ---- coverage：能力 × 层级
    #   只统计**能力层**（unit/integration/e2e/contract/perf）。archive/fixtures/lib/
    #   manifest/runner/tools/ci 以及 mutation/fuzz 都是**元层**：前者是资产与工具，
    #   后者是"检查判据自己"与"喂随机输入"的 harness，既没有 capability 归属
    #   （infer_cap 给 `-`），也不该把能力覆盖矩阵撑出一行空 `-`。
    #   total 只对显示出来的这 5 列求和 —— 表格必须自洽：横向加起来等于 total。
    layers = ["unit", "integration", "e2e", "contract", "perf"]
    grid = defaultdict(Counter)
    for rel, layer, kind, cap, suite, cases, size in inv:
        if layer not in layers:
            continue
        grid[cap][layer] += 1
    cov = []
    for cap in sorted(grid):
        row = [cap] + [str(grid[cap][l]) for l in layers]
        row.append(str(sum(grid[cap][l] for l in layers)))
        cov.append(row)
    return {
        "inventory.tsv": [["path", "layer", "kind", "capability", "suite_id", "cases", "bytes"]] + inv,
        "runtime_map.tsv": [["repo_path", "runtime_path", "root", "layer", "kind"]] + rmap,
        "coverage.tsv": [["capability", *layers, "total"]] + cov,
    }


def infer_layer(rel: str) -> str:
    if rel.startswith("tests/_staged/test/e2e/"):
        return "e2e"
    for pre, layer in (("tests/_staged/", "unit"), ("tests/e2e/", "e2e"), ("tests/contract/", "contract"),
                       ("tests/perf/", "perf"), ("tests/fuzz/", "fuzz"), ("tests/mutation/", "mutation"),
                       ("tests/fixtures/", "fixtures"), ("tests/lib/", "lib"),
                       ("tests/archive/", "archive"), ("tests/manifest/", "manifest"),
                       ("tests/runner/", "runner"), ("tests/tools/", "tools"), ("tests/ci/", "ci")):
        if rel.startswith(pre):
            return layer
    return "-"


def infer_cap(rel: str) -> str:
    parts = rel.split("/")
    if rel.startswith("tests/e2e/") and len(parts) > 2:
        return parts[2]
    if rel.startswith("tests/contract/"):
        return "arch-boundary"
    if rel.startswith("tests/perf/"):
        return "perf"
    if rel.startswith("tests/_staged/") and rel.endswith("_test.go"):
        return parts[-1][:-len("_test.go")].split(".")[0]
    return "-"


def infer_suite(rel: str, layer: str, kind: str = "") -> str:
    """为 migration.tsv 未登记 suite_id 的资产推导 suite 号。

    suite 是 **追溯单位**（traceability.tsv 的覆盖计数就按它去重），因此可执行层级
    （unit / integration / e2e / contract / perf）**不允许**留 `-`：留 `-` 等于该资产
    在需求覆盖上不可见。非可执行层级（fixtures / lib / manifest / tools / runner /
    archive）不参与追溯，仍记 `-`。
    """
    if layer not in ("unit", "integration", "e2e", "contract", "perf"):
        return "-"
    if kind == "data":
        # 数据资产（门槛表 / 语料 / 清单）不执行，因此不是追溯单位。
        # 反例：`tests/perf/thresholds.yaml` 若拿到 suite_id `perf.thresholds.yaml`，
        # 就会以"一个 suite"的身份进 legacy_gates 的 suite 全集与追溯计数 —— 虚增覆盖。
        return "-"
    parts = rel.split("/")
    stem = parts[-1]
    for suf in ("_test.go", ".go", ".sh", ".py"):
        if stem.endswith(suf):
            stem = stem[: -len(suf)]
            break
    if rel.startswith("tests/_staged/test/e2e/"):
        return f"e2e.go.{stem}"
    if rel.startswith("tests/_staged/"):
        pkg = "/".join(parts[2:-1])
        return "unit." + pkg.replace("/", ".") if pkg else f"unit.{stem}"
    if rel.startswith("tests/contract/") and len(parts) > 3:
        # 主题目录与脚本名同名时不叠一层：`contract/contract-snapshot/contract_snapshot.sh`
        # → `contract.contract-snapshot`（合同 D8 逐字点名的那两个 suite 号就是这么来的）。
        if stem.replace("_", "-") == parts[2]:
            return f"contract.{parts[2]}"
        return f"contract.{parts[2]}.{stem}"
    if rel.startswith("tests/e2e/") and len(parts) > 3:
        return f"e2e.{parts[2]}.{stem}"
    if rel.startswith("tests/perf/"):
        return f"perf.{stem}"
    return f"{layer}.{stem}"


def render(rows: list[list[str]]) -> str:
    return "\n".join("\t".join(r) for r in rows) + "\n"


def main() -> int:
    ap = argparse.ArgumentParser()
    g = ap.add_mutually_exclusive_group(required=True)
    g.add_argument("--write", action="store_true")
    g.add_argument("--check", action="store_true")
    args = ap.parse_args()

    built = build()
    bad = 0
    for name, rows in built.items():
        dst = MANIFEST / name
        text = render(rows)
        if args.write:
            dst.write_text(text)
            print(f"[写] {name}: {len(rows)-1} 行")
        else:
            cur = dst.read_text() if dst.exists() else ""
            if cur != text:
                print(f"[FAIL] {name} 与盘面漂移（重跑 tests/tools/inventory.py --write）", file=sys.stderr)
                bad += 1
            else:
                print(f"[ok] {name}: {len(rows)-1} 行与盘面一致")
    # 收尾标记：统一 runner 用它证明"脚本真的跑到了最后"（合同 D6.2；退出码 0 不足以排除中途 return）
    if not bad:
        print(f"[PASS] 清单三表与盘面一致（{len(built)} 张派生表，零漂移）")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
