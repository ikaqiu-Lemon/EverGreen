#!/usr/bin/env python3
"""唯一全系统测试入口（system_assurance · T-…-006，合同 D4 / D5 / D6 / D7）。

设计要点（每一条都对应合同里的一个硬约束，不是风格选择）：

* **唯一清单驱动（D4）**：执行计划只能从 `tests/manifest/suites.yaml` 派生。
  runner 里没有任何硬编码的 suite 列表、目录遍历或"顺便再跑一下"的分支；
  清单里没有的东西不会被执行，清单里有而盘面没有的（幽灵）在加载阶段就非零。
* **去重一次遍历（D5）**：`full` 是 profile 过滤后的**集合**，天然无重复；
  同时禁止把历史阶段聚合脚本（`m*_acceptance.sh` / `m*_final_gate.py`）登记成 suite，
  因此不存在 m6→m5→m4 的递归重跑。
* **零执行 / 隐式 skip 必须判红（D6.2）**：
  - Go suite：`go test -json` 逐事件解析，`skip` 事件、`no test files`、
    实际执行的顶层用例数 < 清单声明的 `min_cases` —— 任一命中即 FAIL；
  - shell / python suite：证据标记（默认 `[ok]` / `[PASS]`）计数 < `min_cases`，
    或缺少"跑完了"的收尾标记 —— 即 FAIL。**退出码 0 不等于跑过了**，这是本 runner
    与"直接 bash 一遍脚本"的关键区别。
* **`-run` 过滤不得掩盖用例（D6.2）**：go suite 的 `-run` 正则**不是手写**的，
  而是扫描物化包里的 `func Test…/Fuzz…` 现场生成；只允许通过 `exclude_cases`
  排除**由 e2e 脚本驱动**的 harness 用例，且每个被排除的名字必须能在某支已登记
  shell suite 里找到 `-run <名字>`（`suites.py --check` 的判据），否则就是私自减覆盖。
* **NOT_RUN 分级（D6.3）**：`required: true` 的 NOT_RUN 判 FAIL；`required: false`
  且 `not_run_when` 命中并绑 issue 的单列 NOT_RUN、不计 PASS 分子、退出码 0；
  `--strict` 下可选 NOT_RUN 升级为退出码 2。
* **隔离与资源上限（D7）**：run 级 TMPDIR 在 staging 内，每 suite 独立 scratch；
  e2e 串行；`timeout_s` 必填并逐 suite 生效；报告落 `tests/_report/<run-id>/`。

用法：
  bash tests/run.sh --profile core
  bash tests/run.sh --profile full [--strict] [--keep-on-fail]
  bash tests/run.sh --profile full --plan-only
  bash tests/run.sh --profile unit --only unit.internal.cli
"""
from __future__ import annotations

import argparse
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
DEFAULT_SUITES = REPO / "tests/manifest/suites.yaml"
MATERIALIZE = REPO / "tests/runner/materialize.py"

# 层级执行顺序：清单自检 → 静态门禁 → 白盒 → 跨包 → 场景 → 重型。
# 先跑便宜且定位精准的层，坏消息尽早出现（迁移期的实际收益）。
LAYERS = ["manifest", "contract", "unit", "integration", "e2e", "perf", "fuzz", "mutation"]
PROFILES = ["manifest", "unit", "integration", "e2e", "contract", "core",
            "full", "race", "perf", "fuzz", "mutation"]
KINDS = ["go", "shell", "python", "make"]
NOT_RUN_WHEN = {"no_sibling_teamwork", "no_go_toolchain", "non_native_platform"}
ORIGINS = {"inventory", "tool"}
HEAVY_LAYERS = {"perf", "fuzz", "mutation"}
# 历史阶段聚合入口：一旦被登记成 suite，就会把"阶段递归重跑"带回来（D5）。
STAGE_AGGREGATOR_RE = re.compile(r"(^|/)m[1-6]_[a-z0-9_]*(acceptance|final_gate|gate)[a-z0-9_]*\.(sh|py)$")
GO_CASE_RE = re.compile(r"^func ((?:Test|Fuzz)[A-Z_0-9][A-Za-z_0-9]*)\s*\(", re.M)
REQUIRED_KEYS = ["id", "layer", "capability", "kind", "target", "profiles", "owner_task",
                 "source", "timeout_s", "min_cases", "required", "platform_required",
                 "not_run_when", "issue", "origin"]


