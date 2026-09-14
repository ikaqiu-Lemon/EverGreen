#!/usr/bin/env python3
"""staging materializer —— 把权威测试树物化成「运行期布局」的独立副本。

合同：teamwork/projects/evergreen/system_assurance/docs/specs/2027-03-01-system-test-architecture-contract.md
  · D3.2 默认 **独立 copy**；reflink（CoW）仅在文件系统支持时作为等价加速；
    **禁止 hardlink / symlink**（hardlink 共享 inode → staging 内的写 / truncate / chmod 会污染真源码；
    symlink 已被 go:embed 证伪）。本文件中不存在 os.link 调用。
  · D3.3 布局：<stage>/evergreen（产品源码 + 回填的测试 / 语料）、<stage>/teamwork（合同快照）、<stage>/tmp。
  · D3.4 自检六项，任一命中非零退出。
  · D3.6 **物化安全**（本版新增）：
      - run-id 受字符集约束，且 <stage> 必须真实位于隔离根 .tests-staging/ 之内；
      - runtime_map 的 runtime_path / repo_path 必须是相对路径、无 `..`、非绝对，解析后仍在隔离根内；
      - 旧 run 清理**不按 mtime 猜活跃**：先 flock 探活 + pid 存活判定，活跃 run 一律不删；
        删除目标必须是隔离根的直接子目录且 run-id 合法，任何越界一律拒绝。
  · D3.7 **残留自恢复与输出字段合同**（本版新增）：
      - 同名 run 已存在时**不再直接中断**：先按「活体证据」判定持有者（flock 被占用 /
        materialize.json 的 pid 存活）。有持有者 → 拒绝抢占（退出码 3）；无持有者 →
        判为中断残骸，删除后继续，并在报告里留 `stale_reclaimed=<证据>`。
        `--on-stale fail` 可退回旧的严格失败语义（门禁反例用）。
      - `--run-id` 默认唯一（时间戳 + pid + 随机后缀），同秒内连续物化不撞名。
      - **输出字段合同**：staging 路径只有一个键 —— `stage`。消费方一律读 `stage`；
        `stage_root` 只是本文件内部的隔离根局部变量，**不在** JSON 里，禁止消费方引用
        （反例由 tests/contract/staging-isolation/materialize_safety.sh 锁死）。
  · D10 **可分发性**（本版新增）：源码列举不得硬依赖 `git ls-files`。
      - 有 `.git` 且 git 可用 → git 列举（权威，天然遵守 .gitignore），并与文件系统列举做包含性自检；
      - 无 `.git`（源码分发包）→ 文件系统列举 + 忽略规则，功能等价，**不退化、不 NOT_RUN**。
  · 磁盘：物化前做前置检查（I-005 经验：ENOSPC 会伪装成产品缺陷），并按生命周期清理旧 run。

用法：
  python3 tests/runner/materialize.py --run-id <id> [--mode copy|reflink] [--keep 2]
                                      [--source-mode auto|git|walk] [--no-cleanup] [--json]
输出：
  stdout 打印 staging 根路径；--json 时输出物化报告 JSON。
退出码：0 成功 / 1 自检失败 / 3 环境（磁盘 / 前置）不足。
"""
from __future__ import annotations
import argparse, fcntl, hashlib, json, os, re, shutil, subprocess, sys, time
from pathlib import Path

EXIT_SELFCHECK = 1
EXIT_ENV = 3

STAGE_DIRNAME = ".tests-staging"
RUN_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
RUN_META = "materialize.json"
RUN_LOCK = ".materialize.lock"

# 文件系统列举时的忽略规则（walk 模式；与 .gitignore 的最小等价闭集）。
IGNORE_DIRS = {".git", STAGE_DIRNAME, ".index", "node_modules", "bin", "dist", ".idea", ".vscode"}
IGNORE_SUFFIX = (".test", ".out", ".prof", ".iml")
IGNORE_NAMES = {".DS_Store"}
IGNORE_PREFIXES = ("tests/_report/",)


def die(msg: str, code: int = EXIT_SELFCHECK):
    print(f"[FAIL] {msg}", file=sys.stderr)
    sys.exit(code)


def repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


# ------------------------------------------------------------------ D10 源码列举
def git_ok(repo: Path) -> bool:
    if not (repo / ".git").exists():
        return False
    r = subprocess.run(["git", "-C", str(repo), "rev-parse", "--is-inside-work-tree"],
                       capture_output=True, text=True)
    return r.returncode == 0


