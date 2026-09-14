#!/usr/bin/env bash
# 三条读路径在**索引健康**时的端到端脚本（M5 · T-evergreen.s1_main_flow-158614-067）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §1.1 P-2（Markdown 是唯一权威来源；索引只是派生候选集）
#   §5.2（后端选择单点：健康走索引，不可用 / 陈旧走扫描）
#   §6.1（索引问题不阻断读、不改退出码）/ §6.2（W22 / W23 / W24 + Q5 的分配与同现）
#   §6.4（`data` 键集合不扩张：走了哪条后端**不进输出**，只经诊断承载降级事实）
#   §7.4（索引只出候选集：可见性过滤、`[失效]` 标记、Q1–Q4 一律复用既有单点）
# 以及 M2 查询合同 §3.1（`rel data` 恰五键）、M4 可见性四象限（deprecated 默认隐藏 + Q4）。
#
# 本脚本只守**健康索引面**（降级面走 m5_degrade_fallback.sh，两本互不覆盖）：
#   ① 索引 healthy 时三条读命令（`search` / `card show` / `rel`）全退 0，且输出里
#      **零** W22 / W23 / W24 / Q5 —— 降级必留痕（§6.2），零降级痕迹即「取数走的是索引后端」；
#   ② 索引在位不改既有输出契约：`rel data` 恰五键且逐字有序、`card show data` 键序列
#      与「索引删掉后」逐字相同（键集合不扩张，§6.4），两者都不出现 index / backend 类新键；
#   ③ 索引只出候选集：坏文件照旧产 Q1 + Q3（不因为走索引就静默）、deprecated 端点默认隐藏
#      且恰一条 Q4、`--include-deprecated` 下零 Q4 —— 过滤与标记复用既有单点，没重写一套；
#   ④ 读不写索引：三条读命令跑完，`.index/` 的文件清单与 sha256 逐字不变，
#      目录文件名恒在白名单三值内（读路径不许顺手建 / 改 / 修索引）；
#   ⑤ 权威零改动、零 commit、vault 干净：只读就是只读；
#   ⑥ `skipped[].kind` 仍**恰 2 值**：封闭取值域的两处单一真源各自逐字未漂移
#      （读路径不产写回执，故此格用单源反证，不伪造一次写入）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；无 jq / sqlite3 等外部依赖
#   （只用 bash / coreutils / awk / git / go）；**一切写与构建只发生在 mktemp -d 沙箱内**，
#   真实仓库工作区一个字节都不碰（脚本末尾自查 git status）；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/read_path_index.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-read-index.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

IDX_ABS="${VAULT}/.index"
ALLOWED='eg.db,eg.db-shm,eg.db-wal'

CARD_A='k-20261201-attention'   # active，正向 supports → C（deprecated 端点）
CARD_B='k-20261201-rnn'         # active，被 A 之外的卡引用，用来验反向查询
CARD_C='k-20261201-legacy'      # deprecated：默认视图应隐藏它并产恰一条 Q4
CARD_X='k-20261201-broken'      # 坏文件：健康索引下照旧 Q1 + Q3

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

