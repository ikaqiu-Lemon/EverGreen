#!/usr/bin/env bash
# `eg index` 损坏检测与可恢复重建端到端脚本（M5 · T-evergreen.s1_main_flow-158614-065 · 阶段 4）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §3.1（`.index/` 允许文件恰三个，混入即判不可用）
#   §6（损坏检测：只报不改；坏索引**永不**通过 error 表达 —— 它是诊断，不是失败）
#   §8.1（`build` 第三支「不可用 → 可恢复重建」；`rebuild` 无条件整库重建、根本不读旧库）
#   §9（W23 missing / W24 corrupt 两码 + 封闭 reason 集合）
#   §0.1（索引**不是任何命令的前置**：索引坏了也不许拖垮读命令与写命令）
#   §10（退出码：`0` 成功 / `1` 参数非法 / `4` 派生索引写盘失败且权威零改动）
# 以及 milestone M-005 完成判据「损坏可检测、可恢复、Markdown 恒为唯一权威来源」。
#
# 本脚本只守**损坏面与重建面**（构建面与派生物边界走 m5_index_build.sh，两本互不覆盖）：
#   ① 六种**真字节**坏法各自被判 corrupt（W24）且 reason 落在封闭集合内：
#      非法文件头 / 截断 / 空文件 / eg.db 是目录 / 混入非法文件 / 目录被换成普通文件；
#      每一种都**只报不改** —— status 跑完之后坏字节仍在原地（体检不偷偷修）；
#   ② 缺 eg.db 但残留 -wal / -shm → 判 missing（W23）：残帧不算「有索引」；
#   ③ 每种坏法上 `eg index build` 都能**可恢复重建**：退 0、action=repaired、修完 healthy，
#      且如实留痕 W24（修好了也要说清修的是什么，不许静默成功）；
#   ④ `rebuild` 语义：healthy 上跑也退 0 且 action=rebuilt；坏索引上跑同样成立（不读旧库）；
#      重建结果与原索引在 `(schema_version, head, files_hash, card_count)` 四键上**逐字复现**；
#      混入的污染文件被整目录清掉（重建后文件名恰在白名单三值内）；
#   ⑤ 坏索引**不拖垮任何命令**（§0.1）：`eg search` / `eg card show` / `eg rel query` 照常退 0
#      且输出里**没有** `data.index` 这一格；写命令 `eg rel add` 照常 commit +1，
#      写后同步只**如实留痕 W24 并跳过**（不自动修、不改退出码、不加 `data.index` 键）；
#      全程不因索引坏了而改变任何既有退出码；
#      **T-…-067 重钉（只加严，原判据一条未删）**：读路径已接入索引，故坏索引下三条读命令
#      除「退 0 + 零 data.index」之外，还必须各带**恰一条** `W24` + **恰一条** `Q5`
#      （合同 §5.2 / §6.3）；`W22` 仍恒不出现（这是 corrupt 不是 stale），`eg index` 自己的
#      输出仍恒无 `Q5`（Q5 属查询域）。065 时期这里断言读命令「零 Q5」，依据是「读路径尚未
#      接索引」——该依据在 T-…-067 之后不再成立，故按事实重钉；
#   ⑥ 退出码 4：把 `.index` 换成普通文件让写盘**真失败** → 退 4、诊断码 W24、
#      权威 Markdown 全量 sha256 逐字不变、commit +0（写入侧失败绝不牵连权威数据）；
#   ⑦ 修复期间权威零改动：全程 `domains/ sources/` 的 sha256 清单只在合法写命令那一步变化。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；无 jq / sqlite3 等外部依赖
#   （只用 bash / coreutils / awk / git / go）；**一切写与构建只发生在 mktemp -d 沙箱内**，
#   真实仓库工作区一个字节都不碰（脚本末尾自查 git status）；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/index_corrupt_rebuild.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-index-corrupt.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

IDX_REL='.index'
IDX_ABS="${VAULT}/${IDX_REL}"
DB_ABS="${IDX_ABS}/eg.db"
ALLOWED='eg.db,eg.db-shm,eg.db-wal'
# 封闭 reason 集合（与 index.Reasons() 逐字对照；任一侧漂移即红）。
REASONS='index_dir_missing db_file_missing unexpected_file truncated_file open_failed integrity_check_failed schema_incomplete schema_version_mismatch watermark_self_contradiction'

