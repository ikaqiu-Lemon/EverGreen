#!/usr/bin/env bash
# `eg index build|status` 端到端脚本（M5 · T-evergreen.s1_main_flow-158614-065 · 阶段 4）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §3（`.index/` 派生物布局：允许文件恰 eg.db / eg.db-wal / eg.db-shm，整目录进 .gitignore）
#   §4（固定 Schema：schema_version = IndexSchemaVersion、六张表、index_meta 六键、tokenizer 三档、确定性构建）
#   §5（水位线 `(head, files_hash)` 由权威 Markdown 复算）
#   §7（A-41 纯 Go modernc.org/sqlite + CGO_ENABLED=0 静态构建）
#   §8.1（`eg index build` 三支语义 / `status` 恒退 0 / 只写 `.index/`）
#   §9（W22 stale / W23 missing / W24 corrupt 三码；Q5 属 T-…-067，本阶段恒不产）
# 以及 milestone M-005 完成判据「索引可完整重建、Markdown 恒为唯一权威来源」。
#
# 本脚本只守**构建面与派生物边界**（损坏检测与可恢复重建走 m5_index_corrupt_rebuild.sh，
# 两本脚本互不覆盖）：
#   ① 纯 Go 静态构建：`CGO_ENABLED=0 go build` 成功、二进制 build info 里 `CGO_ENABLED=0`、
#      依赖图含 `modernc.org/sqlite`、全仓 `mattn/go-sqlite3` 恒 0 命中；
#   ② 空索引态：`status` 退 0、health=missing、W23 在场，且 status **自己不建**索引；
#   ③ 全量构建：`build` 退 0、action=built、health=healthy、计数与语料对得上、
#      `index_meta` 六键齐全且 schema_version 为十进制正整数（真值单点 = 二进制的
#      IndexSchemaVersion，随 Schema 演进抬升）/ tokenizer_mode 在三档内；
#   ④ 幂等 no-op：再跑 `build` → action=noop，且 `eg.db` 的 size + mtime + sha256 **逐字不变**，
#      输出里**没有**本次写入计数（零写入就不许造 0）；
#   ⑤ 水位线可复算：`head` == `git rev-parse HEAD`；`files_hash` 在库不变时两次读取相同；
#      写命令产生新 commit 后索引**已被写后同步跟上**（T-…-066 阶段 B：head 前进、仍 fresh）；
#      而**绕过 CLI 的外部编辑**会被如实判陈旧（stale + `W22`，且只报不阻断读），
#      `eg index sync` 一条命令收敛回 fresh；
#   ⑥ 派生物边界：`.index/` 文件名恰在白名单三值内、`git status --porcelain` 逐字不变、
#      `git log` 条数 +0、`domains/ sources/` 全量 .md 的 sha256 清单逐字不变；
#      整目录 `rm -rf` 后 `build` 复原且水位线三键（schema_version / head / files_hash）复现；
#   ⑦ 已做面 / 未做面：`eg index sync` 与 `status --strict` 各退 0（T-…-066 已落地），
#      `--strict` 挂到 build / rebuild / sync 仍退 1，`--limit` 对 index status 无语义仍退 1；
#      `eg bench` 在 T-…-068 已交付，故命令面按当期事实**正面**断言在册（065 期原断言「bench 缺席」，
#      依据是「属 T-…-068 尚未交付」——该依据在 T-…-068 之后不再成立，按事实重钉、强度不降：
#      命令面在册 + 参数面仍封闭 + index 子命令面未扩张，采样语义由 m5_bench_p95.sh 专管）；
#      index 域的任何输出里恒无 `Q5`（读路径降级属查询域，不由 index 命令产出）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；无 jq / sqlite3 等外部依赖
#   （只用 bash / coreutils / awk / git / go）；**一切写与构建只发生在 mktemp -d 沙箱内**，
#   真实仓库工作区一个字节都不碰（脚本末尾自查 git status）；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/index_build.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-index-build.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

IDX_REL='.index'
DB_REL="${IDX_REL}/eg.db"
ALLOWED_FILES='eg.db,eg.db-shm,eg.db-wal'   # 升序（与 ls 排序口径一致）
TOKENIZERS='trigram unicode61_bigram like_scan'

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

