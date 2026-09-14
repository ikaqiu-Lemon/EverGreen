#!/usr/bin/env bash
# 块级安全合并端到端脚本（M6 · T-evergreen.s1_main_flow-158614-073）。
#
# 判据来源：docs/specs/2027-01-24-m6-atomicity-and-strict-check-contract.md
#   §7 / A-57（块级三方「安全合并」：仅当「前像 → 当前值」的差异块与本次要写的块区间不相交时才合并，
#           否则跳过并产 W27 block_merge_conflict；只在块粒度判定，不做行级 diff / patch，不引入 diff 依赖）
#   §8.3（块级冲突复用封闭两值 skipped{kind=file_changed, cause=content_hash_mismatch}，
#         W27 / block locator / 期望 base_block_hash 只落在自由文本诊断，不新造第三个 kind）
#
# 四段断言（与 task Acceptance 判据 9 / M3 零漂移逐条对应）：
#   A. 匹配路径与 M3 逐字一致：base_block_hash == 当前有效块 → 替换该块，退 0，
#      结果与「M3 手工替换该块」的期望文件逐字节相等（frontmatter / 其它分区 / 空行全不动）；
#   B. 安全合并（A-57 新增能力）：用户在自检分区**追加**了一个新块（当前有效块已变），
#      但目标块自读取以来逐字未变 → 用旧目标块的 base_block_hash 仍能**安全合并**替换该目标块，
#      追加块与历史块逐字保留、退 0（这一档在 M3 口径下会因「当前有效块变了」而跳过）；
#   C. 冲突只跳过不覆盖：目标块自读取以来已变化（当期无块命中 base_block_hash）→ 跳过 + 退 3
#      + skipped[].kind=file_changed + 自由文本含 W27 block_merge_conflict + 目标文件字节不变；
#   D. 用户字节零丢失：以上每一档「用户补充」分区逐字保留（B2），任何分支都不丢用户已写入的字节。
#
# 越界反证（本层一律不做）：不新造第三个 skipped kind（block_conflict / block_hash_changed 在
#   internal/ 与报告 JSON 全文恒 0 命中）；不启用退出码 5（退 5 由 T-…-074 单点映射）。
#
# 约束：离线、零交互、可重复；依赖仅 bash / coreutils / git / go / jq；
#   一切写与构建只发生在 mktemp -d 沙箱内，真实仓库工作区一个字节都不碰（末尾自查）；
#   任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/edit/block_merge.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m6-block-merge.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
PLAN="${WORK}/plan.json"

KDIR_REL='domains/ai-infra/knowledge'
CARD="k-20260901-attention"
CARDPATH="${VAULT}/${KDIR_REL}/${CARD}.md"

# 自检分区的单行块（规范化 = 去行尾空白 + 去块尾空行，单行块的规范化即该行本身）。
OLD_BLOCK='- 历史块：为什么需要缩放点积？'
CUR_BLOCK='- 当前有效块：多头注意力的头数如何选？'
NEW_BLOCK='- 升级后的当前有效块：头数与维度如何权衡？'
NEW_BLOCK_B='- 合并写入的目标块：注意力的复杂度上界？'
APPENDED='- 用户后补的新自检块：位置编码要不要学习？'
PHANTOM='- 读取时的原始目标块：一个自那以后已被改写、当期已不在盘上的块。'
USER_LINE='	用户手写、带 Tab 缩进的补充块，B2 要求逐字保留。'

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain; }
sums() { find "${VAULT}" -name '*.md' -type f -exec sha256sum {} + | sort; }
# filehash：与 store.ContentHash 同口径（sha256:<hex>），用于 plan.base 的整文件凭据。
filehash() { printf 'sha256:%s' "$(sha256sum "${VAULT}/$1" | cut -d' ' -f1)"; }
# stamp_gt <新> <旧>：两个带时区 RFC3339 时刻，严格晚于则退 0（与 c2_updated_at_refresh.sh 同源口径）。
stamp_gt() {
  python3 - "$1" "$2" <<'PY_STAMP'
import sys
from datetime import datetime
def p(v):
    return datetime.fromisoformat(v.replace("Z", "+00:00"))
sys.exit(0 if p(sys.argv[1]) > p(sys.argv[2]) else 1)
PY_STAMP
}
# blockhash：与 mdfile.BlockHash 同口径 —— sha256(规范化块文本)[:16]；单行块规范化即该行本身。
blockhash() { printf '%s' "$1" | sha256sum | cut -c1-16; }
# apply：plan JSON 从 stdin 读入，落盘后交 eg apply --json --user-request，回显退出码。
apply() { cat >"${PLAN}"; eg_code apply --plan "${PLAN}" --json --user-request; }
assert_user_line() { grep -Fq -e "${USER_LINE}" "${CARDPATH}" || die "「用户补充」必须逐字保留（$1）"; }