CARD_A='k-20261201-attention'
CARD_B='k-20261201-rnn'

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
jstr() { jvalue "$1" "$2"; }
jnum() { jvalue "$1" "$2"; }
authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals -type f 2>/dev/null || true; } | sort |
    xargs -r sha256sum )
}
idx_files() { ( cd "${IDX_ABS}" && ls -A | sort | paste -sd, - ); }
# assert_index_family <说明>：C2a·M6 现态重钉（§16.3 runtime-reserved + §16.4 现态重钉授权；
#   保留历史事实 + 新增现态双侧锁，非放宽）。
#   · 历史事实一格不放宽：**派生 DB 家族**恒恰 ${ALLOWED} 三值（下方 case 的 * 分支原样比对）。
#   · M6 现态：`.index/` 从纯派生目录变为「派生物 + 不可重建运行时证据」混居目录（§16.2），
#     锁层按 A-53 于其下按需并存 runtime-reserved 的 `run.lock`（普通文件）/ `txn`（目录）；
#     二者不是派生物、不进 DB 家族比对，但额外条目**必须**恰是这两项而非任意杂项（case 显式枚举 = 双侧锁）。
assert_index_family() {
  local what="$1" f db_family=""
  for f in $(idx_files | tr ',' ' '); do
    case "${f}" in
      run.lock|txn|blocks) ;;
      *) db_family="${db_family:+${db_family},}${f}" ;;
    esac
  done
  [ "${db_family}" = "${ALLOWED}" ] ||
    die "${what}：派生 DB 家族 = ${db_family:-（空）}，期望恰 ${ALLOWED}（另允许 blocks 与 M6 runtime-reserved run.lock/txn）"
}
# no_stale_codes <文件> <说明>：`eg index` 自己的输出里恒无 W22 / Q5。
# W22 是陈旧码（本脚本造的全是 corrupt / missing，不是陈旧）；Q5 属**查询域**（只有读路径
# 降级才产它）。T-…-067 之后这两条对索引命令仍然成立：接索引的是 internal/query。
no_stale_codes() {
  local f="$1" what="$2" code
  for code in W22 Q5; do
    if grep -Fq "\"${code}\"" "${f}"; then cat "${f}"; die "${what}：出现了本阶段不该有的诊断码 ${code}"; fi
  done
}