# --------------------------------------------------------------------------- 载入与校验

def load_suites(path: Path) -> list[dict]:
    try:
        import yaml
    except ImportError:  # pragma: no cover
        die("需要 PyYAML（pip install pyyaml）来解析 suites.yaml")
    data = yaml.safe_load(path.read_text()) or {}
    if not isinstance(data, dict) or "suites" not in data:
        die(f"{path} 顶层必须是 mapping 且含 `suites:` 列表")
    suites = data["suites"]
    if not isinstance(suites, list) or not suites:
        die(f"{path} 的 suites 必须是非空列表")
    return suites


def native_platform() -> str:
    m = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}
    return f"{platform.system().lower()}/{m.get(platform.machine(), platform.machine())}"


def staged_pkg_dir(target: str) -> Path:
    """go suite 的 target（`./internal/cli`）→ 测试文件权威存放目录。"""
    return REPO / "tests/_staged" / target.lstrip("./")


def go_case_names(pkg_dir: Path) -> list[str]:
    names: list[str] = []
    for f in sorted(pkg_dir.glob("*_test.go")):
        names.extend(GO_CASE_RE.findall(f.read_text()))
    return names


def validate(suites: list[dict], suites_path: Path) -> list[str]:
    """清单自检：schema + 语义 + 幽灵（登记不在盘）。返回错误行列表。"""
    errs: list[str] = []
    seen: dict[str, int] = {}
    shell_targets: list[str] = []
    for s in suites:
        if isinstance(s, dict) and s.get("kind") == "shell" and isinstance(s.get("target"), str):
            shell_targets.append(s["target"])
    driver_text = ""
    for t in shell_targets:
        p = REPO / t
        if p.is_file():
            driver_text += p.read_text()

    for i, s in enumerate(suites):
        where = f"suites[{i}]"
        if not isinstance(s, dict):
            errs.append(f"{where}: 必须是 mapping")
            continue
        for k in ("evidence_re", "done_re"):
            if s.get(k) is not None:
                try:
                    re.compile(s[k])
                except re.error as e:
                    errs.append(f"{where}: {k} 不是合法正则（{e}）")
        sid = s.get("id", "<无 id>")
        where = f"{sid}"
        missing = [k for k in REQUIRED_KEYS if k not in s]
        if missing:
            errs.append(f"{where}: 缺必填键 {missing}（schema 见合同 D4）")
            continue
        if sid in seen:
            errs.append(f"{where}: id 重复（已出现在 suites[{seen[sid]}]）")
        seen[sid] = i
        if s["layer"] not in LAYERS:
            errs.append(f"{where}: layer={s['layer']} 非法，合法值 {LAYERS}")
        if s["kind"] not in KINDS:
            errs.append(f"{where}: kind={s['kind']} 非法，合法值 {KINDS}")
        if s["origin"] not in ORIGINS:
            errs.append(f"{where}: origin={s['origin']} 非法，合法值 {sorted(ORIGINS)}")
        profs = s["profiles"]
        if not isinstance(profs, list) or not profs:
            errs.append(f"{where}: profiles 必须是非空列表")
            profs = []
        bad = [p for p in profs if p not in PROFILES]
        if bad:
            errs.append(f"{where}: profiles 含非法取值 {bad}，合法值 {PROFILES}")
        if len(set(profs)) != len(profs):
            errs.append(f"{where}: profiles 内部重复 {profs}")
        if "race" in profs and s["kind"] != "go":
            errs.append(f"{where}: 只有 go suite 能进 race profile（-race 是 go 工具链能力）")
        if s["layer"] in HEAVY_LAYERS and ({"core", "full"} & set(profs)):
            errs.append(f"{where}: 重型层 {s['layer']} 不得进 core/full（合同 D5）")
        if s["layer"] not in HEAVY_LAYERS and (set(profs) & HEAVY_LAYERS):
            errs.append(f"{where}: 非重型层不得声明重型 profile {sorted(set(profs) & HEAVY_LAYERS)}")
        for p in ("unit", "integration", "e2e"):
            if p in profs and s["layer"] != p:
                errs.append(f"{where}: 声明了 {p} profile 但 layer={s['layer']}")
        if "contract" in profs and s["layer"] not in ("contract", "manifest"):
            errs.append(f"{where}: contract profile 只接受 contract / manifest 层")
        if not isinstance(s["timeout_s"], int) or s["timeout_s"] <= 0:
            errs.append(f"{where}: timeout_s 必须是正整数（D7 单 suite 超时必填）")
        if not isinstance(s["min_cases"], int) or s["min_cases"] < 1:
            errs.append(f"{where}: min_cases 必须 ≥ 1（零执行判红的下限，D6.2）")
        if not isinstance(s["required"], bool):
            errs.append(f"{where}: required 必须是 bool")
        if not re.fullmatch(r"[a-z0-9]+/[a-z0-9]+", str(s["platform_required"] or "")):
            errs.append(f"{where}: platform_required 形如 linux/amd64")
        nrw, issue = s["not_run_when"], s["issue"]
        if nrw is not None:
            if nrw not in NOT_RUN_WHEN:
                errs.append(f"{where}: not_run_when={nrw} 非法，合法值 {sorted(NOT_RUN_WHEN)}")
            if s["required"]:
                errs.append(f"{where}: required: true 的 suite 不得带 not_run_when"
                            f"（必需就是必需，合同 D6.3）")
            if not issue:
                errs.append(f"{where}: not_run_when 必须绑定 issue（D6.3；不绑就是无据免跑）")
        elif issue:
            errs.append(f"{where}: 没有 not_run_when 却绑了 issue={issue}，语义不明")

        # ---- 幽灵检测（登记不在盘）与阶段聚合禁令
        tgt = str(s["target"])
        if STAGE_AGGREGATOR_RE.search(tgt):
            errs.append(f"{where}: 禁止把历史阶段聚合入口 {tgt} 登记成 suite（合同 D5）")
        if tgt.startswith("tests/archive/"):
            errs.append(f"{where}: 归档材料不得进执行计划（合同 D13）")
        if s["kind"] == "go":
            pkg = staged_pkg_dir(tgt)
            if not pkg.is_dir():
                errs.append(f"{where}: 幽灵 suite —— go 包的测试权威目录不在盘：{pkg}")
            else:
                names = go_case_names(pkg)
                excl = list(s.get("exclude_cases") or [])
                unknown = [n for n in excl if n not in names]
                if unknown:
                    errs.append(f"{where}: exclude_cases 里的用例不存在于 {pkg}：{unknown}")
                for n in excl:
                    if f"-run {n}" not in driver_text:
                        errs.append(f"{where}: exclude_cases 的 {n} 找不到 e2e 驱动方"
                                    f"（必须有已登记 shell suite 以 `-run {n}` 驱动它，否则等于减覆盖）")
                declared = len([n for n in names if n not in excl])
                if declared != s["min_cases"]:
                    errs.append(f"{where}: min_cases={s['min_cases']} 与盘面用例数 {declared} 不一致"
                                f"（新增/删除用例后必须同步清单）")
        elif s["kind"] in ("shell", "python"):
            if not (REPO / tgt).is_file():
                errs.append(f"{where}: 幽灵 suite —— 目标脚本不在盘：{tgt}")
        elif s["kind"] == "make":
            mk = (REPO / "Makefile")
            if not mk.is_file() or not re.search(rf"^{re.escape(tgt)}:", mk.read_text(), re.M):
                errs.append(f"{where}: 幽灵 suite —— Makefile 里没有目标 {tgt}")
    return errs