# idx_obj <信封文件>：`data.index` 那一整个对象的紧凑原文。
# 前缀写成 `"data":{"index":`，因此这个切片顺带断言了「data 的首键就是 index」；
# 该对象是**扁平** map（值里没有嵌套对象），所以 `[^}]*}` 取到的就是完整一格。
# 一切取值都必须先切片再取键 —— 否则会取到报告里的同名键（报告也有 cards / relations）。
idx_obj() {
  grep -o '"data":{"index":{[^}]*}' "$1" | head -1 ||
    { cat "$1"; die "取不到 data.index（data 首键必须是 index）"; }
}
# jnum <信封文件> <键>：取 data.index 下的数字值。
jnum() { idx_obj "$1" | grep -o "\"$2\":-\?[0-9][0-9]*" | head -1 | sed "s/\"$2\"://"; }
# jstr <信封文件> <键>：取 data.index 下的字符串值。
jstr() { idx_obj "$1" | grep -o "\"$2\":\"[^\"]*\"" | head -1 | sed "s/\"$2\":\"//; s/\"$//"; }
# jhas <信封文件> <键>：data.index 下是否有这一格。
jhas() { idx_obj "$1" | grep -Fq "\"$2\":"; }
# authority_sha：domains/ sources/ proposals/ 下全部文件的 sha256 清单（权威零改动的判据）。
# 某个根目录不存在是允许的（比如还没有任何提案），因此 find 的退出码不参与判定。
authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals -type f 2>/dev/null || true; } | sort |
    xargs -r sha256sum )
}
# db_fingerprint：eg.db 的 size + mtime + sha256（no-op「零写入」的判据）。
db_fingerprint() {
  ( cd "${VAULT}" && eg_stat_size_mtime "${DB_REL}" && sha256sum "${DB_REL}" )
}
# idx_files：.index/ 下的文件名（升序、逗号分隔）。
idx_files() { ( cd "${VAULT}/${IDX_REL}" && ls -A | sort | paste -sd, - ); }
# no_stale_codes <文件> <说明>：在**索引与权威一致**的态里，输出恒无 W22 / Q5
# （W22 只许出现在真陈旧那一格，Q5 属 T-…-067 全程不许出现）。
no_stale_codes() {
  local f="$1" what="$2"
  for code in W22 Q5; do
    if grep -Fq "\"${code}\"" "${f}"; then
      cat "${f}"; die "${what}：出现了本阶段不该有的诊断码 ${code}"
    fi
  done
}
# no_q5 <文件> <说明>：陈旧态也必须零 Q5（读路径降级属 T-…-067，未做不许假装已做）。
no_q5() {
  local f="$1" what="$2"
  if grep -Fq '"Q5"' "${f}"; then
    cat "${f}"; die "${what}：出现了 Q5（读路径降级属 T-…-067）"
  fi
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 纯 Go 静态构建（A-41）
step "CGO_ENABLED=0 构建 + 纯 Go SQLite 证据（modernc.org/sqlite，零 mattn/go-sqlite3）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
# 二进制自带的 build info 是最硬的证据：它记录了实际生效的 CGO_ENABLED。
BUILDINFO="$(cd "${REPO_ROOT}" && go version -m "${EG}" 2>/dev/null || true)"
printf '%s\n' "${BUILDINFO}" | grep -Fq 'CGO_ENABLED=0' ||
  die "二进制 build info 里没有 CGO_ENABLED=0（静态构建口径被破坏）"
(cd "${REPO_ROOT}" && go list -deps ./cmd/eg) >"${WORK}/deps.txt" || die "go list -deps 失败"
grep -Fq 'modernc.org/sqlite' "${WORK}/deps.txt" ||
  die "依赖图里没有 modernc.org/sqlite（A-41 选型被换掉了）"
MATTN="$( { grep -rF 'mattn/go-sqlite3' "${REPO_ROOT}/go.mod" "${REPO_ROOT}/go.sum" \
  "${REPO_ROOT}/internal" "${REPO_ROOT}/cmd" || true; } | wc -l | tr -d ' ')"
[ "${MATTN}" = "0" ] || die "仓库里出现 mattn/go-sqlite3（${MATTN} 处）：A-41 明令禁止 CGO 驱动"
ok "二进制就绪且 CGO_ENABLED=0：$(eg --version | head -1)；驱动 = modernc.org/sqlite"

# ---------------------------------------------------------------- 2. seed 语料
step "seed 语料：三张卡（含一张 deprecated）+ 一个不可解析 .md + 一条真实关系"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"

seed_card() { # id title status created updated
  local dir="${VAULT}/domains/ai-infra/knowledge"
  mkdir -p "${dir}"
  {
    printf '%s\n' '---' "id: $1" "status: $3" "created_at: '$4'" "updated_at: '$5'" \
      "title: $2" 'sources: []' '---' '' '## 知识内容' '' "正文占位：$2。" ''
  } >"${dir}/$1.md"
}
seed_card "${CARD_A}" "注意力机制的计算代价" active 2026-12-01 "2026-12-01T10:00:00+08:00"
seed_card "${CARD_B}" "RNN 的长序列衰减" deprecated 2026-12-01 "2026-12-01T10:05:00+08:00"
seed_card "${CARD_C}" "运维值班注意力分配" active 2026-12-01 "2026-12-01T10:10:00+08:00"
printf -- '---\n- 1\n---\n\n# 坏卡\n' >"${VAULT}/domains/ai-infra/knowledge/broken.md"
gitv add -A && gitv commit -q -m "seed: m5 index 语料"
[ "$(porcelain)" = "" ] || die "前置条件失败：工作区应干净"
# 一条真实关系（走 eg rel add 写路径，顺带产生一次 commit）。
[ "$(eg_code rel add "${CARD_A}" supports "${CARD_B}" --reason 端到端语料)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg rel add 失败"; }
[ "$(porcelain)" = "" ] || die "前置条件失败：rel add 之后工作区应干净"
ok "3 张卡 + 1 个坏文件 + 1 条 supports 关系入库，工作区干净"

BASE_COMMITS="$(commits)"
BASE_STATUS="$(porcelain)"
authority_sha >"${WORK}/authority.before.txt"
[ -s "${WORK}/authority.before.txt" ] || die "权威快照为空：语料没建起来，后面的反证会失去意义"

# ---------------------------------------------------------------- 3. 空索引态：status 退 0 且不建索引
step "索引不存在：eg index status 退 0、health=missing、W23 在场，且 status 自己不建索引"
[ "$(eg_code index status --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "missing 态 status 必须退 0（索引坏了是诊断不是失败）"; }
cp "${WORK}/out.txt" "${WORK}/status.missing.json"
[ "$(jstr "${WORK}/status.missing.json" health)" = "missing" ] ||
  die "health = $(jstr "${WORK}/status.missing.json" health)，期望 missing"
grep -Fq '"code":"W23"' "${WORK}/status.missing.json" || die "缺 W23（索引缺失）"
grep -Fq '"exit_code":0' "${WORK}/status.missing.json" || die "信封 exit_code 应为 0"
grep -Fq '"meta_readable":false' "${WORK}/status.missing.json" ||
  die "索引不存在时 meta_readable 必须为 false（不拿零值冒充事实）"
# ── C2a·M6 现态重钉（§16.1 只读命令不拿锁不建索引 + §16.3 runtime-reserved + §16.4 现态重钉授权；
#    保留历史事实 + 新增现态双侧锁，非放宽）──
# 历史事实一格不放宽：status 是只读命令，**绝不建派生索引 DB**——eg.db / eg.db-wal / eg.db-shm 三者恒不存在。
# M6 现态：前置的 `eg rel add`（A 类写命令）已按 §16.1/S1 拿锁，锁层按 A-53 于 `.index/` 下按需建出
# runtime-reserved 的 `run.lock`（+ 可能的 `txn/`）——建 `.index/` 目录 ≠ 建索引。故 `.index/` 若存在，
# 只允许 runtime-reserved 条目（run.lock / txn），且**必无**派生 DB 家族。
for db in eg.db eg.db-wal eg.db-shm; do
  [ ! -e "${VAULT}/${IDX_REL}/${db}" ] || die "status 建出了派生 DB ${db}：只读命令绝不建索引"
done
if [ -e "${VAULT}/${IDX_REL}" ]; then
  for entry in $(idx_files | tr ',' ' '); do
    case "${entry}" in
      run.lock|txn) ;;
      *) die "${IDX_REL}/ 出现非 runtime-reserved 条目「${entry}」：missing 态只允许锁层的 run.lock / txn（§16.3）" ;;
    esac
  done