# ccount <文件> <码>：数某个诊断码出现的条数（用于「恰一条」这类等号断言）。
ccount() {
  grep -o '"code":"[^"]*"' "$1" | sed 's/"code":"//; s/"$//' | grep -cx "$2" || true
}
# assert_reason_closed <reason> <说明>：reason 必须落在封闭集合内。
assert_reason_closed() {
  printf '%s\n' ${REASONS} | grep -qx "$1" || die "$2：reason=$1 不在封闭集合内（${REASONS}）"
}
# assert_corrupt_readonly <坏法说明>：status 判 corrupt(W24) + reason 封闭 + 只报不改。
# 「只报不改」的判据是**坏字节仍在原地**：体检前后 .index/ 的 sha256 清单逐字相同。
assert_corrupt_readonly() {
  local what="$1"
  ( cd "${IDX_ABS}/.." && find "${IDX_REL}" \( -type f -o -type d \) | sort ) >"${WORK}/idx.before.txt"
  ( cd "${IDX_ABS}/.." && { find "${IDX_REL}" -type f -exec sha256sum {} + 2>/dev/null || true; } | sort ) \
    >>"${WORK}/idx.before.txt"
  [ "$(eg_code index status --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "${what}：坏索引是诊断不是失败，status 必须退 0"; }
  cp "${WORK}/out.txt" "${WORK}/status.json"
  [ "$(jstr "${WORK}/status.json" health)" = "corrupt" ] ||
    { cat "${WORK}/status.json"; die "${what}：health = $(jstr "${WORK}/status.json" health)，期望 corrupt"; }
  grep -Fq '"code":"W24"' "${WORK}/status.json" || die "${what}：缺 W24"
  grep -Fq '"usable":false' "${WORK}/status.json" || die "${what}：usable 必须为 false"
  assert_reason_closed "$(jstr "${WORK}/status.json" reason)" "${what}"
  no_stale_codes "${WORK}/status.json" "${what} 的 status"
  ( cd "${IDX_ABS}/.." && find "${IDX_REL}" \( -type f -o -type d \) | sort ) >"${WORK}/idx.after.txt"
  ( cd "${IDX_ABS}/.." && { find "${IDX_REL}" -type f -exec sha256sum {} + 2>/dev/null || true; } | sort ) \
    >>"${WORK}/idx.after.txt"
  diff -u "${WORK}/idx.before.txt" "${WORK}/idx.after.txt" >/dev/null ||
    { diff -u "${WORK}/idx.before.txt" "${WORK}/idx.after.txt" || true
      die "${what}：status 动了 ${IDX_REL}/ 的内容 —— 只读体检只报不改（修复走 eg index rebuild）"; }
  printf '    · %s → W24 / reason=%s（只报不改）\n' "${what}" "$(jstr "${WORK}/status.json" reason)"
}
# assert_repaired <坏法说明>：build 可恢复重建（退 0 / action=repaired / 修完 healthy / 留痕 W24）。
assert_repaired() {
  local what="$1"
  [ "$(eg_code index build --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "${what}：坏索引不是死局，build 必须退 0"; }
  cp "${WORK}/out.txt" "${WORK}/repair.json"
  [ "$(jstr "${WORK}/repair.json" action)" = "repaired" ] ||
    { cat "${WORK}/repair.json"; die "${what}：action = $(jstr "${WORK}/repair.json" action)，期望 repaired"; }
  [ "$(jstr "${WORK}/repair.json" health)" = "healthy" ] || die "${what}：修完 health 应为 healthy"
  grep -Fq '"code":"W24"' "${WORK}/repair.json" ||
    die "${what}：修好了也必须如实留痕 W24（不许静默成功）"
  [ "$(jnum "${WORK}/repair.json" cards)" = "2" ] || die "${what}：修完 cards 应为 2"
  assert_index_family "${what}：修完 ${IDX_REL}/"
  printf '    · %s → repaired（修完 healthy、留痕 W24、目录回到白名单）\n' "${what}"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 + 语料
step "构建 eg（CGO_ENABLED=0）+ seed 两张卡并首次建索引"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
seed_card() { # id title status
  local dir="${VAULT}/domains/ai-infra/knowledge"
  mkdir -p "${dir}"
  {
    printf '%s\n' '---' "id: $1" "status: $3" "created_at: '2026-12-01'" \
      "updated_at: '2026-12-01T10:00:00+08:00'" "title: $2" 'sources: []' '---' '' \
      '## 知识内容' '' "正文占位：$2。" ''
  } >"${dir}/$1.md"
}
seed_card "${CARD_A}" "注意力机制的计算代价" active
seed_card "${CARD_B}" "RNN 的长序列衰减" deprecated
gitv add -A && gitv commit -q -m "seed: m5 index 损坏语料"
[ "$(porcelain)" = "" ] || die "前置条件失败：工作区应干净"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "首次建索引失败"; }
cp "${WORK}/out.txt" "${WORK}/baseline.json"
[ "$(jstr "${WORK}/baseline.json" action)" = "built" ] || die "首建 action 应为 built"
BASE_SCHEMA="$(jnum "${WORK}/baseline.json" schema_version)"
BASE_HEAD="$(jstr "${WORK}/baseline.json" head)"
BASE_FHASH="$(jstr "${WORK}/baseline.json" files_hash)"
BASE_CARDS="$(jnum "${WORK}/baseline.json" card_count)"
[ "${BASE_CARDS}" = "2" ] || die "首建 card_count = ${BASE_CARDS}，期望 2"
BASE_COMMITS="$(commits)"
authority_sha >"${WORK}/authority.before.txt"
[ -s "${WORK}/authority.before.txt" ] || die "权威快照为空：语料没建起来"
cp "${DB_ABS}" "${WORK}/good.db"
ok "二进制与语料就绪；基线水位线：schema=${BASE_SCHEMA} / head=${BASE_HEAD:0:12}… / cards=${BASE_CARDS}"

# ---------------------------------------------------------------- 2. 缺 eg.db 但残留 -wal/-shm → missing
step "缺 eg.db 但残留 -wal / -shm：判 missing（W23）—— 残帧不算「有索引」"
rm -f "${DB_ABS}"
: >"${IDX_ABS}/eg.db-wal"
: >"${IDX_ABS}/eg.db-shm"
[ "$(eg_code index status --json)" = "0" ] || die "missing 态 status 必须退 0"
cp "${WORK}/out.txt" "${WORK}/missing.json"
[ "$(jstr "${WORK}/missing.json" health)" = "missing" ] ||
  { cat "${WORK}/missing.json"; die "health 应为 missing"; }
grep -Fq '"code":"W23"' "${WORK}/missing.json" || die "缺 W23"
[ "$(jstr "${WORK}/missing.json" reason)" = "db_file_missing" ] ||
  die "reason = $(jstr "${WORK}/missing.json" reason)，期望 db_file_missing"
grep -Fq '"meta_readable":false' "${WORK}/missing.json" || die "meta_readable 应为 false"
# missing 走的是「全量构建」而不是「可恢复重建」：action=built。
[ "$(eg_code index build --json)" = "0" ] || die "missing 态 build 应退 0"
[ "$(jstr "${WORK}/out.txt" action)" = "built" ] ||
  die "missing 态 action = $(jstr "${WORK}/out.txt" action)，期望 built"
[ "$(idx_files)" = "${ALLOWED}" ] || assert_index_family "建完 ${IDX_REL}/"
ok "残留 WAL 帧不算索引：判 missing / W23 / db_file_missing，build 走全量构建"

# ---------------------------------------------------------------- 3. 六种真字节坏法：只报不改
step "六种真字节坏法各判 corrupt（W24）+ reason 封闭 + 只报不改"
# 坏法一：非法文件头（前 100 字节不是 SQLite 魔数）。
printf '这不是一个 SQLite 文件，只是一段中文。' >"${DB_ABS}"
assert_corrupt_readonly "非法文件头"
# 坏法二：截断（保留合法魔数但只留 40 字节 —— 连文件头都不完整）。
cp "${WORK}/good.db" "${DB_ABS}" && truncate -s 40 "${DB_ABS}"
assert_corrupt_readonly "文件截断到 40 字节"
# 坏法三：空文件（0 字节）。
: >"${DB_ABS}"
assert_corrupt_readonly "空文件（0 字节）"
# 坏法四：eg.db 是个目录。
rm -f "${DB_ABS}" && mkdir -p "${DB_ABS}"
assert_corrupt_readonly "eg.db 是目录"
rmdir "${DB_ABS}"
# 坏法五：混入非白名单文件（目录布局被破坏）。
cp "${WORK}/good.db" "${DB_ABS}"
printf 'stray\n' >"${IDX_ABS}/notes.txt"
assert_corrupt_readonly "混入非法文件 notes.txt"
# 坏法六：整个 .index 被换成普通文件（目录不可访问）。
rm -rf "${IDX_ABS}" && printf 'not a dir\n' >"${IDX_ABS}"
[ "$(eg_code index status --json)" = "0" ] || die ".index 是文件时 status 仍必须退 0"
cp "${WORK}/out.txt" "${WORK}/status.json"
[ "$(jstr "${WORK}/status.json" health)" = "corrupt" ] ||
  { cat "${WORK}/status.json"; die ".index 是文件时 health 应为 corrupt"; }
assert_reason_closed "$(jstr "${WORK}/status.json" reason)" ".index 是普通文件"
printf '    · .index 被换成普通文件 → W24 / reason=%s\n' "$(jstr "${WORK}/status.json" reason)"
ok "六种坏法全部判 corrupt / W24、reason 落在封闭集合内、体检只报不改"

# ---------------------------------------------------------------- 4. 非目录占位：M6 fail closed（退 5 + E15）
step ".index 是普通文件 / 悬空链接 → M6 单点安全解析器 fail closed 退 5 + E15、权威零改动、commit +0、占位原样保留（C2a·M6 现态重钉 §16.4 R6/R7）"
# 4a. `.index` 被外部换成普通文件 —— C2a·M6 现态重钉（§16.4；R6/R7）。
#   · M5 历史事实（保留）：M5 期 `.index` 是纯派生目录，占位属「目录位置上的污染」，build 可
#     直接清掉再整库重建（无需用户 rm -rf）→ 退 0 + repaired + W24。
#   · M6 现态（新增正面锁）：`.index` 变为「派生物 + 不可重建运行时证据」混居目录（§16.2），禁
#     `os.RemoveAll(.index)`（R6-D1）；B 类维护在拿锁前必先过单点安全解析器（`internal/txn/safedir.go`，
#     parent-symlink fail closed，S5/R7）。占位普通文件（非目录）⇒ 安全解析器拒绝把它当运行时目录、
#     绝不盲删非预期条目 → **fail closed** 退 `5` + `E15`、**零索引/零权威写入**、占位**原样保留**，等待人工清障。
[ "$(eg_code index build --json)" = "5" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die ".index 是普通文件时 M6 应 fail closed 退 5（parent-symlink fail closed）"; }
cp "${WORK}/out.txt" "${WORK}/heal.json"
grep -Fq '"E15"' "${WORK}/heal.json" || die "普通文件占位的 fail closed 必须携 E15"
grep -Fq '"exit_code":5' "${WORK}/heal.json" || die "普通文件占位 build 的信封 exit_code 必须是 5"
{ [ -f "${IDX_ABS}" ] && [ ! -d "${IDX_ABS}" ]; } ||
  die "M6 fail closed 绝不盲删非预期条目：占位普通文件必须原样保留"
[ "$(commits)" = "${BASE_COMMITS}" ] || die "fail closed 路径不得产生 commit"
authority_sha >"${WORK}/authority.heal.txt"
diff -u "${WORK}/authority.before.txt" "${WORK}/authority.heal.txt" >/dev/null ||
  die "普通文件占位的 fail closed 牵连了权威 Markdown：这是绝对禁止的"
# 人工清障后 build 一条全量重建回 healthy（恢复路径仍在）。
rm -f "${IDX_ABS}"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "人工清障后 build 应退 0（全量重建）"; }
[ "$(jstr "${WORK}/out.txt" action)" = "built" ] ||
  die "人工清障后 action = $(jstr "${WORK}/out.txt" action)，期望 built"
[ "$(jstr "${WORK}/out.txt" health)" = "healthy" ] || die "人工清障重建后 health 应为 healthy"
assert_index_family "人工清障重建后 ${IDX_REL}/"

# 4b. 让写盘**真失败**：把 .index 做成悬空符号链接 —— C2a·M6 现态重钉（§16.4；R6/R7）。
#   · M5 历史事实（保留）：M5 期体检读不到目录（判 missing），而 MkdirAll 撞上「同名条目已存在
#     但不是目录」当场失败 → 退 4（派生索引写盘失败、权威零改动、commit +0、不留半成品）。
#   · M6 现态（新增正面锁）：symlink（含悬空 / 含指向合法目标）被单点安全解析器**更早**顶回来
#     （§16.3「任一项为 symlink ⇒ fail closed」）→ 退 `5` + `E15`、**本次零索引写入**、权威零改动、
#     commit +0；退 5 比 M5 的退 4 更早、更严（写盘尝试之前即拒绝），authority 保护判据一字不弱。
#     注：index.go 的退 4 写盘失败分支（写入侧真失败）仍在，只是 symlink 触发器在 M6 下先命中安全解析器。
rm -rf "${IDX_ABS}"
ln -s ./no-such-target "${IDX_ABS}"
[ "$(eg_code index status --json)" = "0" ] || die "悬空链接下 status 仍必须退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "missing" ] ||
  die "悬空链接下 health = $(jstr "${WORK}/out.txt" health)，期望 missing"
[ "$(eg_code index build --json)" = "5" ] ||
  { cat "${WORK}/out.txt" "${WORK}/err.txt"; die "悬空链接属 M6 parent-symlink fail closed：build 应退 5"; }
cp "${WORK}/out.txt" "${WORK}/exit5.json"
grep -Fq '"exit_code":5' "${WORK}/exit5.json" || die "信封 exit_code 应为 5"
grep -Fq '"E15"' "${WORK}/exit5.json" || die "symlink fail closed 必须带 E15 诊断"
# 人类可读的解释只在文本模式给（JSON 信封里是 data.errors 那一格），两侧都要如实。
[ "$(eg_code index build)" = "5" ] || die "文本模式下 symlink fail closed 同样必须退 5"
cat "${WORK}/out.txt" "${WORK}/err.txt" >"${WORK}/exit5.txt"
grep -Fq '零索引写入' "${WORK}/exit5.txt" ||
  { cat "${WORK}/exit5.txt"; die "退 5 时必须如实声明「本次零索引写入」"; }
grep -Fq 'parent-symlink fail closed' "${WORK}/exit5.txt" || die "退 5 时必须点明 parent-symlink fail closed"
authority_sha >"${WORK}/authority.exit5.txt"
diff -u "${WORK}/authority.before.txt" "${WORK}/authority.exit5.txt" >/dev/null ||
  die "fail closed 牵连了权威 Markdown：这是绝对禁止的"
[ "$(commits)" = "${BASE_COMMITS}" ] || die "退 5 路径不得产生 commit"
# 工作区里此刻**只**该有测试自己塞进去的那个坏链接：`.gitignore` 写的是目录形态 `.index/`，
# 一个悬空符号链接自然不在忽略范围内 —— 这恰好证明它是**外部污染**而不是本命令的产物。
[ "$(porcelain)" = "?? .index" ] ||
  { porcelain; die "退 5 路径除测试自造的坏链接外不得留下任何东西"; }
# 用户按提示清掉那个坏链接后即可恢复（索引恒为可重建派生，零信息损失）。
rm -f "${IDX_ABS}"
[ "$(porcelain)" = "" ] || { porcelain; die "清掉坏链接后工作区应干净"; }
[ "$(eg_code index build --json)" = "0" ] || die "清掉坏链接后 build 应退 0"
[ "$(jstr "${WORK}/out.txt" action)" = "built" ] || die "恢复后的 action 应为 built"
ok "普通文件 / 悬空链接占位：M6 单点安全解析器 fail closed 退 5 + E15 且权威零改动 / commit +0 / 占位原样保留，清掉即恢复"

# ---------------------------------------------------------------- 5. 可恢复重建（build 第三支）
step "build 第三支：三种坏法各自 action=repaired、修完 healthy、如实留痕 W24"
printf '坏字节：非法头' >"${DB_ABS}"
assert_repaired "非法文件头"
cp "${DB_ABS}" "${WORK}/good2.db"
truncate -s 40 "${DB_ABS}"
assert_repaired "文件截断"
printf 'stray\n' >"${IDX_ABS}/notes.txt"
assert_repaired "混入非法文件"
# 修完的水位线四键必须与基线逐字复现（同一份权威 + 同一个 head → 同一条水位线）。
[ "$(eg_code index status --json)" = "0" ] || die "修完 status 应退 0"
cp "${WORK}/out.txt" "${WORK}/after_repair.json"
[ "$(jnum "${WORK}/after_repair.json" schema_version)" = "${BASE_SCHEMA}" ] || die "schema_version 漂移"
[ "$(jstr "${WORK}/after_repair.json" head)" = "${BASE_HEAD}" ] || die "head 漂移"
[ "$(jstr "${WORK}/after_repair.json" files_hash)" = "${BASE_FHASH}" ] || die "files_hash 漂移"
[ "$(jnum "${WORK}/after_repair.json" card_count)" = "${BASE_CARDS}" ] || die "card_count 漂移"
ok "三种坏法都能可恢复重建，且修完水位线四键与基线逐字复现"

# ---------------------------------------------------------------- 6. rebuild 语义
step "rebuild：healthy 与 corrupt 上都退 0、action=rebuilt、四键复现、污染文件被清掉"
[ "$(eg_code index rebuild --json)" = "0" ] || { cat "${WORK}/err.txt"; die "healthy 上 rebuild 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/rebuild1.json"
[ "$(jstr "${WORK}/rebuild1.json" action)" = "rebuilt" ] ||
  die "action = $(jstr "${WORK}/rebuild1.json" action)，期望 rebuilt"
[ "$(jstr "${WORK}/rebuild1.json" files_hash)" = "${BASE_FHASH}" ] || die "rebuild 后 files_hash 漂移"
[ "$(jnum "${WORK}/rebuild1.json" cards)" = "2" ] || die "rebuild 后 cards 应为 2"
# 坏索引 + 混入污染文件上 rebuild：它根本不读旧库，一次就干净。
printf '坏字节' >"${DB_ABS}"
printf 'stray\n' >"${IDX_ABS}/notes.txt"
mkdir -p "${IDX_ABS}/subdir"
[ "$(eg_code index rebuild --json)" = "0" ] || { cat "${WORK}/err.txt"; die "corrupt 上 rebuild 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/rebuild2.json"
[ "$(jstr "${WORK}/rebuild2.json" action)" = "rebuilt" ] || die "corrupt 上 action 应为 rebuilt"
[ "$(jstr "${WORK}/rebuild2.json" health)" = "healthy" ] || die "rebuild 完应 healthy"
grep -Fq '"code":"W24"' "${WORK}/rebuild2.json" ||
  die "在坏索引上重建也要留痕 W24（用户得知道自己修的不是例行重建）"
[ "$(idx_files)" = "${ALLOWED}" ] ||
  assert_index_family "rebuild 后 ${IDX_REL}/（污染文件与子目录必须被清掉，仅 M6 runtime-reserved 例外）"
[ "$(jstr "${WORK}/rebuild2.json" files_hash)" = "${BASE_FHASH}" ] || die "rebuild 后 files_hash 漂移"
ok "rebuild 在 healthy / corrupt 上都成立；污染文件与子目录被整目录清掉，四键复现"

# ---------------------------------------------------------------- 7. 坏索引不拖垮任何命令（§0.1）
step "坏索引不是任何命令的前置：读命令照常退 0、写命令照常 commit +1，且输出无 data.index"
printf '坏字节：让索引处于不可用状态' >"${DB_ABS}"
[ "$(eg_code index status --json)" = "0" ] || die "前置：索引应处于 corrupt 态且 status 退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "corrupt" ] || die "前置：索引应处于 corrupt 态"
for c in "search 注意力" "card show ${CARD_A}" "rel ${CARD_A}"; do
  # shellcheck disable=SC2086
  [ "$(eg_code ${c} --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "索引坏了不许拖垮读命令：eg ${c} 应退 0"; }
  # 原判据保留：data 键集合不扩张，索引事实绝不进 data（合同 §6.4）。
  grep -Fq '"data":{"index":' "${WORK}/out.txt" &&
    die "eg ${c} 的输出里出现了 data.index：索引事实只能经诊断承载（合同 §6.4）"
  # T-…-067 新增判据：坏索引下读路径必须降级留痕，恰一条 W24 + 恰一条 Q5，且不误报 W22。
  [ "$(ccount "${WORK}/out.txt" W24)" = "1" ] ||
    { cat "${WORK}/out.txt"; die "eg ${c}：坏索引下应恰一条 W24"; }
  [ "$(ccount "${WORK}/out.txt" Q5)" = "1" ] ||
    { cat "${WORK}/out.txt"; die "eg ${c}：坏索引下降级应恰一条 Q5（有原因码必有 Q5）"; }
  [ "$(ccount "${WORK}/out.txt" W22)" = "0" ] ||
    { cat "${WORK}/out.txt"; die "eg ${c}：corrupt 不是 stale，不许出现 W22"; }
done
C0="$(commits)"
[ "$(eg_code rel add "${CARD_A}" supports "${CARD_B}" --reason 坏索引不该拦住写路径)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "索引坏了不许拖垮写命令"; }
[ "$(commits)" = "$((C0 + 1))" ] || die "写命令必须照常 commit +1（索引不是写命令的前置）"
grep -Fq '"data":{"index":' "${WORK}/out.txt" &&
  die "写后索引同步只许追加既有诊断结构，不许给写命令加 data.index 这一格"
# 写后同步在坏索引上只**如实留痕**：W24 在场，但绝不自动修（自动修会掩盖库为什么坏）。
grep -Fq '"W24"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "索引不可用时写命令应如实留痕 W24（不许静默跳过）"; }
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "corrupt" ] ||
  die "写命令悄悄修了索引：写后同步只留痕不自动修（修复只走用户显式 eg index rebuild）"
ok "坏索引不拖垮读写命令；写后同步如实留痕 W24 并跳过，索引状态保持 corrupt"

# ---------------------------------------------------------------- 8. 权威零改动总账
step "权威零改动总账：除上一步 rel add 合法改动的一张卡外，字节零变化"
[ "$(eg_code index rebuild --json)" = "0" ] || die "收尾 rebuild 应退 0"
authority_sha >"${WORK}/authority.after.txt"
diff -u "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" >"${WORK}/auth.diff" || true
UNEXPECTED="$( { grep -E '^[+-][0-9a-f]{64} ' "${WORK}/auth.diff" || true; } |
  { grep -vF "${CARD_A}.md" || true; } | wc -l | tr -d ' ')"
[ "${UNEXPECTED}" = "0" ] ||
  { cat "${WORK}/auth.diff"; die "除 rel add 合法改动的 ${CARD_A}.md 外，权威文件被动了 ${UNEXPECTED} 处"; }
[ "$(porcelain)" = "" ] || { porcelain; die "${IDX_REL}/ 污染了工作区（应被 .gitignore 整目录忽略）"; }
ok "权威 Markdown 只在合法写命令那一步变化；工作区干净"

# ---------------------------------------------------------------- 9. 仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] index_corrupt_rebuild.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