# ---- JSON 取值（信封是单行紧凑 JSON；括号配平扫描，字符串里的括号不算） ----
JAWK='
function extract(s, key,   i, n, d, instr, esc, c, start) {
  i = index(s, key); if (i == 0) return ""
  i += length(key); start = i; d = 0; instr = 0; esc = 0; n = length(s)
  for (; i <= n; i++) {
    c = substr(s, i, 1)
    if (instr) {
      if (esc) esc = 0; else if (c == "\\") esc = 1; else if (c == "\"") instr = 0
      continue
    }
    if (c == "\"") { instr = 1; continue }
    if (c == "{" || c == "[") d++
    else if (c == "}" || c == "]") { d--; if (d == 0) return substr(s, start, i - start + 1) }
    else if (d == 0 && c == ",") return substr(s, start, i - start)
  }
  return substr(s, start)
}
function keys(s,   i, j, n, d, instr, esc, c, cur, out) {
  n = length(s); d = 0; instr = 0; esc = 0; out = ""; cur = ""
  for (i = 1; i <= n; i++) {
    c = substr(s, i, 1)
    if (instr) {
      if (esc) { esc = 0; cur = cur c; continue }
      if (c == "\\") { esc = 1; cur = cur c; continue }
      if (c == "\"") {
        instr = 0
        j = i + 1
        while (substr(s, j, 1) == " ") j++
        if (d == 1 && substr(s, j, 1) == ":") out = out (out == "" ? "" : ",") cur
        cur = ""
        continue
      }
      cur = cur c; continue
    }
    if (c == "\"") { instr = 1; cur = ""; continue }
    if (c == "{" || c == "[") d++
    else if (c == "}" || c == "]") d--
  }
  return out
}
'
# jdata <文件>：顶层 data 那一整格的紧凑原文。
jdata() { awk -v key='"data":' "${JAWK}"'{ printf "%s", extract($0, key) }' "$1"; }
# jkeys <文件>：data 的**顶层键序列**（逗号分隔、原序）。
jkeys() { awk -v key='"data":' "${JAWK}"'{ printf "%s", keys(extract($0, key)) }' "$1"; }
# jcodes <文件>：warnings[] 的 code 序列（原序、逗号分隔）。
jcodes() {
  awk -v key='"warnings":' "${JAWK}"'{ printf "%s", extract($0, key) }' "$1" |
    grep -o '"code":"[^"]*"' | sed 's/"code":"//; s/"$//' | paste -sd, -
}
# ccount <文件> <码>：某个诊断码在 warnings[] 里的条数。
ccount() {
  local n
  n="$(awk -v key='"warnings":' "${JAWK}"'{ printf "%s", extract($0, key) }' "$1" |
    grep -o "\"code\":\"$2\"" | wc -l | tr -d ' ')"
  echo "${n}"
}
# assert_no_degrade_codes <文件> <说明>：健康索引下 W22 / W23 / W24 / Q5 一律零条。
assert_no_degrade_codes() {
  local f="$1" what="$2" code
  for code in W22 W23 W24 Q5; do
    [ "$(ccount "${f}" "${code}")" = "0" ] ||
      { cat "${f}"; die "${what}：索引 healthy 时不得出现 ${code}（降级必留痕，有痕即没走索引）"; }
  done
}
idx_files() { ( cd "${IDX_ABS}" && ls -A | sort | paste -sd, - ); }
# assert_index_family <说明>：C2a·M6 现态重钉（§16.3 runtime-reserved + §16.4 现态重钉授权；
#   保留历史事实 + 新增现态双侧锁，非放宽）。历史事实一格不放宽：**派生 DB 家族**恒恰 ${ALLOWED}
#   三值（case 的 * 分支原样比对）。M6 现态：`.index/` 变为「派生物 + 运行时证据」混居目录，写命令
#   锁层按 A-53 于其下并存 runtime-reserved 的 run.lock（普通文件）/ txn（目录）；二者不进 DB 家族
#   比对，但额外条目**必须**恰是这两项而非任意杂项（case 显式枚举 = 双侧锁）。
assert_index_family() {
  local what="$1" f db_family=""
  for f in $(idx_files | tr ',' ' '); do
    case "${f}" in
      run.lock|txn) ;;  # M6 runtime-reserved（§16.3）：锁文件 / 事务日志目录，非派生物
      *) db_family="${db_family:+${db_family},}${f}" ;;
    esac
  done
  [ "${db_family}" = "${ALLOWED}" ] ||
    die "${what}：派生 DB 家族 = ${db_family:-（空）}，期望恰 ${ALLOWED}（另允许 M6 runtime-reserved run.lock/txn）"
}
idx_snapshot() {
  ( cd "${VAULT}" && find .index \( -type f -o -type d \) | sort
    cd "${VAULT}" && { find .index -type f -exec sha256sum {} + 2>/dev/null || true; } | sort )
}
authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals reviews -type f 2>/dev/null || true; } |
    sort | xargs -r sha256sum )
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 + 语料
step "构建 eg（CGO_ENABLED=0）+ seed 四个文件（active / active / deprecated / 坏文件）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
KDIR="${VAULT}/domains/ai-infra/knowledge"
mkdir -p "${KDIR}"
REASON_AC='新卡结论支持既有卡的适用范围判断：两者在同一前提下互为佐证，理由字段写足以通过写前校验的长度。'
REASON_AB='新卡结论限定既有卡的适用前提：算力增长不必然带来知识规模增长，理由字段写足以通过写前校验的长度。'
{
  printf '%s\n' '---' "id: ${CARD_A}" 'status: active' "created_at: '2026-12-01'" \
    "updated_at: '2026-12-01T10:00:00+08:00'" 'title: 注意力机制的计算代价' 'tags: [ai, method]' \
    'sources: []' 'relations:' "  - type: supports" "    target: ${CARD_C}" \
    "    reason: ${REASON_AC}" "  - type: limits" "    target: ${CARD_B}" \
    "    reason: ${REASON_AB}" '---' '' '## 知识内容' '' '正文占位：注意力机制的计算代价随序列长度平方增长。' ''
} >"${KDIR}/${CARD_A}.md"
{
  printf '%s\n' '---' "id: ${CARD_B}" 'status: active' "created_at: '2026-12-01'" \
    "updated_at: '2026-12-01T10:00:00+08:00'" 'title: 循环网络的长序列衰减' 'tags: [ai]' \
    'sources: []' '---' '' '## 知识内容' '' '正文占位：循环网络在长序列上的梯度衰减问题。' ''
} >"${KDIR}/${CARD_B}.md"
{
  printf '%s\n' '---' "id: ${CARD_C}" 'status: deprecated' "created_at: '2026-11-01'" \
    "updated_at: '2026-12-01T10:00:00+08:00'" 'title: 已失效的旧结论' 'sources: []' '---' '' \
    '## 知识内容' '' '正文占位：这条结论已经失效。' ''
} >"${KDIR}/${CARD_C}.md"
# 坏文件：frontmatter 缺 id + 状态非法，扫不动就必须说出来（Q1），不因走索引而静默。
printf '%s\n' '---' 'id:' 'status: ？？？' '---' '坏文件正文。' >"${KDIR}/${CARD_X}.md"
gitv add -A && gitv commit -q -m "seed: m5 读路径语料（含 deprecated 端点与坏文件）"
[ "$(gitv status --porcelain)" = "" ] || die "前置条件失败：vault 应干净"
BASE_COMMITS="$(commits)"
authority_sha >"${WORK}/authority.before.txt"
[ -s "${WORK}/authority.before.txt" ] || die "权威快照为空：语料没建起来"
ok "二进制与语料就绪（3 张可解析卡 + 1 个坏文件；commit=${BASE_COMMITS}）"