fi
no_stale_codes "${WORK}/status.missing.json" "missing 态 status"
ok "status 退 0、health=missing、W23 在场，且派生 DB 家族仍不存在（${IDX_REL}/ 若在只含 M6 runtime-reserved）"

# ---------------------------------------------------------------- 4. 全量构建
step "eg index build：action=built、health=healthy、计数与语料一致、index_meta 六键齐全"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg index build 应退 0"; }
cp "${WORK}/out.txt" "${WORK}/build.json"
[ "$(jstr "${WORK}/build.json" action)" = "built" ] ||
  die "action = $(jstr "${WORK}/build.json" action)，期望 built"
[ "$(jstr "${WORK}/build.json" health)" = "healthy" ] ||
  die "建完 health = $(jstr "${WORK}/build.json" health)，期望 healthy"
grep -Fq '"usable":true' "${WORK}/build.json" || die "建完 usable 必须为 true"
# 计数与语料对得上：3 张卡（含 deprecated，失效卡同等入索引）、1 条关系、1 个坏文件被跳过。
[ "$(jnum "${WORK}/build.json" cards)" = "3" ] ||
  die "cards = $(jnum "${WORK}/build.json" cards)，期望 3（失效卡同等入索引）"
[ "$(jnum "${WORK}/build.json" relations)" = "1" ] ||
  die "relations = $(jnum "${WORK}/build.json" relations)，期望 1"
