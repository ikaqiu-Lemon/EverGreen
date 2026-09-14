#!/usr/bin/env bash
# 索引三形态降级的端到端脚本（M5 · T-evergreen.s1_main_flow-158614-067）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §1.1 P-2（Markdown 是唯一权威来源，索引可丢弃）/ §5.2（不可用与陈旧一律回落全量扫描）
#   §6.1（索引问题不阻断读、**不新增以索引为原因的非 0 退出码**）
#   §6.2 / §6.3（missing → W23、corrupt → W24、stale → W22，各与**恰一条** Q5 同现）
#   §6.4（`data` 键集合不扩张：降级事实只经诊断承载）
# 以及 milestone M-005 完成判据 8「索引可丢弃 / 降级不失败」。
#
# 本脚本只守**降级面**（健康面走 m5_read_path_index.sh，两本互不覆盖）：
#   ① 三形态各自被如实归因：`rm -rf .index/` → W23；截断 `eg.db` → W24；
#      `git commit --allow-empty` 前移 HEAD → W22（权威字节一字未改，故等价性可逐字比对：
#      水位线 = `(head, files_hash)`，head 变即陈旧，合同 §5.1 / A-44）；
#   ①' A-44 反证：**只改 mtime**（内容与 HEAD 都没变）**不算**变更 —— 仍走索引、零 W22、零 Q5。
#      `mtime` 只作快路径过滤，未命中就回权威重算 `content_hash` 再判定，绝不以 mtime 作结论；
#   ② 每一形态下 `search` / `card show` / `rel` **全退 0**，且 `--json` 的 `.data` 与
#      索引在位时**逐字相等** —— 判据形态就是脚本内的 `diff` 空差异断言（合同 §5.2 的等价性）；
#   ③ 每一形态下每条读命令各多出**恰一条** W22|W23|W24 + **恰一条** Q5，且**只**多这两条：
#      把这两条剔掉后，诊断码序列与索引在位时逐字相同（Q1 / Q3 / Q4 一条不多一条不少）；
#   ④ 健康态零 Q5：`eg index build` 修回来之后 Q5 立刻消失、`.data` 仍逐字相等（闭环可逆）；
#   ⑤ 坏 / 缺 / 旧索引下 `eg index status` 也照样退 0（体检是诊断不是失败）；
#   ⑥ 权威零改动、零 commit、vault 干净；全库零 `os.Exit(5)`（退出码面不扩张，那是 M6）。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；无 jq / sqlite3 等外部依赖
#   （只用 bash / coreutils / awk / git / go）；**一切写与构建只发生在 mktemp -d 沙箱内**，
#   真实仓库工作区一个字节都不碰（脚本末尾自查 git status）；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/degrade_fallback.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-degrade.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

IDX_ABS="${VAULT}/.index"
DB_ABS="${IDX_ABS}/eg.db"

CARD_A='k-20261201-attention'   # active，正向 supports → C（deprecated 端点，产 Q4）
CARD_B='k-20261201-rnn'         # active，被 A 引用，用来验反向查询
CARD_C='k-20261201-legacy'      # deprecated
CARD_X='k-20261201-broken'      # 坏文件（Q1 + Q3 全程在场，降级不许吞掉它）

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
'
jdata()  { awk -v key='"data":'     "${JAWK}"'{ printf "%s", extract($0, key) }' "$1"; }
jwarn()  { awk -v key='"warnings":' "${JAWK}"'{ printf "%s", extract($0, key) }' "$1"; }
jcodes() { jwarn "$1" | grep -o '"code":"[^"]*"' | sed 's/"code":"//; s/"$//'; }
ccount() { local n; n="$(jcodes "$1" | grep -cx "$2" || true)"; echo "${n}"; }