# seed_card <extra-selfcheck-lines...>：把一张五分区卡写进 vault 并提交，工作区回到干净。
# 自检分区固定含 OLD_BLOCK、CUR_BLOCK，额外块由参数逐行追加在 CUR_BLOCK 之后。
seed_card() {
  rm -f "${CARDPATH}"
  mkdir -p "${VAULT}/${KDIR_REL}"
  {
    printf '%s\n' '---' "id: ${CARD}" 'title: 注意力机制' 'status: active' \
      "created_at: '2026-09-01'" "updated_at: '2026-09-01T10:00:00+08:00'" \
      'tags:' '  - 注意力' '---' '' \
      '## 知识内容' '' '正文占位一行。' '' \
      '## 解释与依据' '' '依据占位。' '' \
      '## 条件与边界' '' '仅 S1。' '' \
      '## 用户补充' '' "${USER_LINE}" '' \
      '## 理解自检' '' "${OLD_BLOCK}" '' "${CUR_BLOCK}"
    for extra in "$@"; do printf '%s\n%s\n' "" "${extra}"; done
  } >"${CARDPATH}"
  gitv -c user.email=eg@example.com -c user.name=eg add -A
  gitv -c user.email=eg@example.com -c user.name=eg commit -q -m "seed: m6 block-merge 语料"
  [ -z "$(porcelain)" ] || die "前置：seed 后工作区应干净"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
command -v jq >/dev/null || die "本脚本用 jq 读 .data.report.skipped[]"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

step "eg init 建库"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
ok "库就绪"

# ---------------------------------------------------------------- A. 匹配路径与 M3 逐字一致
step "A 匹配路径：base_block_hash == 当前有效块 → 替换该块，退 0 且与 M3 期望文件逐字节相等"
seed_card
BASE="$(commits)"
BH_CUR="$(blockhash "${CUR_BLOCK}")"
[ "$(printf '%s' "${BH_CUR}" | wc -c | tr -d ' ')" = "16" ] || die "base_block_hash 应为 16 位十六进制"
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"自检问题升级（匹配路径）",
 "base":{"${KDIR_REL}/${CARD}.md":"$(filehash "${KDIR_REL}/${CARD}.md")"},
 "ops":[{"op":"replace_block","target":"${CARD}","section":"理解自检","initiator":"user",
         "reason":"当前有效块升级","base_block_hash":"${BH_CUR}","block":"${NEW_BLOCK}\n"}]}
JSON
)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "匹配路径 replace_block 应退 0，实退 ${C}"; }
# 内容时间戳跟随实际写入刷新（授权合同 §2.1 矩阵第 8 行 / I-…-009）：先独立验证
# updated_at 恰一行且严格变新，再把这个实得值放进期望文件 —— 其余字节仍逐字节比对。
[ "$(grep -c '^updated_at:' "${CARDPATH}")" = "1" ] || die "updated_at 必须恰一行（整行覆盖）"
NOW_AT="$(sed -n 's/^updated_at: *//p' "${CARDPATH}" | tr -d "'\"")"
stamp_gt "${NOW_AT}" '2026-09-01T10:00:00+08:00' ||
  die "块替换是一次实际写入，updated_at 必须刷新（旧 2026-09-01T10:00:00+08:00 → 实得 ${NOW_AT}）"
# 构造 M3 期望文件：seed 原文里把 CUR_BLOCK 那一行换成 NEW_BLOCK，updated_at 换成本次
# 写入时刻，其余逐字不动。
{
  printf '%s\n' '---' "id: ${CARD}" 'title: 注意力机制' 'status: active' \
    "created_at: '2026-09-01'" "updated_at: '${NOW_AT}'" \
    'tags:' '  - 注意力' '---' '' \
    '## 知识内容' '' '正文占位一行。' '' \
    '## 解释与依据' '' '依据占位。' '' \
    '## 条件与边界' '' '仅 S1。' '' \
    '## 用户补充' '' "${USER_LINE}" '' \
    '## 理解自检' '' "${OLD_BLOCK}" '' "${NEW_BLOCK}"
} >"${WORK}/expected.md"
diff -u "${WORK}/expected.md" "${CARDPATH}" >"${WORK}/diff.txt" ||
  { cat "${WORK}/diff.txt"; die "匹配路径输出与 M3 期望文件不逐字节相等（发生了 M3 行为漂移）"; }
grep -Fq -- "${OLD_BLOCK}" "${CARDPATH}" || die "历史块必须逐字保留（append-only）"
assert_user_line "A"
[ "$(commits)" = "$((BASE + 1))" ] || die "一次成功块替换应恰产生 1 个 commit"
ok "匹配路径：退 0 + 与 M3 期望逐字节相等 + 历史块 / 用户补充逐字保留 + 恰 1 commit"