# `skipped` 表的取值域恰 2 值（file_changed / user_block_unsafe，均属**写路径**），
# 因此全量构建后本表恒 0 行；而「不可解析的 .md」是**扫描层**事实，走 Q1 诊断如实上报 ——
# 两条路都必须成立，缺任一条就等于静默丢文件。
[ "$(jnum "${WORK}/build.json" skipped)" = "0" ] ||
  die "skipped = $(jnum "${WORK}/build.json" skipped)，期望 0（该表由写路径填充，全量构建恒 0 行）"
grep -Fq '"code":"Q1"' "${WORK}/build.json" ||
  die "不可解析的 .md 必须走 Q1 如实上报（绝不静默跳过）"
grep -Fq 'broken.md' "${WORK}/build.json" ||
  die "Q1 诊断里必须逐字带上被跳过的文件路径"
# index_meta 六键 + 固定 schema 版本 + tokenizer 落在三档内。
for k in schema_version head files_hash tokenizer_mode built_at_unix card_count; do
  jhas "${WORK}/build.json" "${k}" || die "data.index 缺 index_meta 键 ${k}"
done
# schema_version 的**真值单点**是二进制里的 `IndexSchemaVersion`（合同 §4.2 逐字如此写：
# 「`IndexSchemaVersion` 的十进制字符串」），并不是某个具体数字 —— 它会随 Schema 演进抬升
# （T-005-A 的 kind / validation 两列就把它从 1 抬到了 2）。因此这里**不再抄写字面量**，
# 改为按合同实际约束的三件事验：① 是十进制正整数；② 全脚本各观测点逐字同一个值
# （见第 8 节复原构建）；③ 版本不匹配时**永不迁移、只整库重建**（那条语义由
# index_corrupt_rebuild.sh 的 schema_version_mismatch 分支与单测 / CLI 合同覆盖）。
# 抄字面量正是 v1→v2 抬升后本脚本假红的成因：它锁住的是「当时是几」，而不是合同。
SCHEMA_V="$(jnum "${WORK}/build.json" schema_version)"
printf '%s' "${SCHEMA_V}" | grep -Eqx '[1-9][0-9]*' ||
  die "schema_version = ${SCHEMA_V}，期望十进制正整数（真值单点 = 二进制的 IndexSchemaVersion）"
[ "$(jnum "${WORK}/build.json" card_count)" = "3" ] || die "index_meta.card_count 应为 3"
TOK="$(jstr "${WORK}/build.json" tokenizer_mode)"
printf '%s\n' ${TOKENIZERS} | grep -qx "${TOK}" || die "tokenizer_mode = ${TOK}，不在三档 ${TOKENIZERS} 内"
no_stale_codes "${WORK}/build.json" "build 输出"
ok "action=built、cards=3 / relations=1、坏文件走 Q1、schema_version=${SCHEMA_V}、tokenizer_mode=${TOK}"

