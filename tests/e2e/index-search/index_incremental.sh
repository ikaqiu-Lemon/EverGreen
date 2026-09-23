#!/usr/bin/env bash
# `eg index sync` 增量收敛端到端脚本（M5 · T-evergreen.s1_main_flow-158614-066 · 阶段 B）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §2（Markdown 是唯一权威来源，`.index/` 只是可删除、可重建的派生物）
#   §5.1（水位线 `(head, files_hash)`；`(size, mtime)` 只作快路径，不作一致性依据）
#   §5.2（三向 diff：新增 / 修改 / 删除；重命名 = 删除 + 新增）
#   §5.3（**增量结果必须与整库重建等价**；退化必须留痕，不许静默）
#   §6.1（索引问题不改变退出码；陈旧只报不阻断读）
#   §9（W22 stale / W23 missing / W24 corrupt）
# 以及 milestone M-005 完成判据「索引可完整重建、Markdown 恒为唯一权威来源」。
#
# 本脚本只守**增量收敛的正确性**（构建面走 m5_index_build.sh、损坏面走 m5_index_corrupt_rebuild.sh、
# 写命令写后同步走 m5_index_consistency.sh，四本互不覆盖）：
#   ① 四种权威变更各自被三向 diff **精确认出**（计数逐字相等，不是「大于 0 就算过」）：
#      新增一张卡 / 改一张卡 / 删一张卡 / 重命名一张卡（= 删除 + 新增，卡总数不变）；
#   ② 每一次 `eg index sync` 之后，`(head, files_hash, cards, relations)` 四键与
#      **紧随其后的整库 `eg index rebuild`** 逐字相同 —— 这就是「增量 == 全量重建」的硬判据；
#   ③ 幂等 + 零写入：变更收敛完再 `sync` → `action=noop`，且 `eg.db` 的
#      size + mtime + sha256 **逐字不变**（零写入就不许造 0 计数）；
#   ④ 终局可重建：`rm -rf .index/` 后一条 `eg index build` 复原，四键与最后一次 `sync` 逐字相同；
#   ⑤ 陈旧只报不阻断：陈旧态 `status` 恒退 `0`、`blocks_read=false`、`W22` 在场；
#      `eg search` / `eg card show` / `eg rel` 照常退 `0` 且输出里**没有** `data.index`。
#      **T-…-067 重钉（只加严，原判据一条未删）**：读路径已接入索引，因此陈旧态下三条读命令
#      除「退 0 + 零 data.index」之外，还必须各带**恰一条** `W22` + **恰一条** `Q5`
#      （合同 §5.2 / §6.3：陈旧一律回落全量扫描且必须留痕）。065 / 066 时期这里断言「零 Q5」，
#      依据是「读路径尚未接索引」——那条依据在 T-…-067 之后不再成立，故按事实重钉；
#   ⑥ 边界：`eg index` 全程 **commit 恒 0 次**、权威 `.md` 的 sha256 清单只在脚本自己的
#      外部编辑那几步变化、`.index/` 不进 Git、`eg index` 自己的输出里恒无 `Q5`
#      （Q5 属**查询域**：只有读路径降级才产它，索引命令一条都不许产）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；无 jq / sqlite3 等外部依赖
#   （只用 bash / coreutils / awk / git / go）；**一切写与构建只发生在 mktemp -d 沙箱内**，
#   真实仓库工作区一个字节都不碰（脚本末尾自查 git status）；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/index_incremental.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-index-incr.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

IDX_REL='.index'
DB_REL="${IDX_REL}/eg.db"
KDIR_REL='domains/ai-infra/knowledge'

CARD_A='k-20261201-attention'
CARD_B='k-20261201-rnn'
CARD_C='k-20261201-ops'
CARD_D='k-20261201-batching'
CARD_D2='k-20261201-batching-v2'

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
gitv() { git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg "$@"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain | sort; }

# 取值一律经结构化解析进入 data.index：该对象含嵌套 blocks 状态，不能按首个 `}` 截断。
jvalue() {
  python3 - "$1" "$2" <<'PY' ||
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    value = json.load(handle)["data"]["index"][sys.argv[2]]
if isinstance(value, bool):
    print(str(value).lower())
else:
    print(value)
PY
  { cat "$1"; die "取不到 data.index.$2"; }
}
jnum() { jvalue "$1" "$2"; }
jstr() { jvalue "$1" "$2"; }