# ---------------------------------------------------------------- B. 安全合并（A-57 新增能力）
step "B 安全合并：读取后用户追加了新块（整文件已变），但目标块逐字未变 → 用读取时凭据仍安全合并"
seed_card                                  # 自检分区 = OLD_BLOCK / CUR_BLOCK，当前有效块 = CUR_BLOCK
BASE="$(commits)"
READ_FILEHASH="$(filehash "${KDIR_REL}/${CARD}.md")"   # eg「读取时」的整文件凭据
BH_TARGET="$(blockhash "${CUR_BLOCK}")"    # 目标块 = CUR_BLOCK，读取后逐字未变
# 用户随后在自检分区追加一个新块并提交：整文件 content_hash 已变（M3 口径下会因此整文件跳过）。
printf '\n%s\n' "${APPENDED}" >>"${CARDPATH}"
gitv -c user.email=eg@example.com -c user.name=eg commit -aqm "user appends a new self-check block"
[ -z "$(porcelain)" ] || die "前置：追加块后工作区应干净"
[ "${READ_FILEHASH}" != "$(filehash "${KDIR_REL}/${CARD}.md")" ] ||
  die "前置：追加后整文件 hash 应已改变（否则不构成 A-57 的差异场景）"
# 用**读取时**的整文件凭据 + 目标块凭据发起：整文件已变，但目标块 CUR_BLOCK 逐字未变 → 安全合并。
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"整文件已变但目标块未变，安全合并",
 "base":{"${KDIR_REL}/${CARD}.md":"${READ_FILEHASH}"},
 "ops":[{"op":"replace_block","target":"${CARD}","section":"理解自检","initiator":"user",
         "reason":"安全合并替换目标块","base_block_hash":"${BH_TARGET}","block":"${NEW_BLOCK_B}\n"}]}
JSON
)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "安全合并应退 0（M3 口径本会整文件跳过），实退 ${C}"; }
grep -Fq -- "${NEW_BLOCK_B}" "${CARDPATH}" || die "目标块应被安全合并替换为新块"
if grep -Fq -- "${CUR_BLOCK}" "${CARDPATH}"; then die "目标块替换后旧目标块不应残留"; fi
grep -Fq -- "${APPENDED}" "${CARDPATH}" || die "用户后补的新自检块必须逐字保留（用户字节零丢失）"
grep -Fq -- "${OLD_BLOCK}" "${CARDPATH}" || die "历史块必须逐字保留"
assert_user_line "B"
[ "$(commits)" = "$((BASE + 2))" ] || die "seed 后追加(+1) 与安全合并(+1) 共 +2 个 commit，实得 $(commits)（BASE=${BASE}）"
ok "安全合并：退 0 + 目标块被替换 + 追加块 / 历史块 / 用户补充逐字保留（整文件已变仍不误跳过）"

# ---------------------------------------------------------------- C. 冲突只跳过不覆盖 + W27
step "C 冲突：目标块自读取以来已变化（当期无块命中 base_block_hash）→ 跳过 + 退 3 + W27 + 目标字节不变"
seed_card
BASE="$(commits)"
BH_STALE="$(blockhash "${PHANTOM}")"   # 读取时的原始块 hash；当期盘上无任何块与之逐字相同
SUM_BEFORE="$(sums)"
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"用过期块 hash 复写目标块",
 "base":{"${KDIR_REL}/${CARD}.md":"$(filehash "${KDIR_REL}/${CARD}.md")"},
 "ops":[{"op":"replace_block","target":"${CARD}","section":"理解自检","initiator":"user",
         "reason":"用陈旧 base_block_hash 复写","base_block_hash":"${BH_STALE}","block":"- 冲突时这段字节不许落盘。\n"}]}
JSON
)"
[ "${C}" = "3" ] || { cat "${WORK}/out.txt"; die "块级冲突应退 3（跳过），实退 ${C}"; }
jq -e '[.data.report.skipped[].kind] | index("file_changed") != null' "${WORK}/out.txt" >/dev/null ||
  { cat "${WORK}/out.txt"; die "块级冲突必须复用封闭两值 kind=file_changed（不新造第三值）"; }
grep -Fq 'W27' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "冲突自由文本必须含诊断码 W27"; }
grep -Fq 'block_merge_conflict' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "冲突自由文本必须含 block_merge_conflict 子因"; }
grep -Fq "${BH_STALE}" "${WORK}/out.txt" || die "冲突诊断应带期望的 base_block_hash"
[ "${SUM_BEFORE}" = "$(sums)" ] || die "块级冲突必须字节不变（只跳过不覆盖）"
[ "$(commits)" = "${BASE}" ] || { echo "commit 从 ${BASE} 变到 $(commits)"; die "块级冲突必须零 commit"; }
if grep -Fq '冲突时这段字节不许落盘。' "${CARDPATH}"; then die "冲突时新块一个字节都不许落盘"; fi
assert_user_line "C"
ok "块级冲突：退 3 + kind=file_changed + W27 block_merge_conflict + 目标字节不变 + 零 commit"

# ---------------------------------------------------------------- D. 封闭 kind 静态反证
step "D 封闭两值：internal/ 与本次报告 JSON 全文无 block_conflict / block_hash_changed 第三值"
N="$( { grep -rn 'block_conflict\|block_hash_changed' "${REPO_ROOT}/internal/" || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/ 出现被禁的第三 kind 字面量（${N} 处）"
if grep -Fq 'block_conflict' "${WORK}/out.txt"; then die "报告 JSON 不该出现 block_conflict 字面量"; fi
ok "封闭两值守住：无 block_conflict / block_hash_changed 命中"

# ---------------------------------------------------------------- E. 真实仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] block_merge.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