# ---------------------------------------------------------------- 5. 幂等 no-op：零写入
step "再跑 eg index build：action=noop，且 eg.db 的 size + mtime + sha256 逐字不变"
db_fingerprint >"${WORK}/db.before.txt"
sleep 1   # 让 mtime 有变化的机会：这一秒是为了让「零写入」这条断言真的有分辨力
[ "$(eg_code index build --json)" = "0" ] || die "healthy 态 build 应退 0"
cp "${WORK}/out.txt" "${WORK}/noop.json"
[ "$(jstr "${WORK}/noop.json" action)" = "noop" ] ||
  die "action = $(jstr "${WORK}/noop.json" action)，期望 noop（索引原本健康就不该重建）"
db_fingerprint >"${WORK}/db.after.txt"
diff -u "${WORK}/db.before.txt" "${WORK}/db.after.txt" >/dev/null ||
  { diff -u "${WORK}/db.before.txt" "${WORK}/db.after.txt" || true; die "no-op 分支动了 eg.db：它必须零写入"; }
# 零写入就不许造「本次写了 0 张卡」这种假事实。
for k in cards relations files skipped dropped_duplicate_cards; do
  ! jhas "${WORK}/noop.json" "${k}" ||
    die "no-op 分支不得给出本次写入计数（键 ${k}）：事实是「本次一个字节都没写」"
done
# 人类可读面同样要说实话（摘要只在文本模式输出，JSON 信封里没有这一格）。
[ "$(eg_code index build)" = "0" ] || die "文本模式 build 应退 0"
grep -Fq '本次零写入' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "no-op 的人类可读摘要必须如实说明本次零写入"; }
db_fingerprint >"${WORK}/db.after2.txt"
diff -u "${WORK}/db.before.txt" "${WORK}/db.after2.txt" >/dev/null ||
  die "文本模式的 no-op 也动了 eg.db：零写入就是零写入"
ok "action=noop 且 eg.db 指纹逐字不变；输出里没有伪造的本次写入计数"

# ---------------------------------------------------------------- 6. 水位线可复算 + 陈旧如实判定
step "水位线：head == git HEAD；写命令写后同步跟上；外部编辑判陈旧（W22）且 sync 可收敛"
GIT_HEAD="$(gitv rev-parse HEAD)"
IDX_HEAD="$(jstr "${WORK}/build.json" head)"
[ -n "${IDX_HEAD}" ] || die "index_meta.head 为空"
case "${GIT_HEAD}" in "${IDX_HEAD}"*) ;; *) die "index_meta.head=${IDX_HEAD} 与 git HEAD=${GIT_HEAD} 不一致" ;; esac
FILES_HASH="$(jstr "${WORK}/build.json" files_hash)"
[ -n "${FILES_HASH}" ] || die "index_meta.files_hash 为空"
[ "$(eg_code index status --json)" = "0" ] || die "healthy 态 status 应退 0"
[ "$(jstr "${WORK}/out.txt" files_hash)" = "${FILES_HASH}" ] ||
  die "库不变时两次读到的 files_hash 不同：水位线必须确定"
[ "$(jstr "${WORK}/out.txt" freshness)" = "fresh" ] || die "刚构建完必须是 fresh"
# 6a. 走 CLI 的写命令：Markdown 落盘 + commit 之后索引被**写后同步**跟上（T-…-066 阶段 B）。
[ "$(eg_code rel add "${CARD_A}" supports "${CARD_C}" --reason 触发写后索引同步)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "eg rel add 失败"; }
NEW_HEAD="$(gitv rev-parse HEAD)"
[ "${NEW_HEAD}" != "${GIT_HEAD}" ] || die "前置失败：写命令没有产生新 commit"
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
cp "${WORK}/out.txt" "${WORK}/status.afterwrite.json"
[ "$(jstr "${WORK}/status.afterwrite.json" health)" = "healthy" ] || die "写后索引应仍 healthy"
[ "$(jstr "${WORK}/status.afterwrite.json" freshness)" = "fresh" ] ||
  die "写命令已挂写后同步：Markdown 落盘后索引应仍 fresh（不留陈旧尾巴）"
case "${NEW_HEAD}" in "$(jstr "${WORK}/status.afterwrite.json" head)"*) ;;
  *) die "写后同步没把水位线推到新 HEAD（index head=$(jstr "${WORK}/status.afterwrite.json" head)）" ;; esac
