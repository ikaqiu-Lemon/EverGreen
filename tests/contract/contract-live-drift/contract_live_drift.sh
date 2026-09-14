#!/usr/bin/env bash
# 合同快照 ↔ 真实 teamwork 双侧漂移比对（system_assurance · T-…-006，合同 D8；
# suite id: contract.contract-live-drift；**可选 suite**：required=false，
# not_run_when=no_sibling_teamwork，绑 I-evergreen.s1_main_flow-158614-003；
# sibling 可由 EG_TEAMWORK_ROOT 指定，或自动识别 ../teamwork / ../Teamwork）
#
# 判据来源：合同 D8 的 suite 划分表 —— 快照是**必需真源**（由 contract.contract-snapshot
# 单独把守），而"快照有没有落后于真实合同"是**可选**判据：有真源在场时不一致必须判红，
# 没有真源（单仓分发包）时单列 NOT_RUN 且**不影响退出码**。这条分级是 D8 的核心：
# 既不让单仓跑不过，也不让快照可以悄悄过期。
#
#   L1 正向：快照里每个文件与 sibling 同路径经公开脱敏投影后**逐字节**一致（cmp，不是 diff 语义比对）。
#   L2 反向：sibling 在快照覆盖目录下的文件集合与快照集合**相等** —— 只做正向的话，
#      「把落后的文件从快照里删掉」就能让漂移消失，等于用减覆盖换绿（D9 禁止）。
#   L3 反例：临时副本上改一个字节 / 删一个文件，L1、L2 必须分别转红。
#
# 约束：离线、只读；反例只在 mktemp 副本上构造。sibling 不在场时以退出码 3 报环境不足
# （runner 会按 not_run_when 先判 NOT_RUN，这里的 3 只是直跑时的兜底语义）。
# 用法：cd evergreen && bash tests/contract/contract-live-drift/contract_live_drift.sh
set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"

SNAP="${REPO_ROOT}/tests/fixtures/contracts"
LIVE="${EG_TEAMWORK_ROOT:-}"
if [ -z "${LIVE}" ]; then
  for cand in "${REPO_ROOT}/../teamwork" "${REPO_ROOT}/../Teamwork"; do
    if [ -d "${cand}" ]; then
      LIVE="${cand}"
      break
    fi
  done
fi

if [ -z "${LIVE}" ] || [ ! -d "${LIVE}" ]; then
  printf '[ENV] 无 sibling teamwork（可用 EG_TEAMWORK_ROOT 指定，或放置于 ../teamwork / ../Teamwork）：本 suite 是可选的 live 漂移比对，\n' >&2
  printf '      按合同 D8 应单列 NOT_RUN 并绑 I-…-003，不影响退出码。\n' >&2
  exit 3
fi

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-live-drift.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

sanitize_live_doc() {
  python3 - "$1" "$2" <<'PY'
from pathlib import Path
import re
import sys
src, dst = map(Path, sys.argv[1:3])
s = src.read_text(encoding="utf-8")
corp = "byte" + "dance"
doc_host = "lark" + "office.com"
agent_mark = "AI" + "ME"
mail_token = "EMAIL_" + "5d0bc4"
owner_user = "zhou" + "hang.26"
owner_name = "周" + "航"
pairs = [
    ("https://" + corp + "." + doc_host + "/wiki/LB73w54iOiXN96keHVLcK38JnSe", "https://docs.example.invalid/evergreen/design"),
    ("https://" + corp + "." + doc_host + "/wiki/EwnAwqUqhiuVjmkYdDDcqA2Vndf", "https://docs.example.invalid/evergreen/review"),
    ("https://" + agent_mark.lower() + "." + corp + ".net/chat/", "https://agent.example.invalid/chat/"),
    (agent_mark.lower() + "." + corp + ".net", "agent.example.invalid"),
    (agent_mark + " PPE", "Agent Harness E2E"),
    ("真实 " + agent_mark, "真实 Agent Harness"),
    (agent_mark + " Agent", "Agent Harness Agent"),
    (agent_mark + " 原始报告", "Agent Harness 原始报告"),
    (agent_mark, "Agent Harness"),
    (owner_user, "maintainer"),
    (owner_name, "项目维护者"),
    ("module evergreen / go 1.22", "module github.com/ikaqiu-Lemon/EverGreen / go 1.22"),
    ("evergreen/internal/", "github.com/ikaqiu-Lemon/EverGreen/internal/"),
]
for old, new in pairs:
    s = s.replace(old, new)
s = re.sub(r"<?[A-Za-z0-9._%+-]+@" + re.escape(corp) + r"\.com>?", "maintainer@example.com", s)
s = re.sub(r"<<EMAIL_[A-Za-z0-9]+>>", "maintainer@example.com", s)
s = re.sub(r"<EMAIL_[A-Za-z0-9]+>", "maintainer@example.com", s)
s = s.replace("项目维护者 `maintainer@example.com`", "项目维护者 `<maintainer@example.com>`")
s = s.replace("项目维护者 maintainer@example.com", "项目维护者 <maintainer@example.com>")
dst.write_text(s, encoding="utf-8")
PY
}