def git_ls(repo: Path, *paths: str) -> list[str]:
    r = subprocess.run(["git", "-C", str(repo), "ls-files", *paths], capture_output=True, text=True)
    if r.returncode != 0:
        die(f"git ls-files 失败：{r.stderr.strip()[:300]}")
    return [f for f in r.stdout.splitlines() if f]


def walk_ls(repo: Path, *paths: str) -> list[str]:
    """无 .git 时的等价列举：文件系统遍历 + 忽略规则（D10）。"""
    roots = [repo / p for p in paths] if paths else [repo]
    out: list[str] = []
    for root in roots:
        if root.is_file():
            out.append(str(root.relative_to(repo)))
            continue
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = sorted(d for d in dirnames if d not in IGNORE_DIRS)
            for fn in sorted(filenames):
                if fn in IGNORE_NAMES or fn.endswith(IGNORE_SUFFIX):
                    continue
                rel = str((Path(dirpath) / fn).relative_to(repo))
                if rel.startswith(IGNORE_PREFIXES):
                    continue
                out.append(rel)
    return sorted(out)


class Lister:
    """源码列举器：git 与 walk 两条等价实现走同一接口（D10：分发包无 .git 也必须能物化）。"""

    def __init__(self, repo: Path, mode: str):
        self.repo = repo
        if mode == "git" and not git_ok(repo):
            die("--source-mode git 但仓库无 .git 或 git 不可用（分发包请用 auto / walk）")
        self.mode = ("git" if git_ok(repo) else "walk") if mode == "auto" else mode
        self.notes: list[str] = []
        if self.mode == "walk":
            self.notes.append("源码列举走文件系统遍历（无 .git 或显式指定），不依赖 git 元数据")

    def ls(self, *paths: str) -> list[str]:
        return git_ls(self.repo, *paths) if self.mode == "git" else walk_ls(self.repo, *paths)

    def crosscheck(self) -> None:
        """git 可用时断言 git 列举 ⊆ walk 列举 —— 保证分发包（walk 模式）不会漏件。"""
        if self.mode != "git":
            return
        tracked, walked = set(git_ls(self.repo)), set(walk_ls(self.repo))
        missing = sorted(tracked - walked)
        if missing:
            die("D10 自检失败：文件系统列举漏掉受版本控制的文件 "
                f"{len(missing)} 个（分发包会缺件）：{missing[:5]}")
        extra = sorted(walked - tracked)
        if extra:
            self.notes.append(f"walk 多出 {len(extra)} 个未跟踪文件（分发包不含；本次按 git 列举物化）")


def avail_mib(path: Path) -> int:
    st = os.statvfs(path)
    return int(st.f_bavail * st.f_frsize / (1024 * 1024))


def total_size_mib(repo: Path, files: list[str]) -> float:
    total = sum((repo / f).stat().st_size for f in files if (repo / f).is_file())
    return total / (1024 * 1024)


def read_runtime_map(repo: Path) -> list[dict]:
    path = repo / "tests/manifest/runtime_map.tsv"
    if not path.exists():
        die("缺 tests/manifest/runtime_map.tsv（D3.3 要求显式登记运行期布局）")
    out = []
    lines = path.read_text().splitlines()
    header = lines[0].split("\t")
    for ln in lines[1:]:
        if not ln.strip():
            continue
        out.append(dict(zip(header, ln.split("\t"))))
    return out


# ------------------------------------------------------------------ D3.6 路径约束
def safe_rel(rel: str, what: str) -> Path:
    """把清单里的相对路径收敛为安全相对路径；越界一律 die（清单是可编辑文本，必须当不可信输入）。"""
    if not rel or rel.startswith("/") or rel.startswith("~"):
        die(f"D3.6 拒绝{what}绝对路径：{rel!r}")
    p = Path(rel)
    if p.is_absolute() or any(part in ("..", "") for part in p.parts):
        die(f"D3.6 拒绝{what}含 `..` 或空段的路径：{rel!r}")
    if "\\" in rel or "\n" in rel:
        die(f"D3.6 拒绝{what}含非法字符的路径：{rel!r}")
    return p


def inside(root: Path, target: Path) -> bool:
    """target 解析后是否位于 root 之内（解析可阻断 symlink 逃逸）。"""
    try:
        r, t = root.resolve(strict=False), target.resolve(strict=False)
    except OSError:
        return False
    return r == t or r in t.parents