no_stale_codes "${WORK}/status.afterwrite.json" "写后同步完成的 status"
ok "写命令写后同步：head 跟到新 commit、仍 fresh、零 W22"
# 6b. 绕过 CLI 的外部编辑：索引无从知晓 → 必须如实判陈旧（W22），且只报不阻断读。
CARD_B_PATH="${VAULT}/domains/ai-infra/knowledge/${CARD_B}.md"
cp "${CARD_B_PATH}" "${WORK}/cardB.orig.md"
printf '%s\n' '' '外部编辑：绕过 CLI 直接改权威文件。' >>"${CARD_B_PATH}"
[ "$(eg_code index status --json)" = "0" ] || die "陈旧态 status 仍必须退 0（体检不阻断）"
cp "${WORK}/out.txt" "${WORK}/status.stale.json"
[ "$(jstr "${WORK}/status.stale.json" health)" = "healthy" ] ||
  die "陈旧不是损坏：health 仍应是 healthy（陈旧只体现在 freshness）"
[ "$(jstr "${WORK}/status.stale.json" freshness)" = "stale" ] ||
  die "外部编辑后必须判陈旧：freshness 应为 stale"
grep -Fq '"W22"' "${WORK}/status.stale.json" || die "陈旧必须产 W22（合同 §9）"
grep -Fq '"blocks_read":false' "${WORK}/status.stale.json" ||
  die "陈旧只报不阻断读：blocks_read 必须是 false"
[ "$(jnum "${WORK}/status.stale.json" changed_modified)" = "1" ] ||
  die "三向 diff 应恰好认出 1 个被改文件"
no_q5 "${WORK}/status.stale.json" "陈旧态 status"
# 6c. 一条 sync 收敛回 fresh；随后把外部编辑还原并再 sync，权威字节回到基线。
[ "$(eg_code index sync --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg index sync 应退 0"; }
[ "$(jstr "${WORK}/out.txt" action)" = "synced" ] ||
  die "healthy 索引 + 真变更时 sync 的 action 应是 synced（实为 $(jstr "${WORK}/out.txt" action)）"
grep -Fq '"degraded":false' "${WORK}/out.txt" || die "healthy 索引上的 sync 不该走退化分支"
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
[ "$(jstr "${WORK}/out.txt" freshness)" = "fresh" ] || die "sync 之后必须 fresh"
no_stale_codes "${WORK}/out.txt" "sync 收敛后的 status"
cp "${WORK}/cardB.orig.md" "${CARD_B_PATH}"
[ "$(eg_code index sync --json)" = "0" ] || die "还原后的 sync 应退 0"
[ "$(eg_code index status --json)" = "0" ] || die "status 恒退 0"
[ "$(jstr "${WORK}/out.txt" freshness)" = "fresh" ] || die "还原后 sync 应回到 fresh"
no_stale_codes "${WORK}/out.txt" "还原并收敛后的 status"
ok "外部编辑 → stale + W22（不阻断读）；eg index sync 一条命令收敛回 fresh"

# ---------------------------------------------------------------- 7. 派生物边界
step "派生物边界：.index/ 文件名白名单、git 零污染、权威 .md 字节零改动"
FILES_NOW="$(idx_files)"
# ── C2a·M6 现态重钉（§3 派生物白名单 + §16.3 runtime-reserved + §16.4 现态重钉授权；保留历史事实 + 现态双侧锁）──
# 历史事实一格不放宽：**派生 DB 家族**只许 eg.db / eg.db-wal / eg.db-shm 三值（下方 case 的 * 分支原样复算）。
# M6 现态：build 是 B 类写命令，其锁层按 §16.3/A-53 在 `.index/` 下并存 runtime-reserved 的 run.lock / txn；
# 二者不是派生物、不进白名单比对，但**必须**是锁层这两项而非任意杂项（case 显式枚举 = 双侧锁，非放宽）。
for f in $(printf '%s' "${FILES_NOW}" | tr ',' ' '); do
  case "${f}" in
    run.lock|txn) ;;  # M6 runtime-reserved（§16.3）：锁文件 / 事务日志目录，非派生物
    *) printf '%s\n' "${ALLOWED_FILES}" | tr ',' '\n' | grep -qx "${f}" ||
         die "${IDX_REL}/ 出现非白名单文件 ${f}（派生 DB 允许集合恰 ${ALLOWED_FILES}，另加 M6 runtime-reserved run.lock/txn）" ;;
  esac
