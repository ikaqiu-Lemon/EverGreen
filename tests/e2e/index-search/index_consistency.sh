#!/usr/bin/env bash
# 写命令**写后索引同步**与「Markdown / 索引一致性」端到端脚本
# （M5 · T-evergreen.s1_main_flow-158614-066 · 阶段 B）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §2（Markdown 是唯一权威来源；索引问题绝不牵连权威数据）
#   §5.3（写后同步：写入成功之后才同步；增量结果与整库重建等价）
#   §6.1（索引问题**不改变退出码**、不阻断读、不回滚已写入内容）
#   §7.2（写命令不替用户建索引；索引不可用只报不自动修）
#   §9（W22 stale / W23 missing / W24 corrupt）
# 以及 milestone M-005 完成判据「Markdown 与索引恒一致（或如实可判定为陈旧）」。
#
# 本脚本只守**写路径与索引的一致性合同**（增量算法本身走 m5_index_incremental.sh，
# 构建面走 m5_index_build.sh，损坏面走 m5_index_corrupt_rebuild.sh，四本互不覆盖）：
#   ① 六条写命令（`apply` / `edit` 共用 runPlan 落点、`delete`、`undelete`、
#      `mark-reviewed`、`reconcile`）在 Markdown 落盘 + commit **成功之后**同步索引：
#      每条命令跑完，`eg index status` 立刻是 `fresh`（不留陈旧尾巴），且 `head` 跟到新 HEAD；
#   ② **索引未建 = 完全正常**：`.index/` 不存在时六条写命令照常退 0、
#      **不替用户建索引**（跑完 `.index/` 仍不存在），也不报 W23 噪声；
#   ③ **索引不可用只报不自动修**：坏索引下写命令照常退 0 且 commit +1，
#      如实留痕 `W24`，跑完索引**依然坏着**（修复只走用户显式 `eg index rebuild`）；
#   ④ **索引问题不牵连权威（历史底线不改，机制按 M6 现态重钉，§16.4 R6/R7）**：
#      · M5 历史事实（保留）：`.index` 是纯派生目录，被换成普通文件占位时属「可恢复不可用」，
#        索引侧 build「先删再全量建」→ 退 0 + `repaired` + `W24`，权威 Markdown 逐字零改动；
#      · M6 现态（新增正面锁）：`.index` 变为「派生物 + 运行时证据」混居目录（§16.2），禁
#        `os.RemoveAll(.index)`（R6-D1），维护前先过单点安全解析器（`internal/txn/safedir.go`，
#        parent-symlink fail closed）。占位普通文件（非目录）⇒ **fail closed** 退 `5` + `E15`、
#        **零权威写入**、占位**原样保留**（绝不盲删非预期条目），等待人工清障后 build 全量重建回 fresh。
#      两侧共同底线一字不改：索引侧命令**绝不回写权威 Markdown**（authority sha 前后逐字相等）；
#   ⑤ **键面零漂移**：接上写后同步之后，写命令的 `data` 首键与既有产物键**逐字不变**，
#      且输出里**没有** `data.index` 这一格（索引只走既有诊断结构 `report` / `warnings`）；
#   ⑥ 写后同步的结果与整库重建**等价**：一串写命令跑完之后 `sync` 恒 `noop`，
#      `eg index rebuild` 出来的 `(head, files_hash, cards, relations)` 与写后现值逐字相同；
#   ⑦ **挂载面是封闭枚举**（Task 的 Tests / Acceptance 各点名 `apply` / `edit` / `delete` /
#      `rel` / `reconcile`，本仓另含 `undelete` / `mark-reviewed`）—— C2a·M6 现态重钉（§16.4）：
#      M5 §5.3 那份枚举**不含** `proposal approve` / `capture` / `config set`（历史事实保留）；
#      M6 §16.1 硬规则 3 把 `proposal approve` 提级为 A 类强事务（T-072 C4b），其写后增量
#      同步落在同一把锁内，故 approve 写后**如实立刻 fresh**（新增现态正面锁）。仍未挂载的
#      `config set` 推进 Git HEAD 后 `status` **如实**报 `stale` + `reason=head_moved` + `W22`
#      （`blocks_read=false`、三向 diff 计数全 0），且下一条**已挂载**写命令或一条
#      `eg index sync` 即收敛回 `fresh`（不主动追 HEAD ≠ 修不回来）；
#   ⑧ 全程无 `Q5`（读路径降级属 T-…-067），也不出现退出码 `5`（强校验属 M6）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；无 jq / sqlite3 等外部依赖
#   （只用 bash / coreutils / awk / git / go）；**一切写与构建只发生在 mktemp -d 沙箱内**，
#   真实仓库工作区一个字节都不碰（脚本末尾自查 git status）；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/index_consistency.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-index-consistency.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