# --------------------------------------------------------------------------- 执行

def die(msg: str, code: int = 1) -> None:
    print(f"[FAIL] {msg}", file=sys.stderr)
    sys.exit(code)


def not_run_reason(s: dict, native: str) -> str | None:
    nrw = s["not_run_when"]
    if nrw is None:
        return None
    if nrw == "no_sibling_teamwork":
        sib = REPO.parent / "teamwork"
        return None if sib.is_dir() else "无 sibling teamwork（单仓分发口径）"
    if nrw == "no_go_toolchain":
        return None if shutil.which("go") else "本机无 go 工具链"
    if nrw == "non_native_platform":
        want = s["platform_required"]
        return None if want == native else f"suite 要求 {want}，本机 {native}"
    return f"未知 not_run_when={nrw}"


def materialize(run_id: str) -> Path:
    out = subprocess.run([sys.executable, str(MATERIALIZE), "--run-id", run_id,
                          "--no-cleanup", "--json"], cwd=REPO, capture_output=True, text=True)
    if out.returncode != 0:
        sys.stderr.write(out.stdout + out.stderr)
        die(f"物化失败（run-id={run_id}）")
    # D3.7 输出字段合同：消费方只认 `stage`，不复刻布局。
    stage = Path(json.loads(out.stdout)["stage"])
    if not (stage / "evergreen").is_dir():
        die(f"物化报告的 stage 不可用：{stage}")
    return stage