done
[ "$(porcelain)" = "${BASE_STATUS}" ] ||
  { porcelain; die "${IDX_REL}/ 污染了工作区：eg init 已把整目录写进 .gitignore"; }
gitv check-ignore -q "${IDX_REL}/eg.db" || die ".gitignore 未忽略 ${IDX_REL}/"
# index 命令恒 0 次 commit：这里的 +1 是上一步 rel add 制造的，index 自己一次都没提交。
[ "$(commits)" = "$((BASE_COMMITS + 1))" ] ||
  die "commit 数 = $(commits)，期望 $((BASE_COMMITS + 1))（只有 rel add 那一次；eg index 恒 0 次提交）"
grep -Fq '"commit":null' "${WORK}/build.json" || die "build 报告里 git.commit 必须是 null"
authority_sha >"${WORK}/authority.after.txt"
diff -u "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" >"${WORK}/auth.diff" || true
# 上一步 rel add 会合法改动 CARD_A；除它之外权威文件字节必须一个都没动。
UNEXPECTED="$( { grep -E '^[+-][0-9a-f]{64} ' "${WORK}/auth.diff" || true; } |
  { grep -vF "${CARD_A}.md" || true; } | wc -l | tr -d ' ')"
[ "${UNEXPECTED}" = "0" ] ||
  { cat "${WORK}/auth.diff"; die "除 rel add 合法改动的 ${CARD_A}.md 外，权威文件字节被动了 ${UNEXPECTED} 处"; }
ok "${IDX_REL}/ 文件名 = ${FILES_NOW}（⊆ 白名单）；git 零污染、eg index 恒 0 次 commit、权威零改动"

# ---------------------------------------------------------------- 8. 整目录删掉能复原
step "rm -rf .index/ 后 eg index build 复原：三键水位线复现（索引恒为可重建派生）"
rm -rf "${VAULT}/${IDX_REL}"
[ "$(eg_code index status --json)" = "0" ] || die "删掉之后 status 应退 0"
[ "$(jstr "${WORK}/out.txt" health)" = "missing" ] || die "删掉之后 health 应为 missing"
[ "$(eg_code index build --json)" = "0" ] || die "复原构建应退 0"
cp "${WORK}/out.txt" "${WORK}/rebuilt.json"
[ "$(jstr "${WORK}/rebuilt.json" action)" = "built" ] || die "复原后的 action 应为 built"
# 与第 4 节同一个观测量逐字比对：复原不是「又建了一份新 Schema」，而是把**同一版**
# Schema 的库重新算出来（真值单点仍是二进制里的 IndexSchemaVersion，见第 4 节注释）。
[ "$(jnum "${WORK}/rebuilt.json" schema_version)" = "${SCHEMA_V}" ] ||
  die "复原后的 schema_version = $(jnum "${WORK}/rebuilt.json" schema_version)，期望与首建逐字相同（${SCHEMA_V}）"
[ "$(jstr "${WORK}/rebuilt.json" head)" = "${NEW_HEAD}" ] ||
  die "复原后的 head 应是当前 git HEAD（水位线由权威 Markdown 复算）"
[ "$(jnum "${WORK}/rebuilt.json" cards)" = "3" ] || die "复原后的 cards 应仍是 3"
# 同一份权威 + 同一个 head → files_hash 必须可复算：再删再建一次，两次逐字相同。
H1="$(jstr "${WORK}/rebuilt.json" files_hash)"
rm -rf "${VAULT}/${IDX_REL}"
[ "$(eg_code index build --json)" = "0" ] || die "第二次复原构建应退 0"
H2="$(jstr "${WORK}/out.txt" files_hash)"
[ "${H1}" = "${H2}" ] || die "两次全量构建的 files_hash 不同（${H1} vs ${H2}）：构建必须确定"
ok "删干净后一条命令复原，且两次构建的 files_hash 逐字相同：${H1}"