# keys4 <信封文件> <head 键> <files_hash 键>：把「等价」压成一行可 diff 的四键指纹。
keys4() {
  printf '%s|%s|%s|%s\n' "$(jstr "$1" "$2")" "$(jstr "$1" "$3")" \
    "$(jnum "$1" cards)" "$(jnum "$1" relations)"
}
sync_keys()  { keys4 "$1" after_head after_files_hash; }
build_keys() { keys4 "$1" head files_hash; }

authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals -type f 2>/dev/null || true; } | sort |
    xargs -r sha256sum )
}
db_fingerprint() { ( cd "${VAULT}" && eg_stat_size_mtime "${DB_REL}" && sha256sum "${DB_REL}" ); }

# no_q5 <文件> <说明>：`Q5` 属**查询域**（读路径降级留痕），`eg index` 自己一条都不许产。
# T-…-067 之后仍然成立：接入索引的是 `internal/query`，索引命令的诊断面一格未动。
no_q5() {
  grep -Fq '"Q5"' "$1" && { cat "$1"; die "$2：eg index 的输出里不得出现 Q5（Q5 属查询域）"; }
  return 0
}

# ccount <文件> <码>：数某个诊断码在信封里出现的条数（用于「恰一条」这类等号断言）。
ccount() {
  grep -o '"code":"[^"]*"' "$1" | sed 's/"code":"//; s/"$//' | grep -cx "$2" || true
}
# changes_are <文件> <added> <modified> <removed>：三向 diff 计数**逐字相等**。
changes_are() {
  local f="$1" a="$2" m="$3" r="$4"
  [ "$(jnum "${f}" changed_added)" = "${a}" ] ||
    die "changed_added = $(jnum "${f}" changed_added)，期望 ${a}"
  [ "$(jnum "${f}" changed_modified)" = "${m}" ] ||
    die "changed_modified = $(jnum "${f}" changed_modified)，期望 ${m}"
  [ "$(jnum "${f}" changed_removed)" = "${r}" ] ||
    die "changed_removed = $(jnum "${f}" changed_removed)，期望 ${r}"
}
# seed_card <id> <title> <status> <正文>：**绕过 CLI** 直接写权威文件（模拟外部编辑 / 手工整理）。
seed_card() {
  mkdir -p "${VAULT}/${KDIR_REL}"
  {
    printf '%s\n' '---' "id: $1" "status: $3" "created_at: '2026-12-01'" \
      "updated_at: '2026-12-01T10:00:00+08:00'" "title: $2" 'sources: []' '---' \
      '' '## 知识内容' '' "$4" ''
  } >"${VAULT}/${KDIR_REL}/$1.md"
}
# commit_authority <消息>：把外部编辑落成一次真 commit（让 head 也真实前进）。
commit_authority() { gitv add -A && gitv commit -q -m "$1"; }