def assert_in_stage(stage: Path, target: Path, what: str) -> Path:
    if not inside(stage, target):
        die(f"D3.6 {what}落到隔离根之外：{target}（隔离根 {stage}）")
    return target


# ------------------------------------------------------------------ D3.6 活跃 run 探活
def run_is_active(run: Path) -> tuple[bool, str]:
    """活跃判定：flock 探活 → 记录 pid 存活 → 元数据缺失/不可解析时保守视为活跃。"""
    lock = run / RUN_LOCK
    if lock.exists():
        try:
            fd = os.open(lock, os.O_RDWR)
        except OSError:
            return True, "锁文件不可打开（保守视为活跃）"
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            fcntl.flock(fd, fcntl.LOCK_UN)
        except OSError:
            return True, "flock 被占用（有活跃持有者）"
        finally:
            os.close(fd)
    meta = run / RUN_META
    if not meta.is_file():
        return True, "缺 materialize.json（来源不明，保守不删）"
    try:
        pid = int(json.loads(meta.read_text()).get("pid") or 0)
    except (ValueError, OSError, json.JSONDecodeError):
        return True, "materialize.json 不可解析（保守不删）"
    if pid > 0:
        try:
            os.kill(pid, 0)
            return True, f"物化进程 pid={pid} 仍存活"
        except ProcessLookupError:
            pass
        except PermissionError:
            return True, f"pid={pid} 存在（他人持有）"
    return False, "无锁持有者且 pid 已退出"


# ------------------------------------------------------------------ D3.7 同名 run 占用判定
def same_name_holder(run: Path) -> tuple[bool, str]:
    """同名 staging 目录**是否有活着的持有者**。

    与 `run_is_active()` 的保守取向刻意不同，因为要回答的是两个不同的问题：

      · `run_is_active()` 服务于**额度淘汰**（删别人的 run）。那里"证据不足"必须站在
        不删一侧——误删正在跑的 run 会制造"文件突然消失"这类伪产品缺陷。
      · 本函数服务于**同名回收**（复用自己指定的 run-id）。那里"证据不足"若也站在
        不删一侧，就会出现实测到的死结：上一次物化在写 materialize.json **之前**被
        中断，留下半个目录，此后每一次同名物化都被 D3.4-5 判成"残留污染"直接中断，
        必须人工 `rm -rf` 才能继续——门禁在保护一个**没有持有者**的空壳。

    因此这里只认**活体证据**，且两条证据都以进程存活为前提：
      1) flock 被占用 —— 锁随持有进程退出自动释放，占用即证明有活体；
      2) materialize.json 里的 pid 仍存活（或存在但属他人，PermissionError）。

    元数据缺失 / 不可解析**不再**视为活跃：若真有活体，第 1 条早已命中。
    """
    lock = run / RUN_LOCK
    if lock.exists():
        try:
            fd = os.open(lock, os.O_RDWR)
        except OSError:
            return True, "锁文件不可打开（保守视为有持有者）"
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            fcntl.flock(fd, fcntl.LOCK_UN)
        except OSError:
            return True, "flock 被占用（有活跃持有者）"
        finally:
            os.close(fd)
    meta = run / RUN_META
    if meta.is_file():
        try:
            pid = int(json.loads(meta.read_text()).get("pid") or 0)
        except (ValueError, OSError, json.JSONDecodeError):
            return False, "materialize.json 不可解析且无锁持有者（判为残骸）"
        if pid > 0:
            try:
                os.kill(pid, 0)
                return True, f"物化进程 pid={pid} 仍存活"
            except ProcessLookupError:
                return False, f"pid={pid} 已退出（判为残骸）"
            except PermissionError:
                return True, f"pid={pid} 存在（他人持有）"
        return False, "materialize.json 未记录有效 pid（判为残骸）"
    return False, "无锁持有者且无 materialize.json（中断残骸）"


def copy_file(src: Path, dst: Path, mode: str) -> str:
    dst.parent.mkdir(parents=True, exist_ok=True)
    if mode == "reflink":
        r = subprocess.run(["cp", "--reflink=always", "-p", str(src), str(dst)],
                           capture_output=True, text=True)
        if r.returncode == 0:
            return "reflink"
        # CoW 不支持 → 退化为独立 copy（隔离性等价，见 D3.2）
    shutil.copy2(src, dst)
    return "copy"