# ---------------------------------------------------------------- 2. 建索引 → healthy
step "eg index build → 索引 healthy / fresh（三条读路径的前提，不是前置依赖）"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "index build 必须退 0"; }
cp "${WORK}/out.txt" "${WORK}/build.json"
grep -Fq '"health":"healthy"' "${WORK}/build.json" || { cat "${WORK}/build.json"; die "建完应 healthy"; }
[ "$(eg_code index status --json)" = "0" ] || die "index status 必须退 0"
cp "${WORK}/out.txt" "${WORK}/status.json"
grep -Fq '"health":"healthy"' "${WORK}/status.json" || die "status.health 应为 healthy"
grep -Fq '"freshness":"fresh"' "${WORK}/status.json" ||
  { cat "${WORK}/status.json"; die "status.freshness 应为 fresh（坏文件不算陈旧）"; }
assert_index_family ".index/ 派生 DB 家族校验"
idx_snapshot >"${WORK}/idx.before.txt"
ok "索引 healthy + fresh；.index/ 派生 DB 家族恰在白名单三值内（另允许 M6 runtime-reserved run.lock/txn）"

# ---------------------------------------------------------------- 3. 三条读路径全退 0 且零降级痕迹
step "健康索引下 search / card show / rel 全退 0，且零 W22 / W23 / W24 / Q5（⇒ 走索引后端）"
[ "$(eg_code search 正文 --json)" = "0" ] || { cat "${WORK}/err.txt"; die "search 必须退 0"; }
cp "${WORK}/out.txt" "${WORK}/search.idx.json"
[ "$(eg_code card show "${CARD_A}" --json)" = "0" ] || { cat "${WORK}/err.txt"; die "card show 必须退 0"; }
cp "${WORK}/out.txt" "${WORK}/card.idx.json"
[ "$(eg_code rel "${CARD_B}" --json)" = "0" ] || { cat "${WORK}/err.txt"; die "rel 必须退 0"; }
cp "${WORK}/out.txt" "${WORK}/rel.idx.json"
assert_no_degrade_codes "${WORK}/search.idx.json" "search"
assert_no_degrade_codes "${WORK}/card.idx.json" "card show"
assert_no_degrade_codes "${WORK}/rel.idx.json" "rel"
for f in search card rel; do
  grep -Fq '"exit_code":0' "${WORK}/${f}.idx.json" || die "${f} 的信封 exit_code 应为 0"
  grep -Fq '"status":"completed"' "${WORK}/${f}.idx.json" || die "${f} 的信封 status 应为 completed"