# converge_and_prove <说明> <期望 cards> <期望 added> <期望 modified> <期望 removed>
#   ① 变更已在磁盘上 → status 必须判陈旧（W22）且三向 diff 计数逐字相等；
#   ② `eg index sync` 收敛 → action=synced、非退化、卡数如实；
#   ③ 紧接着整库 `eg index rebuild` → 四键与增量结果**逐字相同**（增量 == 全量重建）。
converge_and_prove() {
  local what="$1" want_cards="$2" a="$3" m="$4" r="$5"
  [ "$(eg_code index status --json)" = "0" ] || die "${what}：陈旧态 status 仍必须退 0"
  cp "${WORK}/out.txt" "${WORK}/st.json"
  [ "$(jstr "${WORK}/st.json" freshness)" = "stale" ] ||
    die "${what}：权威变了索引没变，freshness 应为 stale（实为 $(jstr "${WORK}/st.json" freshness)）"
  grep -Fq '"W22"' "${WORK}/st.json" || die "${what}：陈旧必须产 W22"
  grep -Fq '"blocks_read":false' "${WORK}/st.json" || die "${what}：陈旧只报不阻断读"
  changes_are "${WORK}/st.json" "${a}" "${m}" "${r}"
  no_q5 "${WORK}/st.json" "${what} 的 status"

  [ "$(eg_code index sync --json)" = "0" ] || { cat "${WORK}/err.txt"; die "${what}：sync 应退 0"; }
  cp "${WORK}/out.txt" "${WORK}/sync.json"
  [ "$(jstr "${WORK}/sync.json" action)" = "synced" ] ||
    die "${what}：healthy 索引 + 真变更时 action 应为 synced（实为 $(jstr "${WORK}/sync.json" action)）"
  grep -Fq '"degraded":false' "${WORK}/sync.json" || die "${what}：不该走退化分支"
  [ "$(jnum "${WORK}/sync.json" cards)" = "${want_cards}" ] ||
    die "${what}：收敛后 cards = $(jnum "${WORK}/sync.json" cards)，期望 ${want_cards}"
  changes_are "${WORK}/sync.json" "${a}" "${m}" "${r}"
  no_q5 "${WORK}/sync.json" "${what} 的 sync"

  [ "$(eg_code index status --json)" = "0" ] || die "${what}：收敛后 status 应退 0"
  [ "$(jstr "${WORK}/out.txt" freshness)" = "fresh" ] || die "${what}：收敛后必须 fresh"

  # 硬判据：同一份权威上，增量收敛的结果与整库重建**逐字相同**。
  [ "$(eg_code index rebuild --json)" = "0" ] || die "${what}：对照重建应退 0"
  cp "${WORK}/out.txt" "${WORK}/rb.json"
  local ik rk
  ik="$(sync_keys "${WORK}/sync.json")"
  rk="$(build_keys "${WORK}/rb.json")"
  [ "${ik}" = "${rk}" ] ||
    die "${what}：增量与整库重建不等价（增量 ${ik} vs 重建 ${rk}）—— 合同 §5.3 的底线"
  ok "${what}：diff = +${a}/~${m}/-${r}、cards=${want_cards}、增量 == 全量重建（${ik}）"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 二进制 + 语料
step "CGO_ENABLED=0 构建二进制，并 seed 三张卡 + 一条真实关系"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
seed_card "${CARD_A}" "注意力机制的计算代价" active "正文占位：注意力。"
seed_card "${CARD_B}" "RNN 的长序列衰减" deprecated "正文占位：RNN。"
seed_card "${CARD_C}" "运维值班注意力分配" active "正文占位：运维。"
commit_authority "seed: m5 增量索引语料"
[ "$(eg_code rel add "${CARD_A}" supports "${CARD_B}" --reason 端到端语料)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg rel add 失败"; }
[ "$(porcelain)" = "" ] || { porcelain; die "前置失败：工作区应干净"; }
ok "二进制就绪（$(eg --version | head -1)）；3 张卡 + 1 条 supports 入库"

# ---------------------------------------------------------------- 2. 基线全量构建
step "eg index build 基线：action=built、cards=3、relations=1、fresh"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "基线 build 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/base.json"
[ "$(jstr "${WORK}/base.json" action)" = "built" ] || die "基线 action 应为 built"
[ "$(jnum "${WORK}/base.json" cards)" = "3" ] || die "基线 cards 应为 3"
[ "$(jnum "${WORK}/base.json" relations)" = "1" ] || die "基线 relations 应为 1"
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
[ "$(jstr "${WORK}/out.txt" freshness)" = "fresh" ] || die "刚建完必须 fresh"
INDEX_COMMITS="$(commits)"
authority_sha >"${WORK}/authority.base.txt"
ok "基线四键 = $(build_keys "${WORK}/base.json")"

# ---------------------------------------------------------------- 3. 新增
step "新增一张卡（外部写入 + commit）：diff = +1/~0/-0，增量 == 全量重建"
seed_card "${CARD_D}" "推理批处理的吞吐权衡" active "正文占位：批处理与吞吐。"
commit_authority "外部新增一张卡"
converge_and_prove "新增" 4 1 0 0

# ---------------------------------------------------------------- 4. 修改
step "改一张卡的正文（外部写入 + commit）：diff = +0/~1/-0，增量 == 全量重建"
printf '%s\n' '' '追加正文：批处理会放大尾延迟。' >>"${VAULT}/${KDIR_REL}/${CARD_D}.md"
commit_authority "外部修改一张卡"
converge_and_prove "修改" 4 0 1 0

# ---------------------------------------------------------------- 5. 删除
step "删掉一张卡文件（外部 rm + commit）：diff = +0/~0/-1，增量 == 全量重建"
rm -f "${VAULT}/${KDIR_REL}/${CARD_C}.md"
commit_authority "外部删除一张卡"
converge_and_prove "删除" 3 0 0 1

# ---------------------------------------------------------------- 6. 重命名 = 删除 + 新增
step "重命名一张卡（换 id 与文件名 + commit）：diff = +1/~0/-1，卡总数不变"
seed_card "${CARD_D2}" "推理批处理的吞吐权衡" active "正文占位：批处理与吞吐。"
rm -f "${VAULT}/${KDIR_REL}/${CARD_D}.md"
commit_authority "外部重命名一张卡"
converge_and_prove "重命名" 3 1 0 1

# ---------------------------------------------------------------- 7. 幂等 + 零写入
step "收敛完再 sync：action=noop，且 eg.db 的 size + mtime + sha256 逐字不变"
db_fingerprint >"${WORK}/db.before.txt"
sleep 1   # 让 mtime 有变化的机会：这一秒是为了让「零写入」这条断言真的有分辨力
[ "$(eg_code index sync --json)" = "0" ] || die "无变更时 sync 应退 0"
cp "${WORK}/out.txt" "${WORK}/noop.json"
[ "$(jstr "${WORK}/noop.json" action)" = "noop" ] ||
  die "无变更时 action 应为 noop（实为 $(jstr "${WORK}/noop.json" action)）"
changes_are "${WORK}/noop.json" 0 0 0
db_fingerprint >"${WORK}/db.after.txt"
diff -u "${WORK}/db.before.txt" "${WORK}/db.after.txt" >/dev/null ||
  die "no-op 的 sync 动了 eg.db：零写入就是零写入"
[ "$(eg_code index sync --json)" = "0" ] || die "再跑一次 sync 仍应退 0"
[ "$(jstr "${WORK}/out.txt" action)" = "noop" ] || die "sync 必须幂等（第二次仍是 noop）"
ok "sync 幂等：连续两次 noop，eg.db 指纹逐字不变"

# ---------------------------------------------------------------- 8. 终局可重建
step "rm -rf .index/ 后一条 eg index build 复原：四键与最后一次 sync 逐字相同"
[ "$(eg_code index status --strict --json)" = "0" ] || die "strict status 应退 0"
cp "${WORK}/out.txt" "${WORK}/strict.json"
[ "$(jstr "${WORK}/strict.json" freshness)" = "fresh" ] ||
  die "全量重算 content_hash 之后仍应 fresh（快路径没说假话）"
grep -Fq '"strict":true' "${WORK}/strict.json" || die "--strict 必须如实留痕"
FINAL_KEYS="$(printf '%s|%s|%s|%s\n' "$(jstr "${WORK}/strict.json" actual_head)" \
  "$(jstr "${WORK}/strict.json" files_hash)" "$(jnum "${WORK}/strict.json" card_count)" 1)"
rm -rf "${VAULT}/${IDX_REL}"
[ "$(eg_code index status --json)" = "0" ] || die "删掉之后 status 应退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "missing" ] || die "删掉之后 health 应为 missing"
grep -Fq '"W23"' "${WORK}/out.txt" || die "缺 W23（索引缺失）"
[ "$(eg_code index build --json)" = "0" ] || die "复原 build 应退 0"
cp "${WORK}/out.txt" "${WORK}/final.json"
REBUILT_KEYS="$(printf '%s|%s|%s|%s\n' "$(jstr "${WORK}/final.json" head)" \
  "$(jstr "${WORK}/final.json" files_hash)" "$(jnum "${WORK}/final.json" cards)" \
  "$(jnum "${WORK}/final.json" relations)")"
[ "${FINAL_KEYS}" = "${REBUILT_KEYS}" ] ||
  die "增量收敛出来的库与全新构建不等价（增量 ${FINAL_KEYS} vs 全新 ${REBUILT_KEYS}）"
ok "整目录删掉后一条命令复原，四键逐字相同：${REBUILT_KEYS}"

# ---------------------------------------------------------------- 9. 陈旧不阻断读 + 读路径未接索引
step "陈旧只报不阻断：读命令照常退 0、零 data.index，且各恰一条 W22 + Q5（T-…-067 重钉）"
printf '%s\n' '' '再一次外部编辑：制造陈旧。' >>"${VAULT}/${KDIR_REL}/${CARD_A}.md"
[ "$(eg_code index status --json)" = "0" ] || die "陈旧态 status 恒退 0"
[ "$(jstr "${WORK}/out.txt" freshness)" = "stale" ] || die "外部编辑后应判陈旧"
grep -Fq '"use_index":false' "${WORK}/out.txt" ||
  die "陈旧时 use_index 应为 false（陈旧的索引不可信，读侧一律回落扫描）"
for c in "search 注意力" "card show ${CARD_A}" "rel ${CARD_A}"; do
  # shellcheck disable=SC2086
  [ "$(eg_code ${c} --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "索引陈旧不许拖垮读命令：eg ${c} 应退 0"; }
  # 原判据保留：data 键集合不扩张，索引事实绝不进 data（合同 §6.4）。
  grep -Fq '"data":{"index":' "${WORK}/out.txt" &&
    die "eg ${c} 的输出里出现了 data.index：索引事实只能经诊断承载（合同 §6.4）"
  # T-…-067 新增判据：降级必须留痕，且恰一条 W22 + 恰一条 Q5（不多不少）。
  [ "$(ccount "${WORK}/out.txt" W22)" = "1" ] ||
    { cat "${WORK}/out.txt"; die "eg ${c}：陈旧态应恰一条 W22"; }
  [ "$(ccount "${WORK}/out.txt" Q5)" = "1" ] ||
    { cat "${WORK}/out.txt"; die "eg ${c}：陈旧态降级应恰一条 Q5（有原因码必有 Q5）"; }
  for other in W23 W24; do
    [ "$(ccount "${WORK}/out.txt" "${other}")" = "0" ] ||
      die "eg ${c}：陈旧态归因不唯一，出现了 ${other}"
  done
done
gitv checkout -- "${KDIR_REL}/${CARD_A}.md"
[ "$(eg_code index sync --json)" = "0" ] || die "还原后 sync 应退 0"
ok "陈旧态：status 退 0 / use_index=false；三条读命令退 0、零 data.index、各恰一条 W22 + Q5"

# ---------------------------------------------------------------- 10. 边界总账
step "边界总账：eg index 恒 0 次 commit、权威只被脚本自己的外部编辑改过、.index/ 不进 Git"
# 第 2 步之后本脚本只做了「外部编辑 + 自己 commit」，eg index 自己一次都没提交：
# 因此 commit 数的增量恰等于脚本自己发起的 4 次 commit（新增 / 修改 / 删除 / 重命名）。
[ "$(commits)" = "$((INDEX_COMMITS + 4))" ] ||
  die "commit 数 = $(commits)，期望 $((INDEX_COMMITS + 4))（eg index 恒 0 次提交）"
grep -Fq '"commit":null' "${WORK}/final.json" || die "index 报告里 git.commit 必须是 null"
[ "$(porcelain)" = "" ] || { porcelain; die "${IDX_REL}/ 污染了工作区（应被整目录忽略）"; }
gitv check-ignore -q "${DB_REL}" || die ".gitignore 未忽略 ${IDX_REL}/"
# 权威文件的现态 = 基线 - 删掉的 C - 改名前的 D + 改名后的 D2（其余逐字不变）。
authority_sha >"${WORK}/authority.now.txt"
diff -u "${WORK}/authority.base.txt" "${WORK}/authority.now.txt" >"${WORK}/auth.diff" || true
UNEXPECTED="$( { grep -E '^[+-][0-9a-f]{64} ' "${WORK}/auth.diff" || true; } |
  { grep -vE "(${CARD_C}|${CARD_D}|${CARD_D2})\.md" || true; } | wc -l | tr -d ' ')"
[ "${UNEXPECTED}" = "0" ] ||
  { cat "${WORK}/auth.diff"; die "除脚本自己编辑的三张卡外，权威文件被动了 ${UNEXPECTED} 处"; }
ok "eg index 零提交、工作区干净、权威改动恰好只有脚本自己那三张卡"

# ---------------------------------------------------------------- 11. 仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] index_incremental.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