# read_all <标签>：跑三条读命令，把信封与 .data 分别落盘；每条都必须退 0。
read_all() {
  local tag="$1"
  [ "$(eg_code search 正文 --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "${tag}：search 必须退 0（索引问题不许变成命令失败）"; }
  cp "${WORK}/out.txt" "${WORK}/search.${tag}.json"
  [ "$(eg_code card show "${CARD_A}" --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "${tag}：card show 必须退 0"; }
  cp "${WORK}/out.txt" "${WORK}/card.${tag}.json"
  [ "$(eg_code rel "${CARD_B}" --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "${tag}：rel 必须退 0"; }
  cp "${WORK}/out.txt" "${WORK}/rel.${tag}.json"
  local cmd
  for cmd in search card rel; do
    jdata "${WORK}/${cmd}.${tag}.json" >"${WORK}/${cmd}.${tag}.data"
    [ -s "${WORK}/${cmd}.${tag}.data" ] || die "${tag}：${cmd} 的 data 取空了"
    grep -Fq '"exit_code":0' "${WORK}/${cmd}.${tag}.json" || die "${tag}：${cmd} 信封 exit_code 应为 0"
    grep -Fq '"status":"completed"' "${WORK}/${cmd}.${tag}.json" || die "${tag}：${cmd} 信封 status 应为 completed"
  done
}

# assert_degraded <标签> <期望原因码>：三条读命令的 data 与基线逐字相等，
# 且各多出恰一条原因码 + 恰一条 Q5，剔掉这两条后诊断码序列与基线逐字相同。
assert_degraded() {
  local tag="$1" want="$2" cmd other
  for cmd in search card rel; do
    diff -u "${WORK}/${cmd}.base.data" "${WORK}/${cmd}.${tag}.data" ||
      die "${tag}：${cmd} 降级后的 .data 与索引在位时不逐字相等（合同 §5.2 等价性）"
    [ "$(ccount "${WORK}/${cmd}.${tag}.json" "${want}")" = "1" ] ||
      { jcodes "${WORK}/${cmd}.${tag}.json"; die "${tag}：${cmd} 应恰一条 ${want}"; }
    [ "$(ccount "${WORK}/${cmd}.${tag}.json" Q5)" = "1" ] ||
      { jcodes "${WORK}/${cmd}.${tag}.json"; die "${tag}：${cmd} 应恰一条 Q5（有原因码必有 Q5）"; }
    for other in W22 W23 W24; do
      [ "${other}" = "${want}" ] && continue
      [ "$(ccount "${WORK}/${cmd}.${tag}.json" "${other}")" = "0" ] ||
        die "${tag}：${cmd} 归因不唯一，出现了 ${other}"
    done
    # 只多这两条：剔掉原因码与 Q5 后，码序列与基线逐字相同（Q1 / Q3 / Q4 一条不差）。
    jcodes "${WORK}/${cmd}.${tag}.json" | grep -vx -e "${want}" -e Q5 >"${WORK}/codes.${tag}.${cmd}" || true
    jcodes "${WORK}/${cmd}.base.json" >"${WORK}/codes.base.${cmd}"
    diff -u "${WORK}/codes.base.${cmd}" "${WORK}/codes.${tag}.${cmd}" ||
      die "${tag}：${cmd} 除 ${want} + Q5 之外的诊断码发生了漂移"
    printf '    · %s / %s → %s + Q5（.data 空差异；其余诊断码不变）\n' "${tag}" "${cmd}" "${want}"
  done
  [ "$(eg_code index status --json)" = "0" ] ||
    { cat "${WORK}/err.txt"; die "${tag}：index status 也必须退 0（体检是诊断不是失败）"; }
}

authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals reviews -type f 2>/dev/null || true; } |
    sort | xargs -r sha256sum )
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 + 语料 + 基线
step "构建 eg（CGO_ENABLED=0）+ seed 语料 + 建索引 + 取三条读路径基线（索引在位）"
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
    'sources: []' 'relations:' '  - type: supports' "    target: ${CARD_C}" \
    "    reason: ${REASON_AC}" '  - type: limits' "    target: ${CARD_B}" \
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
printf '%s\n' '---' 'id:' 'status: ？？？' '---' '坏文件正文。' >"${KDIR}/${CARD_X}.md"
gitv add -A && gitv commit -q -m "seed: m5 降级语料（含 deprecated 端点与坏文件）"
[ "$(gitv status --porcelain)" = "" ] || die "前置条件失败：vault 应干净"
BASE_COMMITS="$(commits)"
authority_sha >"${WORK}/authority.before.txt"
[ -s "${WORK}/authority.before.txt" ] || die "权威快照为空：语料没建起来"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "index build 必须退 0"; }
grep -Fq '"health":"healthy"' "${WORK}/out.txt" || die "建完应 healthy"
read_all base
for cmd in search card rel; do
  for code in W22 W23 W24 Q5; do
    [ "$(ccount "${WORK}/${cmd}.base.json" "${code}")" = "0" ] ||
      { jcodes "${WORK}/${cmd}.base.json"; die "索引在位的基线不得有 ${code}"; }
  done
done
ok "基线就绪（索引 healthy、三条读路径零降级码；诊断码 search=[$(jcodes "${WORK}/search.base.json" | paste -sd, -)]）"

# ---------------------------------------------------------------- 2. missing → W23
step "形态一：rm -rf .index/ → 三条读命令退 0、.data 空差异、各恰一条 W23 + Q5"
rm -rf "${IDX_ABS}"
[ ! -e "${IDX_ABS}" ] || die ".index/ 应已删除"
read_all missing
assert_degraded missing W23
ok "索引可丢弃：删掉整个 .index/ 后三条读路径照常出结果（W23 + Q5，逐字等价）"