done
ok "三条读路径退 0；诊断码为 search=[$(jcodes "${WORK}/search.idx.json")] / card=[$(jcodes "${WORK}/card.idx.json")] / rel=[$(jcodes "${WORK}/rel.idx.json")]，其中零降级码"

# ---------------------------------------------------------------- 4. 输出契约不扩张
step "rel data 恰五键逐字有序；card show / search 的 data 键集合与「索引删掉后」逐字相同"
REL_KEYS="$(jkeys "${WORK}/rel.idx.json")"
[ "${REL_KEYS}" = "id,relations_out,relations_in,scanned_files,skipped_files" ] ||
  die "rel data 键序列 = ${REL_KEYS}，期望恰五键 id,relations_out,relations_in,scanned_files,skipped_files"
CARD_KEYS="$(jkeys "${WORK}/card.idx.json")"
SEARCH_KEYS="$(jkeys "${WORK}/search.idx.json")"
for keys in "${REL_KEYS}" "${CARD_KEYS}" "${SEARCH_KEYS}"; do
  case ",${keys}," in
    *,index,* | *,backend,* | *,degraded,* | *,index_health,*)
      die "data 出现索引类新键（§6.4 键集合不扩张）：${keys}" ;;
  esac
done
# 同一 vault、同一命令，把索引挪走再跑一次：键序列必须逐字相同（形状与后端无关）。
mv "${IDX_ABS}" "${WORK}/idx.parked"
[ "$(eg_code search 正文 --json)" = "0" ] || die "无索引时 search 仍须退 0"
cp "${WORK}/out.txt" "${WORK}/search.scan.json"
[ "$(eg_code card show "${CARD_A}" --json)" = "0" ] || die "无索引时 card show 仍须退 0"
cp "${WORK}/out.txt" "${WORK}/card.scan.json"
[ "$(eg_code rel "${CARD_B}" --json)" = "0" ] || die "无索引时 rel 仍须退 0"
cp "${WORK}/out.txt" "${WORK}/rel.scan.json"
mv "${WORK}/idx.parked" "${IDX_ABS}"
[ "$(jkeys "${WORK}/search.scan.json")" = "${SEARCH_KEYS}" ] || die "search data 键集合随后端漂移"
[ "$(jkeys "${WORK}/card.scan.json")" = "${CARD_KEYS}" ] || die "card show data 键集合随后端漂移"
[ "$(jkeys "${WORK}/rel.scan.json")" = "${REL_KEYS}" ] || die "rel data 键集合随后端漂移"
ok "rel 恰五键；card show（${CARD_KEYS}）与 search 键集合两后端逐字一致，且零索引类新键"

# ---------------------------------------------------------------- 5. 索引只出候选集：Q1 / Q3 / Q4 照旧
step "索引只出候选集：坏文件照旧 Q1 + Q3；deprecated 端点默认隐藏且恰一条 Q4"
[ "$(ccount "${WORK}/search.idx.json" Q1)" -ge 1 ] ||
  { cat "${WORK}/search.idx.json"; die "健康索引下坏文件仍须产 Q1（不得因走索引而静默）"; }
[ "$(ccount "${WORK}/search.idx.json" Q3)" = "1" ] || die "有 Q1 时 search 应恰一条 Q3 汇总"
grep -Fq '"skipped_files":1' "${WORK}/search.idx.json" ||
  { cat "${WORK}/search.idx.json"; die "search 的 skipped_files 应为 1（坏文件计入，计数单源）"; }