# ---------------------------------------------------------------- 9. 已做面 / 未做面
step "已做面：index sync / status --strict / bench 命令面在册；未做面：index 子命令面未扩张、输出恒无 Q5"
[ "$(eg_code index sync --json)" = "0" ] || die "eg index sync 已落地（T-…-066），必须退 0"
[ "$(jstr "${WORK}/out.txt" action)" = "noop" ] ||
  die "刚建完就 sync 应是 noop（实为 $(jstr "${WORK}/out.txt" action)）"
no_stale_codes "${WORK}/out.txt" "noop sync 输出"
[ "$(eg_code index status --strict --json)" = "0" ] || die "--strict 已落地（T-…-066），必须退 0"
grep -Fq '"strict":true' "${WORK}/out.txt" || die "--strict 必须在 data.index 里如实留痕"
[ "$(jstr "${WORK}/out.txt" freshness)" = "fresh" ] || die "全量重算下也应是 fresh"
no_stale_codes "${WORK}/out.txt" "strict status 输出"
[ "$(eg_code index --json)" = "1" ] || die "缺子命令必须退 1"
[ "$(eg_code index vacuum --json)" = "1" ] || die "未知子命令必须退 1"
[ "$(eg_code index build --strict --json)" = "1" ] || die "--strict 只对 status 有语义：挂 build 必须退 1"
[ "$(eg_code index sync --strict --json)" = "1" ] || die "--strict 挂 sync 必须退 1"
[ "$(eg_code index status extra --json)" = "1" ] || die "多余位置参数必须退 1"
[ "$(eg_code index status --limit 5 --json)" = "1" ] ||
  die "--limit 对 index status 无语义（分页只属查询域），必须退 1"
eg --help >"${WORK}/help.txt" 2>&1  # 退出码不吞：--help 合同退 0，非 0 由 set -e 直接失败
grep -qE '^  index( |$)' "${WORK}/help.txt" || die "--help 命令区缺 index"
grep -Fq 'index build|rebuild|status|sync' "${WORK}/help.txt" ||
  die "--help 必须如实列出四个子命令（build|rebuild|status|sync）"
# ── 【阶段化重钉：bench】────────────────────────────────────────────────
# T-…-065 期这两格断言的是「`eg bench` 缺席、`--help` 不得出现 bench」，依据是
# 「bench 属 T-…-068，本阶段尚未交付」。T-…-068 已交付 bench，**那条依据不再成立**，
# 若继续断言缺席就是拿旧事实判当期红。故按当期事实重钉，且**只改判据形态、不降强度**：
#   · 命令面：`--help` 必须**正面**列出 bench（缺席即红——防「命令被悄悄下掉」）；
#   · 参数面：bench 的未知参数仍必须退 1（参数面封闭没有因新命令落地而放宽）；
#   · 边界面：index 子命令面**不因 bench 落地而扩张**（仍恰 build|rebuild|status|sync 四个），
#     且 bench 属查询域、不得往 `.index/` 白名单外写东西（下面的 porcelain 与白名单自查兜住）。
# bench 的采样语义与五条 p95 门槛由 m5_bench_p95.sh 专管，这里不重复跑采样（避免重复覆盖与耗时）。
grep -qE '^  bench' "${WORK}/help.txt" || die "--help 命令区缺 bench（T-…-068 已交付，缺席即红）"
[ "$(eg_code bench --no-such-flag --json)" = "1" ] || die "bench 未知参数必须退 1（参数面仍封闭）"
N_IDX_SUB="$( { grep -oE 'index build\|rebuild\|status\|sync' "${WORK}/help.txt" || true; } | wc -l | tr -d ' ')"
[ "${N_IDX_SUB}" = "1" ] || die "--help 里 index 子命令面出现 ${N_IDX_SUB} 次登记，期望恰 1（子命令面未扩张）"
# 参数非法一律零写入：上面几条都跑完之后，.index/ 仍是白名单内容、工作区仍不脏。
[ "$(porcelain)" = "${BASE_STATUS}" ] || die "参数非法路径污染了工作区"
ok "sync / --strict 退 0、bench 命令面在册且参数面封闭；错挂 --strict / --limit、未知子命令一律退 1 且零写入；Q5 全程零出现"

# ---------------------------------------------------------------- 10. 仓库工作区零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] index_build.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