def run_cmd(cmd: list[str], cwd: Path, env: dict, timeout: int, log: Path) -> tuple[int, str]:
    log.parent.mkdir(parents=True, exist_ok=True)
    started = time.time()
    with log.open("w") as fh:
        fh.write(f"$ cd {cwd} && {' '.join(cmd)}\n\n")
        fh.flush()
        try:
            p = subprocess.run(cmd, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                               stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, timeout=timeout)
            fh.write(p.stdout)
            return p.returncode, p.stdout
        except subprocess.TimeoutExpired as e:
            partial = e.output or ""
            if isinstance(partial, bytes):
                partial = partial.decode("utf-8", "replace")
            fh.write(partial)
            fh.write(f"\n[TIMEOUT] 超过 {timeout}s（D7 单 suite 超时）\n")
            return 124, partial
        finally:
            fh.write(f"\n[elapsed] {time.time() - started:.1f}s\n")


def judge_go(s: dict, rc: int, out: str) -> tuple[str, int, str]:
    """解析 go test -json：返回 (status, case_count, diagnosis)。"""
    top: dict[str, str] = {}
    pkg_action: dict[str, str] = {}
    no_test_files = False
    noise = 0
    for line in out.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            # 非 JSON 行一律当噪声丢弃：`go env` 提示、包装器打印的 [bits_ut_info]、
            # testcache 说明等都会混进合并后的 stderr。**不允许**从噪声里推断结论，
            # 否则一句无关的 "no test files" 就能把一支真绿 suite 判红（T-…-006 复核项）。
            noise += 1
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            noise += 1
            continue
        action, name = ev.get("Action"), ev.get("Test")
        if not name:
            # 包级事件：`go test -json` 每个包结尾必有 pass/fail/skip，是最权威的一手结论。
            if action in ("pass", "fail", "skip"):
                pkg_action[ev.get("Package") or "?"] = action
            elif action == "output" and "no test files" in (ev.get("Output") or ""):
                no_test_files = True
            continue
        if action not in ("pass", "fail", "skip"):
            continue
        top.setdefault(name.split("/")[0], action)
        if "/" not in name:
            top[name] = action
    skipped = sorted(n for n, a in top.items() if a == "skip")
    failed = sorted(n for n, a in top.items() if a == "fail")
    count = len(top)
    if no_test_files:
        return "FAIL", count, "go 包内没有测试文件（`no test files`）：零执行判红（D6.2）"
    if skipped:
        return "FAIL", count, f"命中 skip：{skipped}（D6.2 禁止隐式 skip；" \
                              f"脚本驱动的 harness 必须走 exclude_cases + 驱动方）"
    if failed:
        return "FAIL", count, f"用例失败：{failed}"
    bad_pkgs = sorted(p for p, a in pkg_action.items() if a == "fail")
    if bad_pkgs:
        return "FAIL", count, f"包级 fail 事件：{bad_pkgs}"
    if rc != 0 and not pkg_action:
        # 只有在**一个包级结论都没拿到**时才拿退出码兜底（编译失败 / panic / 信号）。
        # 有包级 pass 事件时退出码不再是判据：包装器与噪声都可能污染退出码。
        return "FAIL", count, f"go test 退出码 {rc} 且无任何包级结论（编译失败 / panic，见日志）"
    if count < s["min_cases"]:
        return "FAIL", count, f"实际执行顶层用例 {count} < 清单声明 min_cases={s['min_cases']}" \
                              f"（-run 过滤把用例吃掉了，D6.2）"
    if not pkg_action:
        return "FAIL", count, "没有解析到任何包级 pass/fail 事件：结论不可信（D6.2）"
    return "PASS", count, ""