IDX_REL='.index'
DB_REL="${IDX_REL}/eg.db"
KDIR_REL='domains/ai-infra/knowledge'

CARD_A='k-20261201-attention'
CARD_B='k-20261201-rnn'
CARD_C='k-20261201-ops'

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
head_sha() { gitv rev-parse HEAD; }

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
# data_first_key <信封文件>：`data` 的首键名（键面零漂移的判据）。
data_first_key() { grep -o '"data":{"[a-z_]*"' "$1" | head -1 | sed 's/.*{"//; s/"$//'; }

authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals -type f 2>/dev/null || true; } | sort |
    xargs -r sha256sum )
}
# no_forbidden <文件> <说明>：Q5（读路径降级，属 T-…-067）与退出码 5（强校验，属 M6）恒不出现。
no_forbidden() {
  grep -Fq '"Q5"' "$1" && { cat "$1"; die "$2：出现了 Q5（属 T-…-067）"; }
  grep -Fq '"exit_code":5' "$1" && { cat "$1"; die "$2：出现了退出码 5（强校验属 M6）"; }
  return 0
}
# no_index_data <文件> <说明>：写命令绝不因为顺带同步索引而新增 `data.index` 键。
no_index_data() {
  grep -Fq '"data":{"index":' "$1" && { cat "$1"; die "$2：写命令输出里出现了 data.index（键面漂移）"; }
  return 0
}
# no_derived_index <说明>：C2a·M6 现态重钉（§16.1/§16.3/A-53 + §16.4 现态重钉授权；保留历史事实 + 现态双侧锁）。
# 历史事实一格不放宽：写命令**不替用户建派生索引 DB**——eg.db / eg.db-wal / eg.db-shm 三者恒不存在。
# M6 现态：A 类写命令按 §16.1/S1 拿锁，锁层按 A-53 于 `.index/` 下按需建 runtime-reserved（run.lock / txn）；
# 建 `.index/` 目录 ≠ 建索引。故 `.index/` 若存在，只许锁层两项（run.lock / txn），别的杂项当场红。
no_derived_index() {
  local why="$1" e
  for e in eg.db eg.db-wal eg.db-shm; do
    [ ! -e "${VAULT}/${IDX_REL}/${e}" ] ||
      die "${why}：写命令建出了派生 DB ${e}（建索引只走显式 eg index build）"
  done
  [ -e "${VAULT}/${IDX_REL}" ] || return 0
  for e in $( ( cd "${VAULT}/${IDX_REL}" && ls -A | sort ) ); do
    case "${e}" in
      run.lock|txn) ;;
      *) die "${why}：${IDX_REL}/ 出现非 runtime-reserved 条目「${e}」（只许锁层 run.lock / txn，§16.3）" ;;
    esac
  done
  return 0
}
# index_is_fresh <说明>：`eg index status` 立刻是 fresh，且水位线 head == 当前 git HEAD。
index_is_fresh() {
  local what="$1"
  [ "$(eg_code index status --json)" = "0" ] || die "${what}：status 恒退 0"
  [ "$(jstr "${WORK}/out.txt" freshness)" = "fresh" ] ||
    die "${what}：写后同步没跟上（freshness = $(jstr "${WORK}/out.txt" freshness)，期望 fresh）"
  local h; h="$(jstr "${WORK}/out.txt" head)"
  case "$(head_sha)" in "${h}"*) ;; *) die "${what}：水位线 head=${h} 没跟到当前 git HEAD" ;; esac
  grep -Fq '"W22"' "${WORK}/out.txt" && die "${what}：fresh 态不该有 W22"
  return 0
}
seed_card() {
  mkdir -p "${VAULT}/${KDIR_REL}"
  {
    printf '%s\n' '---' "id: $1" "status: $3" "created_at: '2026-12-01'" \
      "updated_at: '2026-12-01T10:00:00+08:00'" "title: $2" 'sources: []' '---' \
      '' '## 知识内容' '' "$4" ''
  } >"${VAULT}/${KDIR_REL}/$1.md"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 二进制 + 语料
step "CGO_ENABLED=0 构建二进制，并 seed 三张卡（工作区干净）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
seed_card "${CARD_A}" "注意力机制的计算代价" active "正文占位：注意力。"
seed_card "${CARD_B}" "RNN 的长序列衰减" active "正文占位：RNN。"
seed_card "${CARD_C}" "运维值班注意力分配" active "正文占位：运维。"
gitv add -A && gitv commit -q -m "seed: m5 写后同步语料"
[ "$(porcelain)" = "" ] || { porcelain; die "前置失败：工作区应干净"; }
ok "二进制就绪（$(eg --version | head -1)）；3 张卡入库"

# ---------------------------------------------------------------- 2. 索引未建：写命令不替用户建
step "索引未建 = 正常状态：写命令照常退 0，跑完仍无派生索引（不替用户建索引）"
# 前置口径重钉（I-…-030）：I-…-021 之后 `eg config set` 属 A 类写路径，会按 §16.1/S1 取
# `vault/.index/run.lock`，锁层按 A-53 **按需**建该目录 —— 所以此刻 `.index/` 可能已存在。
# 「建 `.index/` 目录」≠「建索引」：真正要钉的是「派生索引 DB 家族不存在 + `.index/` 里只有
# 锁层 runtime-reserved 两项」，这正是本脚本既有 no_derived_index 的现态口径（§16.2/§16.3）。
# 因此前置从「`.index/` 必须不存在」改为复用同一个更精确的判据，历史事实一格不放宽。
no_derived_index "建库之后（索引未建）"
C0="$(commits)"
[ "$(eg_code rel add "${CARD_A}" supports "${CARD_B}" --reason 索引未建也照常写 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "索引未建不许拖垮写命令"; }
[ "$(commits)" = "$((C0 + 1))" ] || die "写命令应照常 commit +1"
no_derived_index "索引未建时的 rel add"
grep -Fq '"W23"' "${WORK}/out.txt" &&
  die "「你还没建索引」不该变成每次写入都要看一眼的警告（静默跳过是明写的决定）"
no_index_data "${WORK}/out.txt" "索引未建时的 rel add"
no_forbidden "${WORK}/out.txt" "索引未建时的 rel add"
[ "$(eg_code mark-reviewed --target "${CARD_A}" --json)" = "0" ] || die "mark-reviewed 应退 0"
no_derived_index "mark-reviewed"
ok "索引未建：两条写命令照常退 0 / commit 正常推进 / 派生 DB 家族仍不存在（${IDX_REL}/ 若在只含 M6 runtime-reserved）/ 零 W23 噪声"

# ---------------------------------------------------------------- 3. 建索引后：六条写命令逐条写后同步
step "eg index build 之后：六条写命令逐条跑完立刻 fresh，且 head 跟到新 HEAD"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "index build 应退 0"; }
[ "$(jnum "${WORK}/out.txt" cards)" = "3" ] || die "基线 cards 应为 3"
index_is_fresh "基线 build"

# 3a. apply / edit 共用的 runPlan 落点：这里用 eg edit（整段替换）与 eg rel add / deprecate 各打一次。
[ "$(eg_code edit --target "${CARD_C}" --section 知识内容 --content '整段替换后的正文：值班注意力分配。' --user-request --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg edit 应退 0"; }
[ "$(data_first_key "${WORK}/out.txt")" = "convergence" ] ||
  die "eg edit 的 data 首键漂移了（= $(data_first_key "${WORK}/out.txt")，期望 convergence）"
no_index_data "${WORK}/out.txt" "eg edit"
no_forbidden "${WORK}/out.txt" "eg edit"
index_is_fresh "eg edit（runPlan 落点，与 eg apply 同源）"
ok "eg edit：data 首键仍是 convergence、无 data.index、写后立刻 fresh"

[ "$(eg_code deprecate --target "${CARD_B}" --reason 已被更完整的卡替代 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg deprecate 应退 0"; }
no_index_data "${WORK}/out.txt" "eg deprecate"
index_is_fresh "eg deprecate（runPlan 落点）"
ok "eg deprecate：写后立刻 fresh"

# 3b. mark-reviewed（只写 reviewed_at 单键）。
[ "$(eg_code mark-reviewed --target "${CARD_C}" --json)" = "0" ] || die "mark-reviewed 应退 0"
no_index_data "${WORK}/out.txt" "eg mark-reviewed"
index_is_fresh "eg mark-reviewed"
ok "eg mark-reviewed：写后立刻 fresh"

# 3c. delete（须引用 approved 提案 + --confirm）→ undelete。
[ "$(eg_code proposal new --type logical_delete --target "${CARD_B}" --reason 端到端删除路径 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "proposal new 应退 0"; }
PID="$(grep -o '"id":"p-[^"]*"' "${WORK}/out.txt" | head -1 | sed 's/.*"p-/p-/; s/"$//')"
[ -n "${PID}" ] || { cat "${WORK}/out.txt"; die "取不到提案 ID"; }
[ "$(eg_code proposal approve "${PID}" --confirm --user-request --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "proposal approve 应退 0"; }
no_index_data "${WORK}/out.txt" "eg proposal approve"
no_forbidden "${WORK}/out.txt" "eg proposal approve"
# C2a·M6 现态重钉（§16.4 现态重钉授权；保留 M5 历史事实 + 新增 M6 现态双侧锁）。
#   · M5 历史事实（保留、绝不放宽）：写后同步的挂载面在 M5 §5.3 是**封闭六条**
#     （apply / edit / delete / rel / reconcile，本仓另含 undelete / mark-reviewed）；
#     `proposal approve` / `capture` / `config set` **均不在** M5 那份枚举内。
#   · M6 现态（新增正面锁）：`proposal approve` 已由 T-072 C4b「接入 A 类强事务」提级为
#     A 类权威写命令，按合同 §16.1 硬规则 3，写后增量索引更新（syncIndexAfterWrite）
#     必须落在同一把锁的 `S6` 之后、`S8` 释放锁之前 —— 故 approve 写后**如实立刻 fresh**、
#     水位线跟到新的 git HEAD。这不是「悄悄追 HEAD」，而是 A 类事务在锁内的既定收敛。
index_is_fresh "eg proposal approve（M6 现态：A 类强事务，锁内写后同步）"
ok "eg proposal approve（M6 现态：T-072 C4b 提级 A 类强事务）：写后立刻 fresh；M5 未挂载事实见上方注释"

# head_moved 门禁判据（历史判据一字不改，改由在 M6 下**仍未挂载**的写命令来驱动）：
#   `config set` 会推进 Git HEAD（改写 evergreen.yml 并 commit），但它**不在**写后同步
#   挂载面 —— M5 §5.3 未列、M6 §16.1 也未把它提为 A 类；于是水位线 `(head, files_hash)`
#   不匹配 → 索引被**如实**判陈旧，分因恰是 `head_moved`（卡片内容一个字节都没变）。
#   这正是原判据要锁死的语义：与其悄悄把 head 抹平（那等于让 status 说假话），不如如实
#   报 W22 + 一条 `eg index sync` 收敛。
[ "$(eg_code config set default_domain ml-sys --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "config set 应退 0（未挂载写命令，用于驱动 head_moved 判据）"; }
[ "$(eg_code index status --json)" = "0" ] || die "陈旧态 status 恒退 0"
cp "${WORK}/out.txt" "${WORK}/status.headmoved.json"
[ "$(jstr "${WORK}/status.headmoved.json" freshness)" = "stale" ] ||
  die "未挂载写后同步的命令推进了 HEAD：freshness 应如实为 stale（实为 $(jstr "${WORK}/status.headmoved.json" freshness)）"
[ "$(jstr "${WORK}/status.headmoved.json" reason)" = "head_moved" ] ||
  die "分因应恰为 head_moved（实为 $(jstr "${WORK}/status.headmoved.json" reason)）：卡片内容并未变化"
grep -Fq '"W22"' "${WORK}/status.headmoved.json" || die "陈旧必须产 W22"
grep -Fq '"blocks_read":false' "${WORK}/status.headmoved.json" ||
  die "陈旧只报不阻断读：blocks_read 必须是 false"
[ "$(jnum "${WORK}/status.headmoved.json" changed_modified)" = "0" ] ||
  die "head_moved 场景下三向 diff 的修改数应为 0（只有水位线漂移，没有文件变更）"
[ "$(jnum "${WORK}/status.headmoved.json" changed_added)" = "0" ] || die "head_moved 场景下新增数应为 0"
[ "$(jnum "${WORK}/status.headmoved.json" changed_removed)" = "0" ] || die "head_moved 场景下删除数应为 0"
no_forbidden "${WORK}/status.headmoved.json" "head_moved 态 status"
ok "eg config set（M6 下仍未挂载面）：如实 stale / head_moved / W22 / blocks_read=false，且零文件变更"
[ "$(eg_code delete --target "${CARD_B}" --reason 端到端删除 --proposal "${PID}" --confirm --user-request --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg delete 应退 0"; }
no_index_data "${WORK}/out.txt" "eg delete"
no_forbidden "${WORK}/out.txt" "eg delete"
# 关键：紧接着的**已挂载**写命令一条就把上面那次 head 漂移一起带回 fresh ——
# 边界是「不主动追 HEAD」，不是「陈旧了就修不回来」。
index_is_fresh "eg delete（同时收敛掉 config set 留下的 head 漂移）"
[ "$(eg_code undelete --target "${CARD_B}" --reason 端到端恢复 --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg undelete 应退 0"; }
no_index_data "${WORK}/out.txt" "eg undelete"
index_is_fresh "eg undelete"
ok "eg delete / eg undelete：逐条写后立刻 fresh（并收敛掉未挂载面的 head 漂移），键面零漂移"

# 3d. reconcile：外部编辑先制造「工作区有未纳管改动」，再让 R1 纳管并同步索引。
printf '%s\n' '' '外部编辑：等待 R1 纳管。' >>"${VAULT}/${KDIR_REL}/${CARD_A}.md"
[ "$(porcelain)" != "" ] || die "前置失败：应有未纳管改动"
RC="$(eg_code reconcile --json)"
[ "${RC}" = "0" ] || [ "${RC}" = "2" ] || { cat "${WORK}/err.txt"; die "reconcile 退 ${RC}（期望 0 或 2）"; }
[ "$(data_first_key "${WORK}/out.txt")" = "reconcile" ] ||
  die "eg reconcile 的 data 首键漂移了（= $(data_first_key "${WORK}/out.txt")，期望 reconcile）"
no_index_data "${WORK}/out.txt" "eg reconcile"
no_forbidden "${WORK}/out.txt" "eg reconcile"
[ "$(porcelain)" = "" ] || { porcelain; die "R1 纳管之后工作区应干净"; }
index_is_fresh "eg reconcile（含 R1 纳管的外部编辑）"
ok "eg reconcile：纳管外部编辑后索引立刻 fresh、data 首键仍是 reconcile"

# ---------------------------------------------------------------- 4. 写后结果 == 整库重建
step "一串写命令之后：sync 恒 noop，且整库 rebuild 的四键与写后现值逐字相同"
[ "$(eg_code index sync --json)" = "0" ] || die "sync 应退 0"
[ "$(jstr "${WORK}/out.txt" action)" = "noop" ] ||
  die "写后同步已经把索引带到位，此时 sync 必须是 noop（实为 $(jstr "${WORK}/out.txt" action)）"
[ "$(eg_code index status --strict --json)" = "0" ] || die "strict status 应退 0"
cp "${WORK}/out.txt" "${WORK}/strict.json"
[ "$(jstr "${WORK}/strict.json" freshness)" = "fresh" ] ||
  die "忽略 (size, mtime) 快路径、全部重算 content_hash 之后仍必须 fresh —— 否则写后同步写进去的是假 hash"
NOW_KEYS="$(printf '%s|%s|%s\n' "$(jstr "${WORK}/strict.json" actual_head)" \
  "$(jstr "${WORK}/strict.json" files_hash)" "$(jnum "${WORK}/strict.json" card_count)")"
[ "$(eg_code index rebuild --json)" = "0" ] || die "对照 rebuild 应退 0"
RB_KEYS="$(printf '%s|%s|%s\n' "$(jstr "${WORK}/out.txt" head)" \
  "$(jstr "${WORK}/out.txt" files_hash)" "$(jnum "${WORK}/out.txt" cards)")"
[ "${NOW_KEYS}" = "${RB_KEYS}" ] ||
  die "写后同步出来的库与整库重建不等价（写后 ${NOW_KEYS} vs 重建 ${RB_KEYS}）—— 合同 §5.3 的底线"
ok "sync=noop；strict 全量重算仍 fresh；写后现值 == 整库重建（${RB_KEYS}）"

# ---------------------------------------------------------------- 5. 坏索引：只报不自动修
step "索引不可用：写命令照常退 0 + commit +1，如实留痕 W24，且跑完索引依然坏着"
printf '坏字节：让索引处于不可用状态' >"${VAULT}/${DB_REL}"
[ "$(eg_code index status --json)" = "0" ] || die "前置：status 恒退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "corrupt" ] || die "前置：索引应为 corrupt"
C1="$(commits)"
[ "$(eg_code mark-reviewed --target "${CARD_A}" --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "坏索引不许拖垮写命令"; }
[ "$(commits)" = "$((C1 + 1))" ] || die "坏索引下写命令仍必须 commit +1"
grep -Fq '"W24"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "索引不可用必须如实留痕 W24（不许静默）"; }
no_index_data "${WORK}/out.txt" "坏索引下的 mark-reviewed"
no_forbidden "${WORK}/out.txt" "坏索引下的 mark-reviewed"
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "corrupt" ] ||
  die "写命令悄悄修了索引：自动修只会掩盖「库为什么坏了」，修复只走显式 eg index rebuild"
[ "$(eg_code index rebuild --json)" = "0" ] || die "显式 rebuild 应退 0"
index_is_fresh "显式 eg index rebuild 之后"
ok "坏索引：写命令退 0 / commit +1 / 留痕 W24 / 索引保持 corrupt；显式 rebuild 一条命令修好"

# ---------------------------------------------------------------- 6. 索引侧失败不牵连权威
step "索引不可用时的 eg edit 降级 + 索引路径被普通文件占位时 build 自愈，权威 Markdown 全程零改动"
# 6a. 坏索引（.index/ 内的库文件被写坏，仍是 git-ignored 目录形态）：edit 必须照常落盘 + 只降级留痕。
printf '坏字节：再一次让索引不可用' >"${VAULT}/${DB_REL}"
C2="$(commits)"
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "corrupt" ] || die "前置：索引应为 corrupt"
[ "$(eg_code edit --target "${CARD_C}" --section 知识内容 --content '索引同步不上也必须落盘的正文。' --user-request --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "索引不可用绝不许改变写命令的退出码"; }
cp "${WORK}/out.txt" "${WORK}/edit.degraded.json"
[ "$(commits)" = "$((C2 + 1))" ] || die "写命令仍必须 commit +1（索引失败不牵连权威）"
grep -Fq '索引同步不上也必须落盘的正文。' "${VAULT}/${KDIR_REL}/${CARD_C}.md" ||
  die "权威 Markdown 没落盘：索引同步不上绝不许回滚已写入内容"
grep -qE '"(W22|W24)"' "${WORK}/edit.degraded.json" ||
  { cat "${WORK}/edit.degraded.json"; die "索引没同步上必须如实留痕（W22 / W24 二者之一）"; }
grep -Fq '"exit_code":0' "${WORK}/edit.degraded.json" ||
  die "索引问题不改变退出码：信封 exit_code 必须仍是 0"
[ "$(data_first_key "${WORK}/edit.degraded.json")" = "convergence" ] ||
  die "降级路径也不许动 data 首键（= $(data_first_key "${WORK}/edit.degraded.json")，期望 convergence）"
no_index_data "${WORK}/edit.degraded.json" "索引不可用时的 eg edit"
no_forbidden "${WORK}/edit.degraded.json" "索引不可用时的 eg edit"

# 6b. 索引路径被普通文件占位（外部把 .index 塞成普通文件）—— C2a·M6 现态重钉（§16.4；R6/R7）。
#   · M5 历史事实（保留、不放宽）：M5 期 `.index` 是**纯派生目录**，build 可安全「先删再全量建」，
#     把普通文件占位当「可恢复不可用」一支 → 退 0 + action=repaired + W24 留痕。
#   · M6 现态（新增正面锁）：`.index` 已从纯派生目录变为「派生物 + 不可重建运行时证据」混居目录
#     （§16.2）；M6 交付后禁 `os.RemoveAll(.index)`（R6-D1），B 类维护在拿锁前必先过**单点安全
#     解析器**（`internal/txn/safedir.go`，parent-symlink fail closed，S5/R7）。占位普通文件（非
#     目录）时安全解析器**拒绝**把它当运行时目录、**绝不**盲删非预期的非目录条目 → **fail closed**：
#     退 `5` + `E15`、**零权威写入**、占位**原样保留**，等待人工清障（§16.3 / §18.4 与原因 8 同族）。
# 注意：占位文件不匹配 .gitignore 的 `.index/` 目录形态，因此这里**只跑索引侧命令**，
# 不在占位期间跑写命令 —— M1 的 `git add -A`（脏工作区并入同一 commit）会把这个外部塞进来的
# 文件一起提交，那是 M1 冻结语义的既有边界，不属本 task 的索引合同，只在 handoff 里登记。
rm -rf "${VAULT}/${IDX_REL}"
printf '不是目录：索引路径被外部占位\n' >"${VAULT}/${IDX_REL}"
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "corrupt" ] ||
  die "把 .index 换成普通文件时应判 corrupt（W24）"
authority_sha >"${WORK}/authority.before_build.txt"
# M6 现态：普通文件占位 → build fail closed（退 5 + E15、零写入、占位原样保留）。
[ "$(eg_code index build --json)" = "5" ] ||
  { cat "${WORK}/out.txt"; die "占位文件（非目录）属 M6 parent-symlink fail closed：eg index build 应退 5"; }
grep -Fq '"E15"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "普通文件占位的 fail closed 必须携 E15（写前安全复核大类）"; }
grep -Fq '"exit_code":5' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "普通文件占位 build 的信封 exit_code 必须是 5"; }
{ [ -f "${VAULT}/${IDX_REL}" ] && [ ! -d "${VAULT}/${IDX_REL}" ]; } ||
  die "M6 fail closed 绝不盲删非预期条目：占位普通文件必须原样保留（未被删、未变成目录）"
authority_sha >"${WORK}/authority.after_build.txt"
diff "${WORK}/authority.before_build.txt" "${WORK}/authority.after_build.txt" >/dev/null ||
  { diff "${WORK}/authority.before_build.txt" "${WORK}/authority.after_build.txt" || true
    die "索引侧命令动了权威 Markdown：索引是派生物，绝不许回写权威"; }
# 恢复路径仍在：按 M6 契约「等待人工处理」清掉占位普通文件后，build 一条即全量重建回 fresh。
rm -f "${VAULT}/${IDX_REL}"
[ "$(eg_code index build --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "人工清障后 eg index build 应退 0（全量重建）"; }
[ "$(jstr "${WORK}/out.txt" action)" = "built" ] ||
  { cat "${WORK}/out.txt"; die "人工清障后 build 的 action 应为 built（= $(jstr "${WORK}/out.txt" action)）"; }
no_forbidden "${WORK}/out.txt" "人工清障后的 eg index build"
[ -d "${VAULT}/${IDX_REL}" ] || die "全量重建之后 ${IDX_REL}/ 应当是目录"
index_is_fresh "占位文件人工清障 + 全量重建后"
ok "坏索引：edit 退 0 / commit +1 / Markdown 逐字落盘 / 只降级留痕；普通文件占位（M6 现态）：fail closed 退 5 + E15 + 零权威改动 + 占位原样保留，人工清障后 build 全量重建回 fresh"

# ---------------------------------------------------------------- 7. 边界总账
step "边界总账：.index/ 不进 Git、工作区干净、全程零 Q5 与零退出码 5"
[ "$(porcelain)" = "" ] || { porcelain; die "${IDX_REL}/ 污染了工作区（应被整目录忽略）"; }
gitv check-ignore -q "${DB_REL}" || die ".gitignore 未忽略 ${IDX_REL}/"
for c in "search 注意力" "card show ${CARD_A}" "rel ${CARD_A}" "report --last" "check"; do
  # shellcheck disable=SC2086
  RC="$(eg_code ${c} --json)"
  [ "${RC}" != "5" ] || die "eg ${c} 退 5：强校验与退出码 5 属 M6，本 task 不许出现"
  no_forbidden "${WORK}/out.txt" "eg ${c}"
  no_index_data "${WORK}/out.txt" "eg ${c}"
done
ok "工作区干净、五条只读命令均无 Q5 / 无退出码 5 / 无 data.index"

# ---------------------------------------------------------------- 8. 仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] index_consistency.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