# 快照相对路径清单（不含校验和文件本身）
( cd "${SNAP}" && find . -type f ! -name SHA256SUMS | sed 's|^\./||' | sort ) >"${WORK}/snap.list"
SNAP_N="$(grep -c . "${WORK}/snap.list")"
[ "${SNAP_N}" -ge 10 ] || die "快照只有 ${SNAP_N} 个文件，比对判据不得空转"

step "L1 正向：快照 ${SNAP_N} 个文件与真实 teamwork 逐字节一致"
DRIFT=0
while read -r rel; do
  [ -n "${rel}" ] || continue
  if [ ! -f "${LIVE}/${rel}" ]; then
    printf '  真源缺失：%s（快照登记了，真实仓没有）\n' "${rel}" >&2
    DRIFT=$((DRIFT + 1))
    continue
  fi
  sanitize_live_doc "${LIVE}/${rel}" "${WORK}/live.proj"
  cmp -s "${SNAP}/${rel}" "${WORK}/live.proj" ||
    { printf '  漂移：%s\n' "${rel}" >&2; DRIFT=$((DRIFT + 1)); }
done <"${WORK}/snap.list"
[ "${DRIFT}" -eq 0 ] ||
  die "${DRIFT} 个合同文档与真实 teamwork 不一致：快照已过期，必须重取快照并重跑必需断言"
ok "逐字节一致（cmp），零漂移"

step "L2 反向：真实 teamwork 在快照覆盖目录下的文件集合与快照集合相等"
( cd "${WORK}" && sed 's|/[^/]*$||' snap.list | sort -u ) >"${WORK}/dirs.list"
: >"${WORK}/live.list"
while read -r d; do
  [ -n "${d}" ] || continue
  [ -d "${LIVE}/${d}" ] || die "真实仓缺目录 ${d}：覆盖面比对无法进行"
  ( cd "${LIVE}" && find "${d}" -maxdepth 1 -type f -name '*.md' | sed 's|^\./||' ) >>"${WORK}/live.list"
done <"${WORK}/dirs.list"
sort -u "${WORK}/live.list" -o "${WORK}/live.list"
if ! diff -u "${WORK}/snap.list" "${WORK}/live.list" >"${WORK}/setdiff.txt"; then
  cat "${WORK}/setdiff.txt" >&2
  die "快照与真源的**文件集合**不相等：新增/删除的合同文档没有同步进快照（D9 不许减覆盖换绿）"
fi
ok "集合相等（$(grep -c . "${WORK}/live.list") 个文件），没有「删掉落后文件消漂移」的空间"

step "L3 反例：改字节 / 删文件 —— L1 与 L2 必须分别转红"
cp -r "${SNAP}" "${WORK}/snap"
FIRST_REL="$(head -1 "${WORK}/snap.list")"
printf 'x' >>"${WORK}/snap/${FIRST_REL}"
sanitize_live_doc "${LIVE}/${FIRST_REL}" "${WORK}/live.first"
cmp -s "${WORK}/snap/${FIRST_REL}" "${WORK}/live.first" &&
  die "反例1 未被命中：改了一个字节，cmp 仍然认为一致"
rm -f "${WORK}/snap/${FIRST_REL}"
( cd "${WORK}/snap" && find . -type f ! -name SHA256SUMS | sed 's|^\./||' | sort ) >"${WORK}/snap2.list"
diff -q "${WORK}/snap2.list" "${WORK}/live.list" >/dev/null &&
  die "反例2 未被命中：删掉一个文件后集合比对仍然相等"
git -C "${REPO_ROOT}" status --porcelain -- tests/fixtures/contracts >"${WORK}/dirty.txt"
[ ! -s "${WORK}/dirty.txt" ] || { cat "${WORK}/dirty.txt" >&2; die "反例构造污染了真实仓快照"; }
ok "两组反例分别被 L1 / L2 命中；真实仓快照零改动"

printf '\n[PASS] 合同 live 漂移比对通过（L1~L3；共 %d 步 / %d 条断言）\n' "${STEP}" "${PASS}"