DONE_RE = re.compile(r"\[PASS\]|全部 .{0,60}通过|全部通过")
SHELL_SKIP_RE = re.compile(r"^\s*\[SKIP\]", re.M)


def judge_script(s: dict, rc: int, out: str) -> tuple[str, int, str]:
    ev_re = re.compile(s.get("evidence_re") or r"\[(?:ok|PASS)\]")
    # done_re：收尾标记按 suite 声明。默认族是 `[PASS]` / `全部…通过`，但历史脚本的收尾行
    # 形态并不统一（例：real_article.sh 的 `M1 端到端验收脚本通过：14 步 / 15 条断言`）。
    # 处理办法是让清单**逐条声明**它的收尾标记，而不是把默认正则放宽到"含通过即算跑完"——
    # 后者会让"中途 return 前打印过一次通过"也算跑完，等于废掉这条判据。
    done_re = re.compile(s["done_re"]) if s.get("done_re") else DONE_RE
    count = len(ev_re.findall(out))
    if rc == 124:
        return "FAIL", count, "超时（见日志末尾 [TIMEOUT]）"
    if SHELL_SKIP_RE.search(out):
        return "FAIL", count, "输出里出现 [SKIP] 标记：D6.2 禁止隐式跳过"
    if rc != 0:
        tail = [l for l in out.splitlines() if l.strip()][-1:] or [""]
        return "FAIL", count, f"退出码 {rc}；末行：{tail[0][:200]}"
    if count < s["min_cases"]:
        return "FAIL", count, f"证据标记 {count} < min_cases={s['min_cases']}：" \
                              f"退出码 0 但没有跑够断言，按零执行判红（D6.2）"
    if not done_re.search(out):
        return "FAIL", count, f"缺少收尾标记（{done_re.pattern}）：无法证明脚本跑到了最后（D6.2）"
    return "PASS", count, ""


def build_cmd(s: dict, stage: Path, race: bool) -> list[str]:
    if s["kind"] == "go":
        pkg = staged_pkg_dir(s["target"])
        names = go_case_names(pkg)
        excl = set(s.get("exclude_cases") or [])
        cmd = ["go", "test", "-json", "-count=1", "-p", "2",
               "-timeout", f"{s['timeout_s']}s"]
        if race:
            cmd.append("-race")
        if excl:
            keep = [n for n in names if n not in excl]
            cmd += ["-run", "^(" + "|".join(keep) + ")$"]
        cmd.append(s["target"])
        return cmd
    if s["kind"] == "shell":
        return ["bash", s["target"]]
    if s["kind"] == "python":
        return [sys.executable, s["target"], *(s.get("args") or [])]
    if s["kind"] == "make":
        return ["make", "--no-print-directory", s["target"]]
    raise AssertionError(s["kind"])