# ---------------------------------------------------------------- 3. 修回来 → 健康态零 Q5
step "闭环可逆：eg index build 修回来后 Q5 立刻消失，.data 仍与基线逐字相等"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "重建必须退 0"; }
grep -Fq '"health":"healthy"' "${WORK}/out.txt" || die "重建完应 healthy"
read_all healed
for cmd in search card rel; do
  diff -u "${WORK}/${cmd}.base.data" "${WORK}/${cmd}.healed.data" ||
    die "重建后 ${cmd} 的 .data 与基线不逐字相等"
  [ "$(ccount "${WORK}/${cmd}.healed.json" Q5)" = "0" ] ||
    { jcodes "${WORK}/${cmd}.healed.json"; die "健康索引下 ${cmd} 不得产 Q5"; }
done
ok "重建即回到健康态：零 Q5、.data 与基线逐字相等"

# ---------------------------------------------------------------- 4. corrupt → W24
step "形态二：截断 eg.db（真坏字节）→ 三条读命令退 0、.data 空差异、各恰一条 W24 + Q5"
[ -f "${DB_ABS}" ] || die "eg.db 应在位"
head -c 32 "${DB_ABS}" >"${WORK}/trunc.db" && mv "${WORK}/trunc.db" "${DB_ABS}"
read_all corrupt
assert_degraded corrupt W24
ok "坏索引不是死局：三条读路径照常出结果（W24 + Q5，逐字等价）"

# ---------------------------------------------------------------- 5. 只改 mtime → 仍新鲜（A-44）
step "反证（A-44）：只改 mtime（内容一字不改）**不算**变更 → 仍新鲜、零 W22、零 Q5"
[ "$(eg_code index build --json)" = "0" ] || die "修复必须退 0"
grep -Fq '"health":"healthy"' "${WORK}/out.txt" || die "修完应 healthy"
touch -m -d '2027-01-01T00:00:00' "${KDIR}/${CARD_B}.md"
[ "$(eg_code index status --json)" = "0" ] || die "status 必须退 0"
grep -Fq '"freshness":"fresh"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "只改 mtime 不得判陈旧（A-44：mtime 只作快路径过滤，结论恒以 content_hash 为准）"; }
read_all touched
for cmd in search card rel; do
  diff -u "${WORK}/${cmd}.base.data" "${WORK}/${cmd}.touched.data" ||
    die "只改 mtime 后 ${cmd} 的 .data 与基线不逐字相等"
  for code in W22 W23 W24 Q5; do
    [ "$(ccount "${WORK}/${cmd}.touched.json" "${code}")" = "0" ] ||
      { jcodes "${WORK}/${cmd}.touched.json"; die "只改 mtime 不得产 ${code}（${cmd}）"; }
  done
done
ok "mtime 不是判据：碰过 mtime 的库仍走索引、零降级留痕（.data 与基线逐字相等）"

# ---------------------------------------------------------------- 6. stale → W22
step "形态三：HEAD 前移（权威字节一字不改）→ 水位线对不上判陈旧，各恰一条 W22 + Q5"
gitv commit -q --allow-empty -m "chore: 前移 HEAD（不改任何权威文件）"
BASE_COMMITS="$(commits)"   # 基线随之更新：这一条 commit 是脚本造的，不是读命令产生的
[ "$(eg_code index status --json)" = "0" ] || die "陈旧态 status 必须退 0"
grep -Fq '"freshness":"stale"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "HEAD 变了之后 freshness 应为 stale（水位线含 head）"; }
grep -Fq '"health":"healthy"' "${WORK}/out.txt" ||
  die "陈旧不等于损坏：health 仍应为 healthy"
read_all stale
assert_degraded stale W22
ok "陈旧一律回落扫描：权威字节未变、.data 与基线逐字相等（W22 + Q5）"

# ---------------------------------------------------------------- 7. 权威不动 / 退出码面不扩张
step "权威零改动、零 commit、vault 干净；全库零 os.Exit(5)"
authority_sha >"${WORK}/authority.after.txt"
diff -u "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" >/dev/null ||
  die "降级路径改动了权威 Markdown（Markdown 是唯一权威来源，读不许写它）"
[ "$(commits)" = "${BASE_COMMITS}" ] || die "读命令产生了 commit（$(commits) ≠ ${BASE_COMMITS}）"
[ "$(gitv status --porcelain)" = "" ] || { gitv status --porcelain; die "跑完 vault 应干净（.index/ 不进 Git）"; }
[ "$(grep -rn 'os.Exit(5)' "${REPO_ROOT}/internal" | wc -l | tr -d ' ')" = "0" ] ||
  die "退出码面扩张：M5 不得出现 os.Exit(5)（属 M6）"
ok "权威与 Git 状态逐字不变；commit 仍为 ${BASE_COMMITS}；零 os.Exit(5)"

# ---------------------------------------------------------------- 8. 工作区零污染
step "工作区零污染：脚本写操作只发生在 mktemp -d 内"
[ "$(git -C "${REPO_ROOT}" status --porcelain | sort)" = "${BEFORE_REPO_STATUS}" ] ||
  die "evergreen 工作区被脚本改动"
ok "evergreen 工作区与运行前逐字相同"

printf '\n=== degrade_fallback.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