[ "$(ccount "${WORK}/card.idx.json" Q4)" = "1" ] ||
  { cat "${WORK}/card.idx.json"; die "card show ${CARD_A} 应恰一条 Q4（隐藏 deprecated 端点 ${CARD_C}）"; }
CARD_A_DATA="$(jdata "${WORK}/card.idx.json")"
case "${CARD_A_DATA}" in
  *"${CARD_C}"*) die "默认视图不得暴露 deprecated 端点 ${CARD_C}" ;;
esac
[ "$(eg_code card show "${CARD_A}" --include-deprecated --json)" = "0" ] || die "--include-deprecated 必须退 0"
cp "${WORK}/out.txt" "${WORK}/card.inc.json"
[ "$(ccount "${WORK}/card.inc.json" Q4)" = "0" ] || die "--include-deprecated 下不得产 Q4"
assert_no_degrade_codes "${WORK}/card.inc.json" "card show --include-deprecated"
jdata "${WORK}/card.inc.json" | grep -Fq "${CARD_C}" ||
  die "--include-deprecated 应显示出 deprecated 端点 ${CARD_C}"
jdata "${WORK}/rel.idx.json" | grep -Fq "${CARD_A}" ||
  { cat "${WORK}/rel.idx.json"; die "rel ${CARD_B} 的反向关系应含来源卡 ${CARD_A}"; }
ok "Q1=$(ccount "${WORK}/search.idx.json" Q1) / Q3=1 / Q4=1 照旧成立；过滤与标记复用既有单点"

# ---------------------------------------------------------------- 6. 读不写索引、读不动权威
step "只读就是只读：.index/ 逐字不变、权威零改动、零 commit、vault 干净"
idx_snapshot >"${WORK}/idx.after.txt"
diff -u "${WORK}/idx.before.txt" "${WORK}/idx.after.txt" >/dev/null ||
  { diff -u "${WORK}/idx.before.txt" "${WORK}/idx.after.txt" || true
    die "读路径改动了 .index/（读不许建 / 改 / 修索引，那是 eg index 的事）"; }
assert_index_family "读完 .index/ 派生 DB 家族校验"
authority_sha >"${WORK}/authority.after.txt"
diff -u "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" >/dev/null ||
  die "读路径改动了权威 Markdown"
[ "$(commits)" = "${BASE_COMMITS}" ] || die "读命令产生了 commit（$(commits) ≠ ${BASE_COMMITS}）"
[ "$(gitv status --porcelain)" = "" ] || { gitv status --porcelain; die "读完 vault 应干净"; }
ok "索引与权威逐字不变；commit 仍为 ${BASE_COMMITS}；vault 干净"

# ---------------------------------------------------------------- 7. skipped[].kind 仍恰 2 值
step "skipped[].kind 仍恰 2 值：两处封闭取值域的单一真源逐字未漂移"
KIND_SRC="${REPO_ROOT}/internal/index/schema.go"
[ "$(grep -c 'return \[\]string{"file_changed", "user_block_unsafe"}' "${KIND_SRC}")" = "1" ] ||
  die "internal/index/schema.go 的 SkippedKinds 取值域漂移（必须恰 2 值且恰一处）"
[ "$(grep -rc 'user_block_unsafe' "${REPO_ROOT}/internal/store/receipt.go")" -ge 1 ] ||
  die "internal/store/receipt.go 的回执 kind 单源缺失"
[ "$(grep -rn 'os.Exit(5)' "${REPO_ROOT}/internal" | wc -l | tr -d ' ')" = "0" ] ||
  die "退出码面扩张：M5 不得出现 os.Exit(5)（属 M6）"
ok "kind 恰 2 值（file_changed / user_block_unsafe）；全库零 os.Exit(5)"

# ---------------------------------------------------------------- 8. 工作区零污染
step "工作区零污染：脚本写操作只发生在 mktemp -d 内"
[ "$(git -C "${REPO_ROOT}" status --porcelain | sort)" = "${BEFORE_REPO_STATUS}" ] ||
  die "evergreen 工作区被脚本改动"
ok "evergreen 工作区与运行前逐字相同"

printf '\n=== read_path_index.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