def main() -> int:
    ap = argparse.ArgumentParser(description="Evergreen 唯一测试入口（合同 D4/D5/D6/D7）")
    ap.add_argument("--profile", required=True, choices=PROFILES)
    ap.add_argument("--suites-file", default=str(DEFAULT_SUITES))
    ap.add_argument("--only", nargs="+", default=None, help="只跑这些 suite id（受 profile 过滤）")
    ap.add_argument("--plan-only", action="store_true", help="只打印执行计划（A2 判据消费）")
    ap.add_argument("--strict", action="store_true", help="发布审计口径：可选 NOT_RUN 升级为退出码 2")
    ap.add_argument("--keep-on-fail", action="store_true", help="失败时保留 staging 现场")
    ap.add_argument("--run-id", default=None)
    ap.add_argument("--report-dir", default=None)
    args = ap.parse_args()

    suites_path = Path(args.suites_file)
    if not suites_path.is_absolute():
        suites_path = REPO / suites_path
    suites = load_suites(suites_path)
    errs = validate(suites, suites_path)
    if errs:
        print(f"[FAIL] 清单自检不通过（{suites_path}），共 {len(errs)} 条：", file=sys.stderr)
        for e in errs:
            print(f"  - {e}", file=sys.stderr)
        return 1

    native = native_platform()
    plan = [s for s in suites if args.profile in s["profiles"]]
    if args.only:
        want = set(args.only)
        unknown = want - {s["id"] for s in plan}
        if unknown:
            die(f"--only 里的 suite 不在 {args.profile} 计划内：{sorted(unknown)}")
        plan = [s for s in plan if s["id"] in want]
    plan.sort(key=lambda s: (LAYERS.index(s["layer"]), s["id"]))
    ids = [s["id"] for s in plan]
    if len(set(ids)) != len(ids):
        die("执行计划出现重复 suite id（去重不变量被破坏）")
    if not plan:
        die(f"profile={args.profile} 的执行计划为空：清单里没有任何 suite 声明该 profile")

    if args.plan_only:
        print(f"# profile={args.profile}  suites={len(plan)}  平台={native}")
        for s in plan:
            print(f"{s['layer']:<11} {s['kind']:<6} {s['id']}\t{s['target']}")
        return 0

    run_id = args.run_id or f"run-{time.strftime('%Y%m%d-%H%M%S')}-{os.getpid()}"
    report_dir = Path(args.report_dir) if args.report_dir else REPO / "tests/_report" / run_id
    (report_dir / "logs").mkdir(parents=True, exist_ok=True)

    kinds = {s["kind"] for s in plan}
    needs_stage = bool(kinds & {"go", "shell"})
    stage = materialize(run_id) if needs_stage else None
    staged_root = str(stage / "evergreen") if stage else ""
    tmp_root = (stage / "tmp") if stage else (report_dir / "tmp")
    tmp_root.mkdir(parents=True, exist_ok=True)

    print(f"== profile={args.profile} suites={len(plan)} run-id={run_id} 平台={native}")
    if stage:
        print(f"== staging={stage}")
    results = []
    t0 = time.time()
    for i, s in enumerate(plan, 1):
        sid = s["id"]
        log = report_dir / "logs" / f"{sid}.log"
        reason = not_run_reason(s, native)
        if reason:
            status = "FAIL" if s["required"] else "NOT_RUN"
            diag = (f"required: true 的 suite 不允许 NOT_RUN（D6.3）：{reason}"
                    if s["required"] else f"{reason}；绑定 {s['issue']}")
            log.write_text(f"[{status}] {diag}\n")
            print(f"[{i:>3}/{len(plan)}] {status:<7} {sid}  ({reason})")
            results.append(dict(id=sid, layer=s["layer"], kind=s["kind"], capability=s["capability"],
                                status=status, duration_s=0.0, case_count=0,
                                log_path=str(log.relative_to(REPO)), required=s["required"],
                                platform_required=s["platform_required"], issue=s["issue"],
                                diagnosis=diag))
            continue

        scratch = tmp_root / sid
        scratch.mkdir(parents=True, exist_ok=True)
        env = dict(os.environ)
        env["TMPDIR"] = str(scratch)
        env["EG_STAGE_KEY"] = sid
        if staged_root:
            env["EG_STAGED_ROOT"] = staged_root
        cwd = Path(staged_root) if (s["kind"] == "go" and staged_root) else REPO
        cmd = build_cmd(s, stage, race=(args.profile == "race"))
        started = time.time()
        rc, out = run_cmd(cmd, cwd, env, s["timeout_s"], log)
        dur = time.time() - started
        status, cases, diag = (judge_go(s, rc, out) if s["kind"] == "go"
                               else judge_script(s, rc, out))
        mark = "PASS" if status == "PASS" else status
        print(f"[{i:>3}/{len(plan)}] {mark:<7} {sid}  {dur:.1f}s  cases={cases}"
              + (f"\n            └─ {diag}" if diag else ""))
        results.append(dict(id=sid, layer=s["layer"], kind=s["kind"], capability=s["capability"],
                            status=status, duration_s=round(dur, 2), case_count=cases,
                            log_path=str(log.relative_to(REPO)), required=s["required"],
                            platform_required=s["platform_required"], issue=s["issue"],
                            diagnosis=diag))

    total = time.time() - t0
    # `duration` 是 `duration_s` 的同值别名：报告消费方（CI、T-…-007 验收）按 duration 读，
    # 单位仍是秒。两个键同时写出，避免下游按哪个名字读都要改。
    for r in results:
        r["duration"] = r["duration_s"]
    npass = sum(1 for r in results if r["status"] == "PASS")
    nfail = sum(1 for r in results if r["status"] == "FAIL")
    nnr = sum(1 for r in results if r["status"] == "NOT_RUN")
    # G-A1（D6.1）：本机平台的必需 suite 不得以 NOT_RUN 计入通过。
    req_nr = sum(1 for r in results if r["status"] == "NOT_RUN" and r["required"])
    native_req_not_pass = [r["id"] for r in results
                           if r["required"] and r["platform_required"] == native
                           and r["status"] != "PASS"]
    summary = dict(
        run_id=run_id, profile=args.profile, platform=native, strict=args.strict,
        started=time.strftime("%Y-%m-%dT%H:%M:%S"), duration_s=round(total, 2),
        suites_planned=len(plan), passed=npass, failed=nfail, not_run=nnr,
        # NOT_RUN 不计 PASS 分子（D6.5）：分母只数真正跑了的。
        pass_rate=(round(npass / max(1, npass + nfail), 4)),
        gate_a={"required_not_run": req_nr,
                "native_required_not_pass": native_req_not_pass,
                "pass": (req_nr == 0 and not native_req_not_pass and nfail == 0)},
        suites=results,
    )
    (report_dir / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
    md = [f"# 测试报告 {run_id}", "",
          f"- profile：`{args.profile}`　平台：`{native}`　strict：{args.strict}",
          f"- 计划 {len(plan)} 个 suite：PASS {npass} / FAIL {nfail} / NOT_RUN {nnr}"
          f"（NOT_RUN 不计 PASS 分子）",
          f"- 总耗时 {total:.1f}s　gate_a.pass={summary['gate_a']['pass']}", "",
          "| suite | 层 | 状态 | 用例 | 耗时(s) | 说明 |", "|---|---|---|---|---|---|"]
    for r in results:
        md.append(f"| `{r['id']}` | {r['layer']} | {r['status']} | {r['case_count']} | "
                  f"{r['duration_s']} | {r['diagnosis'][:120]} |")
    (report_dir / "summary.md").write_text("\n".join(md) + "\n")

    print(f"\n== PASS {npass} / FAIL {nfail} / NOT_RUN {nnr}（计划 {len(plan)}）耗时 {total:.1f}s")
    print(f"== 报告 {report_dir.relative_to(REPO)}/summary.json")
    for r in results:
        if r["status"] == "NOT_RUN":
            print(f"== NOT_RUN 单列：{r['id']}（issue={r['issue']}，不计 PASS 分子）")

    code = 0
    if nfail or req_nr or native_req_not_pass:
        code = 1
    elif nnr and args.strict:
        code = 2
    if stage and not (args.keep_on_fail and code):
        shutil.rmtree(stage, ignore_errors=True)
    elif stage:
        print(f"== --keep-on-fail：保留现场 {stage}")
    return code


if __name__ == "__main__":
    sys.exit(main())