def cleanup_old_runs(stage_root: Path, keep: int, current: str) -> tuple[list[str], list[str]]:
    """按「非活跃 + 超出保留额度」删除旧 run；返回 (已删, 保留原因)。

    D3.6：**绝不以 mtime 判活跃** —— 长跑 suite 的 stage 目录 mtime 可以很旧，
    按 mtime 淘汰会把正在跑的 run 拆掉，制造出「文件突然消失」这类伪产品缺陷。
    """
    if not stage_root.exists():
        return [], []
    removed: list[str] = []
    skipped: list[str] = []
    cands: list[Path] = []
    root_res = stage_root.resolve()
    for d in sorted(stage_root.iterdir()):
        if d.name == current:
            continue
        if d.is_symlink() or not d.is_dir():
            skipped.append(f"{d.name}: 非普通目录（不删）")
            continue
        if not RUN_ID_RE.match(d.name):
            skipped.append(f"{d.name}: run-id 不合法（不删，交人工核查）")
            continue
        if d.resolve().parent != root_res:
            skipped.append(f"{d.name}: 不是隔离根的直接子目录（不删）")
            continue
        active, why = run_is_active(d)
        if active:
            skipped.append(f"{d.name}: 活跃（{why}）")
            continue
        cands.append(d)
    cands.sort(key=lambda d: d.stat().st_mtime)   # 仅用于非活跃候选之间的额度淘汰次序
    budget = max(keep - 1, 0)
    while len(cands) > budget:
        victim = cands.pop(0)
        if victim.resolve().parent != root_res or victim.name == current:
            skipped.append(f"{victim.name}: 二次校验未过（不删）")
            continue
        shutil.rmtree(victim, ignore_errors=True)
        removed.append(victim.name)
    return removed, skipped


