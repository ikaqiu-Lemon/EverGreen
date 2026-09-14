#!/usr/bin/env bash
# e2e 场景卫生门禁（T-…-004 的自证件）。
#
# 背景：65 支历史 shell e2e 按能力重组进 tests/e2e/<capability>/<scenario>.sh 后，
# 「阶段前缀不再是执行入口」「失败必须传播」「不依赖工作区绝对路径」这些判据
# 不能只靠一次人工 grep —— 它们必须是可复跑、可反证的门禁，否则下一次改脚本就会静默漂移。
#
# 判据（全部为形态/行为判据，**不设任何路径豁免**）：
#   H1 布局：tests/e2e 下的脚本必须恰好位于 tests/e2e/<capability>/<scenario>.sh；
#      文件名不得以 m1..m6 阶段前缀开头（阶段聚合脚本不得再作执行入口）。
#   H2 自报名一致：非注释行里不得出现 `m<N>*.sh` 这类旧脚本名；脚本输出里出现的 `*.sh`
#      必须就是自己的 basename（报告里的 suite 名不许漂移到已退役的入口名）。
#   H3 失败即非零：每支脚本必须启用 errexit + nounset + pipefail。
#   H4 被测命令不得吞错：同一非注释行同时出现「被测二进制调用形态」与 `|| true` 即判否。
#      形态判据 = 行首/管道/命令位上的 `eg ` 或 `"${EG}"` / `$EG` 调用；
#      `grep -c`、`diff`、`ls` 这类**非被测命令**的探测/计数不在此列（它们不产生产品结论）。
#   H5 无绝对路径：不得出现 /workspace、/home/、/Users/ 这类工作区绝对路径。
#   H6 清单双向一致（仅扫真仓时执行）：盘面每支 e2e 脚本都在 inventory.tsv 里 layer=e2e 且有 suite_id；
#      inventory 里每条 e2e shell 资产也必须在盘。
#   H7 归档隔离（仅扫真仓时执行）：tests/archive/ 下的历史材料不得出现在 tests/e2e/ 内，
#      也不得被任何 e2e 脚本当作执行入口 source/bash 调用。
#
# 反证（本脚本自带 self-test）：在 mktemp 合成树上逐条造反例，每条都必须使扫描判否；
# 一份干净样例必须通过。反证失败 == 门禁失效，同样非零退出。
#
# 用法：cd evergreen && bash tests/contract/e2e-hygiene/hygiene.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== 步骤%d：%s ===\n' "${STEP}" "$1"; }
ok() { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die() { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

# ---------------------------------------------------------------------------
# scan_layout <root> —— H1~H5，纯形态扫描，对真仓与合成树同一套代码。
# 命中即把违规写到 stdout 并返回 1。
# ---------------------------------------------------------------------------
scan_layout() {
  local root="$1"
  python3 - "$root" <<'PY'
import os, re, sys

root = sys.argv[1]
e2e = os.path.join(root, "tests", "e2e")
bad = []

# 被测二进制调用形态：**命令位**上的 eg / ${EG} / $EG。
# 判断前先把 "${EG}" 这类调用占位化，再剥离引号内容——否则 die "eg check …" 的文案、
# 以及 grep -cE '^(…|eg |…)' 这种引号内的正则会被误判成调用（实测两类误判都发生过）。
EG_CALL = re.compile(r'(?:^|[;&|(){}]|\$\(|&&|\|\|)\s*(?:__EGCALL__|\$\{EG\}|\$EG|eg)\s')


def command_positions(line: str) -> str:
    t = line.replace('"${EG}"', '__EGCALL__ ').replace('"$EG"', '__EGCALL__ ')
    t = re.sub(r"'[^']*'", "''", t)
    t = re.sub(r'"[^"]*"', '""', t)
    return t
SWALLOW = re.compile(r'\|\|\s*true\b')
OLD_NAME = re.compile(r'm[1-6][A-Za-z0-9_]*\.sh')
ANY_SH = re.compile(r'[A-Za-z0-9_.-]+\.sh')
ABS = re.compile(r'/workspace\b|/home/|/Users/')

if not os.path.isdir(e2e):
    print("H1 tests/e2e 不存在")
    sys.exit(1)

for dirpath, dirnames, filenames in os.walk(e2e):
    rel = os.path.relpath(dirpath, e2e)
    for fn in sorted(filenames):
        if not fn.endswith(".sh"):
            continue
        p = os.path.join(dirpath, fn)
        r = os.path.relpath(p, root)
        # H1 布局：必须恰好一层能力目录
        if rel in (".", ""):
            bad.append(f"H1 {r}：脚本直挂 tests/e2e/，未归入能力目录")
        elif os.sep in rel:
            bad.append(f"H1 {r}：能力目录层级 >1（{rel}）")
        if re.match(r'^m[1-6][_-]', fn):
            bad.append(f"H1 {r}：文件名以阶段前缀开头，阶段聚合脚本不得作执行入口")

        text = open(p, encoding="utf-8", errors="ignore").read()
        lines = text.splitlines()

        # H3 失败即非零：扫全文找 shell 选项行（有脚本把 set 放在长注释块之后）
        m = re.search(r'^set -([A-Za-z]+)\s+(.*)$', text, re.M)
        flags = m.group(1) if m else ""
        rest = m.group(2) if m else ""
        if not ("e" in flags and "u" in flags and "pipefail" in rest):
            bad.append(f"H3 {r}：缺少 errexit+nounset+pipefail（实测 set -{flags} {rest.strip()}）")

        for i, line in enumerate(lines, 1):
            s = line.strip()
            if s.startswith("#"):
                continue
            # H2 自报名
            if OLD_NAME.search(line):
                bad.append(f"H2 {r}:{i}：非注释行出现已退役入口名 {OLD_NAME.search(line).group(0)}")
            for m in ANY_SH.finditer(line):
                tok = m.group(0)
                # 只约束「自报名」——即出现在输出文案里的脚本名，不约束被 source/bash 的真实路径
                if tok != fn and ("printf" in s or "echo" in s):
                    bad.append(f"H2 {r}:{i}：输出文案自报为 {tok}，与自身 {fn} 不一致")
            # H4 被测命令吞错
            if SWALLOW.search(line) and EG_CALL.search(command_positions(line)):
                bad.append(f"H4 {r}:{i}：被测命令调用被 '|| true' 吞错")
            # H5 绝对路径
            if ABS.search(line):
                bad.append(f"H5 {r}:{i}：出现工作区绝对路径")

for b in bad:
    print(b)
sys.exit(1 if bad else 0)
PY
}

# ---------------------------------------------------------------------------
step "H1~H5：真仓 tests/e2e 形态扫描"
out="$(scan_layout "${REPO_ROOT}" || true)"
if [ -n "${out}" ]; then
  printf '%s\n' "${out}" >&2
  die "真仓 e2e 卫生判据未满足（上列每条都是可定位的具体行）"
fi
n_scripts="$(find "${REPO_ROOT}/tests/e2e" -name '*.sh' | wc -l | tr -d ' ')"
n_caps="$(find "${REPO_ROOT}/tests/e2e" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
ok "H1~H5 全部满足：${n_scripts} 支场景脚本 / ${n_caps} 个能力目录，零阶段前缀入口、零吞错、零绝对路径"

# ---------------------------------------------------------------------------
step "H6：盘面与 inventory.tsv 双向一致"
python3 - "${REPO_ROOT}" <<'PY' || exit 1
import csv, os, sys
root = sys.argv[1]
inv = os.path.join(root, "tests", "manifest", "inventory.tsv")
rows = list(csv.DictReader(open(inv, encoding="utf-8"), delimiter="\t"))
declared = {r["path"]: r for r in rows if r["path"].startswith("tests/e2e/") and r["path"].endswith(".sh")}
on_disk = set()
for dp, _, fns in os.walk(os.path.join(root, "tests", "e2e")):
    for fn in fns:
        if fn.endswith(".sh"):
            on_disk.add(os.path.relpath(os.path.join(dp, fn), root))
missing = sorted(on_disk - set(declared))
ghost = sorted(set(declared) - on_disk)
nosuite = sorted(p for p, r in declared.items() if not r.get("suite_id", "").strip("-").strip())
wrong = sorted(p for p, r in declared.items() if r.get("layer") != "e2e")
bad = ([f"H6 未登记：{p}" for p in missing] + [f"H6 幽灵登记：{p}" for p in ghost]
       + [f"H6 缺 suite_id：{p}" for p in nosuite] + [f"H6 layer≠e2e：{p}" for p in wrong])
for b in bad:
    print(b)
print(f"  [ok] H6 双向一致：盘面 {len(on_disk)} == 登记 {len(declared)}，suite_id 齐全，layer 均为 e2e" if not bad else "")
sys.exit(1 if bad else 0)
PY
PASS=$((PASS + 1))

# ---------------------------------------------------------------------------
step "H7：归档材料与执行入口隔离"
if find "${REPO_ROOT}/tests/e2e" -name '*acceptance*.sh' | grep -q .; then
  die "H7 阶段聚合脚本出现在 tests/e2e/（必须归档到 tests/archive/）"
fi
if grep -rnE '(bash|source|\.)[[:space:]]+[^#]*tests/archive/' "${REPO_ROOT}/tests/e2e" --include='*.sh' | grep -vE ':[0-9]+:[[:space:]]*#' | grep -q .; then
  die "H7 e2e 脚本把 tests/archive/ 下历史材料当执行入口"
fi
n_arch="$(find "${REPO_ROOT}/tests/archive" -name '*.sh' 2>/dev/null | wc -l | tr -d ' ')"
ok "H7 归档隔离成立：tests/archive/ 下 ${n_arch} 支历史脚本不在执行面内、不被 e2e 引用"

# ---------------------------------------------------------------------------
step "反证：合成树上逐条造反例，门禁必须判否"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/eg-hygiene.XXXXXX")"
trap 'rm -rf "${TMP}"' EXIT

mk_clean() { # $1=root
  mkdir -p "$1/tests/e2e/card-query"
  cat >"$1/tests/e2e/card-query/search.sh" <<'S'
#!/usr/bin/env bash
set -Eeuo pipefail
# 历史来源：m2_acceptance.sh L120-140（注释里出现旧名是允许的）
eg search "x" >/dev/null
printf '=== search.sh 全部通过 ===\n'
S
}

expect_reject() { # $1=root $2=说明
  local o
  o="$(scan_layout "$1" || true)"
  [ -n "${o}" ] || die "反证失效：$2 未被判否"
  ok "反例判否：$2 → $(printf '%s' "${o}" | head -1)"
}

C="${TMP}/clean"; mk_clean "${C}"
o="$(scan_layout "${C}" || true)"
[ -z "${o}" ] || { printf '%s\n' "${o}" >&2; die "反证失效：干净合成树被误判"; }
ok "正例通过：干净合成树零违规（注释中的历史来源旧名不判否）"

R="${TMP}/r1"; mk_clean "${R}"; mv "${R}/tests/e2e/card-query/search.sh" "${R}/tests/e2e/search.sh"
expect_reject "${R}" "H1 脚本直挂 tests/e2e/"

R="${TMP}/r2"; mk_clean "${R}"; mv "${R}/tests/e2e/card-query/search.sh" "${R}/tests/e2e/card-query/m2_acceptance.sh"
expect_reject "${R}" "H1 文件名以阶段前缀开头"

R="${TMP}/r3"; mk_clean "${R}"
printf 'printf "=== m5_index_build.sh 通过 ===\\n"\n' >>"${R}/tests/e2e/card-query/search.sh"
expect_reject "${R}" "H2 非注释行自报已退役入口名"

R="${TMP}/r4"; mk_clean "${R}"
python3 - "${R}/tests/e2e/card-query/search.sh" <<'PY'
import sys
p = sys.argv[1]
s = open(p).read().replace("set -Eeuo pipefail", "set -e")
open(p, "w").write(s)
PY
expect_reject "${R}" "H3 缺 nounset/pipefail"

R="${TMP}/r5"; mk_clean "${R}"
printf 'eg check --json >/dev/null || true\n' >>"${R}/tests/e2e/card-query/search.sh"
expect_reject "${R}" "H4 被测命令 eg 被 '|| true' 吞错"

R="${TMP}/r6"; mk_clean "${R}"
printf '"${EG}" rel --json >/dev/null || true\n' >>"${R}/tests/e2e/card-query/search.sh"
expect_reject "${R}" "H4 \${EG} 形态同样判否"

R="${TMP}/r7"; mk_clean "${R}"
printf 'VAULT=/workspace/iris/vault\n' >>"${R}/tests/e2e/card-query/search.sh"
expect_reject "${R}" "H5 工作区绝对路径"

R="${TMP}/r8"; mk_clean "${R}"; mkdir -p "${R}/tests/e2e/card-query/sub"
cp "${R}/tests/e2e/card-query/search.sh" "${R}/tests/e2e/card-query/sub/deep.sh"
expect_reject "${R}" "H1 能力目录层级 >1"

R="${TMP}/r9"; mk_clean "${R}"
printf 'grep -c foo bar.txt || true\n' >>"${R}/tests/e2e/card-query/search.sh"
o="$(scan_layout "${R}" || true)"
[ -z "${o}" ] || { printf '%s\n' "${o}" >&2; die "H4 过宽：非被测命令的计数型 '|| true' 被误判"; }
ok "H4 不过宽：grep -c 之类非被测命令的 '|| true' 不判否（双侧成立）"

printf '\n[PASS] e2e 场景卫生门禁通过（H1~H7 + 9 组反证；共 %d 步 / %d 条断言）\n' "${STEP}" "${PASS}"