def default_run_id() -> str:
    """默认 run-id 唯一化（D3.7）：时间戳到秒 + pid + 4 位随机。

    旧默认只到秒，同秒内两次物化（CI 并行 job、脚本里连着调两次）会撞名，
    于是"残留污染"这条判据被日常操作触发——那不是污染，是撞名。
    """
    return f"{time.strftime('%Y%m%dT%H%M%S')}-{os.getpid()}-{os.urandom(2).hex()}"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--run-id", default=default_run_id(),
                    help="默认唯一（时间戳+pid+随机后缀），并发物化天然不撞名")
    ap.add_argument("--mode", choices=["copy", "reflink"], default="copy",
                    help="默认 copy（独立副本）；reflink 为 CoW 加速，不支持时自动退化 copy")
    ap.add_argument("--source-mode", choices=["auto", "git", "walk"], default="auto",
                    help="源码列举：auto=有 .git 用 git，否则文件系统遍历（D10 分发包可运行）")
    ap.add_argument("--keep", type=int, default=2, help="保留最近 N 个**非活跃** staging run（含本次）")
    ap.add_argument("--no-cleanup", action="store_true", help="不清理旧 run（排障用）")
    ap.add_argument("--on-stale", choices=["reclaim", "fail"], default="reclaim",
                    help="同名 run 已存在且**无活体持有者**时：reclaim=删残骸后继续（默认，D3.7）；"
                         "fail=保持旧严格语义直接失败（门禁反例用）")
    ap.add_argument("--min-free-mib", type=int, default=0,
                    help="磁盘前置检查阈值；0 = 自动（源码体积 ×4 + 512 MiB）")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    repo = repo_root()
    stage_root = repo / STAGE_DIRNAME
    stale_reclaimed: str | None = None   # D3.7：本次是否回收了同名残骸（进报告，便于排障）

    # ---------------- D3.6 run-id 与隔离根约束（先于任何写 / 删动作）
    if not RUN_ID_RE.match(args.run_id):
        die(f"D3.6 run-id 非法：{args.run_id!r}（只许 [A-Za-z0-9._-]、首位字母数字、≤64 字符）")
    if stage_root.is_symlink():
        die(f"D3.6 隔离根是 symlink，拒绝物化：{stage_root}")
    stage_root.mkdir(parents=True, exist_ok=True)
    stage = stage_root / args.run_id
    assert_in_stage(stage_root, stage, "stage 目录")
    if stage.resolve().parent != stage_root.resolve():
        die(f"D3.6 stage 不是隔离根的直接子目录：{stage}")
    if stage.exists():
        # D3.7 同名残骸安全自恢复：只在**确认无活体持有者**时回收，绝不抢占活跃 run。
        if stage.is_symlink() or not stage.is_dir():
            die(f"D3.6 同名 staging 目标不是普通目录（symlink / 文件），拒绝处理：{stage}")
        held, why = same_name_holder(stage)
        if held:
            die(f"同名 staging run 正在被占用，拒绝抢占：{stage}（{why}）"
                f"—— 换一个 --run-id，或等持有者退出。", EXIT_ENV)
        if args.on_stale == "fail":
            die(f"staging 目标已存在且非本次生成：{stage}（D3.4-5 残留污染；{why}；"
                f"当前 --on-stale=fail）")
        # 删除前重做一次越界校验：只删隔离根的直接子目录，且 run-id 合法（已在上方校验）
        if stage.resolve().parent != stage_root.resolve():
            die(f"D3.6 回收目标不是隔离根的直接子目录，拒绝删除：{stage}")
        shutil.rmtree(stage, ignore_errors=False)
        stale_reclaimed = why

    lister = Lister(repo, args.source_mode)
    all_files = lister.ls()
    lister.crosscheck()

    # ---------------- 环境前置：磁盘（I-005：ENOSPC 会伪装成产品缺陷，必须提前判定）
    size = total_size_mib(repo, all_files)
    need = args.min_free_mib or int(size * 4 + 512)
    removed, kept = ([], []) if args.no_cleanup else cleanup_old_runs(stage_root, args.keep, args.run_id)
    free = avail_mib(repo)
    if free < need:
        die(f"磁盘可用 {free} MiB < 需要 {need} MiB（源码 {size:.1f} MiB）；"
            f"已清理旧 staging run {removed or '无'}。这是环境约束，不是产品缺陷。", EXIT_ENV)

    # ---------------- 1. 产品源码（排除 tests/ 权威树）
    product = [f for f in all_files if not f.startswith("tests/")]
    prod_tests = [f for f in product if f.endswith("_test.go")]
    if prod_tests:
        die(f"产品树残留 {len(prod_tests)} 个 *_test.go（D3.4-1 物理分离未成立）：{prod_tests[:5]}")

    stage.mkdir(parents=True)
    (stage / RUN_LOCK).write_text("")   # 供 runner 以 flock 持有；清理时据此探活（D3.6）
    modes_used = set()
    for rel in product:
        src = repo / rel
        if src.is_file():
            dst = assert_in_stage(stage, stage / "evergreen" / safe_rel(rel, "产品文件"), "产品文件落点")
            modes_used.add(copy_file(src, dst, args.mode))

    # ---------------- 1b. 权威测试树本体（tests/**，排除 _staged：其内容已回填到包目录）
    #   e2e / contract / manifest / lib / archive 需要在 staging 内可读：
    #   ① 元测试要扫描 tests/e2e 与 tests/manifest/migration.tsv；② shell 套件可从 staging 复跑。
    tests_tree = [f for f in lister.ls("tests") if not f.startswith("tests/_staged/")]
    for rel in tests_tree:
        src = repo / rel
        if src.is_file():
            dst = assert_in_stage(stage, stage / "evergreen" / safe_rel(rel, "测试树文件"), "测试树落点")
            modes_used.add(copy_file(src, dst, args.mode))

    # ---------------- 2. 按 runtime_map 回填测试 / 语料 / 合同快照
    rmap = read_runtime_map(repo)
    ghosts = [r["repo_path"] for r in rmap if not (repo / r["repo_path"]).is_file()]
    if ghosts:
        die(f"runtime_map 幽灵条目 {len(ghosts)} 条（D3.4-2）：{ghosts[:5]}")

    registered = {r["repo_path"] for r in rmap}
    orphans = [f for f in lister.ls("tests/_staged", "tests/fixtures")
               if f not in registered and not f.endswith("SHA256SUMS")]
    if orphans:
        die(f"tests/ 下未登记的运行期资产 {len(orphans)} 条（D3.4-2 孤儿）：{orphans[:5]}")

    per_layer: dict[str, int] = {}
    for r in rmap:
        if r["root"] not in ("evergreen", "staging"):
            die(f"D3.6 runtime_map root 非法：{r['root']!r}（只许 evergreen / staging）")
        base = stage / "evergreen" if r["root"] == "evergreen" else stage
        dst = assert_in_stage(stage, base / safe_rel(r["runtime_path"], "runtime_path"), "回填落点")
        modes_used.add(copy_file(repo / safe_rel(r["repo_path"], "repo_path"), dst, args.mode))
        per_layer[r["layer"]] = per_layer.get(r["layer"], 0) + 1

    # ---------------- 3. 回填计数自检（D3.4-3）
    staged_tests = sorted(p for p in (stage / "evergreen").rglob("*_test.go")
                          if "tests/" not in str(p.relative_to(stage / "evergreen")))
    want = sum(1 for r in rmap if r["repo_path"].endswith("_test.go"))
    if len(staged_tests) != want:
        die(f"回填后 staging 测试文件 {len(staged_tests)} ≠ 登记数 {want}（D3.4-3）")

    # ---------------- 4. 合同快照校验和（D3.4-4 / D8：快照即必需真源）
    sums = repo / "tests/fixtures/contracts/SHA256SUMS"
    if not sums.exists():
        die("缺 tests/fixtures/contracts/SHA256SUMS（D8：快照必须自带校验和）")
    bad = []
    croot = repo / "tests/fixtures/contracts"
    for ln in sums.read_text().splitlines():
        if not ln.strip():
            continue
        want_hash, rel = ln.split("  ", 1)
        f = croot / rel
        if not f.is_file() or hashlib.sha256(f.read_bytes()).hexdigest() != want_hash:
            bad.append(rel)
    if bad:
        die(f"合同快照校验和不匹配 {len(bad)} 项（D3.4-4 / D8 → FAIL 而非 NOT_RUN）：{bad[:5]}")

    # ---------------- 5. 隔离自检（D3.4-6）：不得与真实仓共享 inode，nlink 必须为 1
    leaks = []
    sample = staged_tests[:50] + [stage / "evergreen" / p for p in product[:50]]
    for p in sample:
        if not p.is_file():
            continue
        st = p.stat()
        if st.st_nlink != 1:
            leaks.append(f"{p} nlink={st.st_nlink}")
            continue
        rel = p.relative_to(stage / "evergreen")
        origin = repo / rel
        if origin.is_file():
            ost = origin.stat()
            if (ost.st_dev, ost.st_ino) == (st.st_dev, st.st_ino):
                leaks.append(f"{p} 与真源同 inode")
    if leaks:
        die(f"staging 与真实仓共享 inode / 硬链接泄漏（D3.4-6，禁止 hardlink）：{leaks[:5]}")

    # ---------------- 6. staging 独立 git 仓（支撑「工作区零污染」类断言与 Makefile COMMIT）
    #   git 二进制缺失（极简分发环境）时不阻断物化：只在报告里标注 staging_git=false，
    #   由消费方（runner）决定是否跳过依赖 git 的断言 —— 但**物化本身不依赖 .git**（D10）。
    (stage / "tmp").mkdir(parents=True, exist_ok=True)
    ev = stage / "evergreen"
    staging_git = False
    if shutil.which("git"):
        try:
            subprocess.run(["git", "init", "-q", str(ev)], check=True, capture_output=True)
            subprocess.run(["git", "-C", str(ev), "add", "-A"], check=True, capture_output=True)
            subprocess.run(["git", "-C", str(ev), "-c", "user.name=eg-staging",
                            "-c", "user.email=eg-staging@example.com", "-c", "commit.gpgsign=false",
                            "commit", "-q", "-m", f"staging baseline {args.run_id}"],
                           check=True, capture_output=True)
            staging_git = True
        except subprocess.CalledProcessError as e:
            die(f"staging 内 git 初始化失败：{(e.stderr or b'').decode(errors='replace')[:200]}")

    report = {
        "run_id": args.run_id,
        "stage": str(stage),
        "pid": os.getpid(),
        "started_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "mode_requested": args.mode,
        "mode_effective": sorted(modes_used),
        "source_mode": lister.mode,
        "source_notes": lister.notes,
        "product_files": len(product),
        "tests_tree_files": len(tests_tree),
        "runtime_map_entries": len(rmap),
        "staged_test_files": len(staged_tests),
        "per_layer": per_layer,
        "contract_snapshot_files": len(sums.read_text().splitlines()),
        "free_mib_before": free,
        "min_free_mib": need,
        "cleaned_runs": removed,
        "kept_runs": kept,
        "staging_git": staging_git,
        "hardlink_used": False,
        # D3.7：同名残骸回收留痕。None = 本次是干净新建；字符串 = 回收原因（判为残骸的证据）。
        "stale_reclaimed": stale_reclaimed,
        "on_stale": args.on_stale,
    }
    (stage / "materialize.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
    if args.json:
        print(json.dumps(report, ensure_ascii=False))
    else:
        print(stage)
    return 0


if __name__ == "__main__":
    sys.exit(main())
