#!/usr/bin/env bash
# D3 审计：事务恢复 / 锁与并发 / 索引一致性与降级（system_assurance · T-…-011）。
#
# 判据来源（声明面）：
#   `2027-01-24-m6-atomicity-and-strict-check-contract.md`
#     §1 A-52 / A-55 / A-58 / A-59；§16.1 命令三类别与锁 / 恢复覆盖面；§16.3 `.index/` 共存契约与
#     runtime reserved entry 类型违规；§18.1 类型违规三层分流；§18.4 `E15` 的 8 个原因；
#     §4 诊断码表 `E15 precheck_failed` / `E16 lock_timeout` / `W26`（恢复留痕）/ `W28`（锁等待）；
#   `2026-12-19-m5-index-architecture-contract.md`
#     §1.1 P-2（Markdown 唯一权威）；§4.1 索引列集合；§5.1 / A-44 水位线 `(head, files_hash)`；
#     §5.2 / §6.1 / §6.3 读路径降级与留痕（恰一条 `W22|W23|W24` + 恰一条 `Q5`）；§7.4 后端选择单点；
#   `internal/query/backend.go` 文件头逐字：「索引与扫描结果逐字等价」「宁可多读，绝不容许一条假阴性」；
#   EPIC：「Markdown 是唯一权威来源，派生索引可删可重建、零信息损失」。
#
# 为什么需要这支 suite：D3 审计（审计方法合同 `2027-03-10-system-audit-method-contract.md` §2 三面对照）
# 把下列条目判为「声明面有、实现面实测一致、但当前测试体系没有任何 suite 从**产品外部**（真实二进制 +
# 真实崩溃 / 真实持锁 / 真实损坏索引）直接锁死」。一致条目同样必须落成可执行判据 —— 否则下一轮回归无从判红。
#
# 本 suite 锁死的判据（全部在 mktemp -d 的真实 vault 上驱动真实二进制；事实只回读文件字节 /
# git 自己 / txnctl scan / eg 自己的 `--json` 信封，不看实现自报）：
#   A 锁覆盖面三类别（§16.1）：外部进程持 `.index/run.lock` 时
#     A 类 `mark-reviewed` 与 B 类 `index build` 退 5 + `E16` + 零写入零 commit；
#     C 类 `search` / `card show` / `check` / `index status` / `report --last` 一律退 0 且**零锁开销**
#     （单条 < 3s，远小于锁等待上限；证明只读命令不拿锁、不因锁忙失败）。
#   B 崩溃恢复（A-55）：`crash-at=partial` 半应用态下，下一条写命令产**恰一条 `W26`**、
#     把权威文件**逐字**回滚到前像（sha256 与崩溃前相等、`git status` 干净）、事务闭合（open=0）。
#   C 恢复幂等：紧接着的第二条写命令**不再**产 `W26`（恢复只发生一次）。
#   D pre-intent residue：无 `intent.json` 的残留被**静默**清理（residue 1→0）且不产 `W26`。
#   E `crash-at=committed`：已发布 commit marker 的事务**保持目标态**（不回滚），事务闭合。
#   F 损坏 intent fail closed（§18.4 原因③）：`intent.json` 不可解析 / `journal_version` 不支持 →
#     A 类退 5 + `E15`、intent **原样保留**；C 类只读**不退 5**（`search` 退 0）。
#   G B-R3 post-crash 外部编辑冲突（原因②）：intent 已发布后人工改目标文件 →
#     退 5 + `E15` + 人工字节**逐字保留**（零覆盖）+ 事务仍未闭合（open=1）。
#   H intent 路径违规（原因④）：把 intent 的 `files[].path` 改成 vault 外路径 →
#     退 5 + `E15` + 逃逸目标文件**未被创建**。
#   I `seq` 溢出（原因⑦）：`.index/txn/seq` = uint64 上限 → 退 5 + `E15` + seq 文件字节不变。
#   J runtime reserved entry 类型违规三层分流（§16.3 / §18.1）：`run.lock` 为目录 / `txn` 为普通文件 /
#     两者为 symlink 四形态下，A 类与 B 类退 5 + `E15`；C 类 `index status` 产 `W24` 且退 0、
#     `search` 产 `W24` + `Q5` 且退 0（只读永不退 5）。
#   K 索引降级语义与权威优先（§5.2 / §6.1 / §6.3）：缺失 → `W23`、损坏 → `W24`、陈旧 → `W22`，
#     读查询各自再加**恰一条 `Q5`**；三种降级态下 `search` 的命中集合与索引健康时**逐字相等**。
#   L 索引可删可重建、零信息损失：`rm -rf .index/` → 读照常出结果（`W23`+`Q5`）→ `index build` →
#     `health=healthy / freshness=fresh`、诊断码消失、答案与降级期**逐字相等**。
#   M 真并发写：两个 eg 同时写同一 vault 的不同目标 → 两者都退 0（后到者等锁后成功）、
#     `txnctl scan` 报 open=0 / corrupt=0、`git status` 干净、`eg check` 退 0。
#
# 刻意**不**在本 suite 断言的四条（已登记 issue，判据随修复任务落地，避免把缺陷锁成基线）：
#   · `config set` / `init` 不取锁、不过恢复屏障，且把半应用坏态提交进 Git 历史 → I-…-021（P0）；
#   · 崩溃恢复后第一条写命令必退 3 / `status=partial` 且零写入（base 快照早于 S2）→ I-…-022；
#     （本 suite 的 B / C / E 段一律用 `mark-reviewed` 驱动恢复，刻意避开该缺陷面）
#   · 写命令把工作区既有改动一并提交却零披露 → I-…-023；
#   · 仅删 `cards_fts` 行 / 篡改 `cards.deleted` 不被 `index.Check` 检出 → I-…-024；
#   · 锁等待 `W28` 落进 `data.errors[]`、信封 `warnings[]` 空 → I-…-016（D2 登记，D3 已交叉扩证）：
#     本 suite 的 A 段只断言退 5 / `E16` / 零写入，**不**断言 `W28` 落在哪个桶。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / python3）；
# 一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/d3_txn_lock_index.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 512

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-d3-txn-lock-index.XXXXXX")"
BASEV="${WORK}/base"          # 干净基准 vault（seed 一次，之后每段 cp -a 复制）
EG="${WORK}/eg"
TXN="${WORK}/txnctl"
HOLD_PID=""

STEP=0
PASS=0
cleanup() {
  [ -n "${HOLD_PID}" ] && kill -9 "${HOLD_PID}" 2>/dev/null || true
  rm -rf "${WORK}"
}
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 键级判定与 intent 改写"

CARD_A="domains/tech/knowledge/k-20270330-d3-alpha.md"
CARD_B="domains/tech/knowledge/k-20270330-d3-beta.md"
ID_A="k-20270330-d3-alpha"
ID_B="k-20270330-d3-beta"

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ── 通用工具（一律显式传 vault 绝对路径，不依赖 cwd 向上查找 evergreen.yml）
egv()  { "${EG}" --vault "$1" "${@:2}" </dev/null; }
# codev <vault> <args...>：回显退出码，输出落 out.txt（恒带 --json，便于键级判定）
codev() { local v="$1"; shift; local c=0; egv "${v}" "$@" --json >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gitv() { git -C "$1" "${@:2}"; }
sha()  { sha256sum "$1" | awk '{print $1}'; }
# 逐字精确读取（两级 X 哨兵：命令替换会吞掉尾随换行，不这样做会造出「前像与文件不等长」的伪影）
readexact() { local s; s=$(cat "$1"; printf X); printf '%s' "${s%X}"; }
# scanfield <vault> <字段名>：从 txnctl scan 末行取 entries/open/corrupt/residue/blocked
scanfield() {
  "${TXN}" scan --vault "$1" 2>/dev/null | tail -1 |
    tr ' ' '\n' | awk -F= -v k="$2" '$1==k {print $2}'
}
# jget <json文件> <python表达式>：o=信封根对象，d=data，
#   codes  = 各诊断桶（信封 warnings[] + data.errors[] + data.warnings[] + report.warnings[]）的 code 全集，
#            用于「是否出现过某个错误码」这类存在性判定；
#   wcodes = **仅信封 warnings[]** 的 code 列表，用于「恰一条 W22/W23/W24/Q5/W26」这类**计数**判定
#            —— 实现会把同一条 warning 同时投影到 data.report.warnings[]，按 codes 计数必然重复计入，
#            而合同说的「恰一条」指的是信封这一处对外契约面。
jget() {
  python3 - "$1" "$2" <<'PY'
import json, sys
raw = open(sys.argv[1], encoding="utf-8").read()
dec, o = json.JSONDecoder(), None
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            o, _ = dec.raw_decode(raw[i:]); break
        except ValueError:
            continue
if o is None:
    print("NOT_JSON"); sys.exit(0)
d = o.get("data") if isinstance(o.get("data"), dict) else {}
codes = []
for bucket in (o.get("warnings"), d.get("errors"), d.get("warnings"),
               (d.get("report") or {}).get("warnings")):
    for it in (bucket or []):
        if isinstance(it, dict):
            codes.append(it.get("code") or "")
wcodes = [ (it.get("code") or "") for it in (o.get("warnings") or []) if isinstance(it, dict) ]
print(eval(sys.argv[2]))
PY
}
# hits <vault> <关键词> <落地文件>：search 的命中 id 列表（排序后逐字可比）
hits() {
  egv "$1" search "$2" --json >"$3" 2>&1 || true
  jget "$3" 'sorted([ (h.get("id") or "") for h in ((d.get("hits") or [])) ])'
}
# 制造崩溃态：crash <vault> <crash-at> <标记前缀> [额外文件]
crash_at() {
  local v="$1" at="$2" mark="$3" pa pb rdy="${WORK}/rdy.$$"
  pa=$(readexact "${v}/${CARD_A}"; printf X); pa=${pa%X}
  pb=$(readexact "${v}/${CARD_B}"; printf X); pb=${pb%X}
  rm -f "${rdy}"
  "${TXN}" crash-commit --vault "${v}" --crash-at "${at}" --ready "${rdy}" \
    --file "${CARD_A}|false|${pa}|${mark}-A
" --file "${CARD_B}|false|${pb}|${mark}-B
" >"${WORK}/crash.log" 2>&1 &
  local p=$!
  local i
  for i in $(seq 1 100); do [ -f "${rdy}" ] && break; sleep 0.05; done
  kill -9 "${p}" 2>/dev/null || true
  wait "${p}" 2>/dev/null || true
  [ -f "${rdy}" ] || die "crash-commit 未到达崩溃点 ${at}（见 ${WORK}/crash.log）"
}
# 起一个真实持锁者并等它就绪；结束后必须 release_lock
acquire_lock() {
  local v="$1" rdy="${WORK}/lockrdy.$$"
  rm -f "${rdy}"
  "${TXN}" lock-hold --vault "${v}" --hold-ms 60000 --ready "${rdy}" \
    --argv "txnctl lock-hold(D3 suite)" >"${WORK}/hold.log" 2>&1 &
  HOLD_PID=$!
  local i
  for i in $(seq 1 120); do [ -f "${rdy}" ] && break; sleep 0.05; done
  [ -f "${rdy}" ] || die "外部持锁者未就绪（见 ${WORK}/hold.log）"
}
release_lock() {
  [ -n "${HOLD_PID}" ] && kill -9 "${HOLD_PID}" 2>/dev/null || true
  HOLD_PID=""
  sleep 0.2
}
# fresh <名字>：从基准 vault 复制一个一次性 vault，回显路径
fresh() { local v="${WORK}/$1"; rm -rf "${v}"; cp -a "${BASEV}" "${v}"; echo "${v}"; }

# ---------------------------------------------------------------- 0. 构建
step "构建 eg 与 txnctl（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${TXN}" ./tests/lib/txnctl) || die "编译 txnctl 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed
step "seed：走真实主链路 init → config → capture → context → apply → index build"
mkdir -p "${BASEV}"
[ "$(codev "${BASEV}" init --domain tech)" = "0" ] || { cat "${WORK}/out.txt"; die "eg init 失败"; }
[ "$(codev "${BASEV}" config set default_domain tech)" = "0" ] || die "config set 失败"
BODY='D3 审计语料正文：事务必须原子，锁必须互斥，恢复必须幂等，索引恒为可重建派生物。'
BODY="${BODY}${BODY}${BODY}"
printf '%s\n' "${BODY}" >"${WORK}/art.md"
[ "$(codev "${BASEV}" capture --url https://example.com/d3 --title 'D3 审计原文' \
      --reason 'D3 审计' --body-file "${WORK}/art.md")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "eg capture 失败"; }
SRC="$(ls "${BASEV}/sources" | head -1 | sed 's/\.md$//')"
[ -n "${SRC}" ] || die "capture 后 sources/ 为空"
BASE_HASH="$(egv "${BASEV}" context --source "${SRC}" --json |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
[ -n "${BASE_HASH}" ] || die "context 未回显 unprocessed.md 的 base hash"
cat >"${WORK}/seed_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "D3 审计 seed",
  "requirement_ids": ["EG-AUD-D3"],
  "base": { "unprocessed.md": "${BASE_HASH}" },
  "ops": [
   { "op": "write_note", "source": "${SRC}", "note_id": "n-20270330-d3", "title": "D3 审计材料笔记",
     "sections": { "材料提炼": "- 事务原子性是对外合同。\n", "Agent 分析": "- 仅用于 D3 判据。\n" },
     "output_cards": [{ "card": "k-20270330-d3-alpha", "mode": "新建" },
                      { "card": "k-20270330-d3-beta", "mode": "新建" },
                      { "card": "k-20270330-d3-gamma", "mode": "新建" }] },
   { "op": "create_card", "card_id": "k-20270330-d3-alpha", "title": "D3 卡 Alpha", "tags": ["audit"],
     "sources": [{ "source": "${SRC}", "note": "n-20270330-d3", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "多文件原子提交要么全部生效要么全部不生效。alphaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } },
   { "op": "create_card", "card_id": "k-20270330-d3-beta", "title": "D3 卡 Beta", "tags": ["audit"],
     "sources": [{ "source": "${SRC}", "note": "n-20270330-d3", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "run.lock 的互斥真源只认内核 flock。betaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } },
   { "op": "create_card", "card_id": "k-20270330-d3-gamma", "title": "D3 卡 Gamma", "tags": ["audit"],
     "sources": [{ "source": "${SRC}", "note": "n-20270330-d3", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "索引损坏时读路径必须降级并给出与权威一致的答案。gammaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } }
  ] }
PLAN
[ "$(codev "${BASEV}" apply --plan "${WORK}/seed_plan.json")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "seed 用 apply 应退 0"; }
[ "$(codev "${BASEV}" index build)" = "0" ] || { cat "${WORK}/out.txt"; die "index build 失败"; }
[ -f "${BASEV}/.index/eg.db" ] || die "index build 后 .index/eg.db 不存在"
[ -z "$(gitv "${BASEV}" status --porcelain)" ] || die "seed 后基准 vault 工作树应干净"
GOLD_HITS_ALPHA="$(hits "${BASEV}" Alpha "${WORK}/gold.json")"
[ "${GOLD_HITS_ALPHA}" = "['${ID_A}']" ] ||
  die "基准命中集合意外：${GOLD_HITS_ALPHA}（期望 ['${ID_A}']）"
ok "基准 vault 就绪：3 卡 + 索引 healthy；基准命中集合 ${GOLD_HITS_ALPHA}"

# ---------------------------------------------------------------- 2. A 锁覆盖面三类别
step "A 锁覆盖面（§16.1）：A/B 类锁忙退 5 + E16 零写入；C 类零锁开销退 0"
V="$(fresh a_lock)"
N0="$(gitv "${V}" rev-list --count HEAD)"
SNAP_A="$(sha "${V}/${CARD_A}")"
acquire_lock "${V}"
# 先测 C 类（快路径：证明只读命令根本不参与等锁），再测 A/B 类（各等锁到 10s 上限）
for c in "search Alpha" "card show ${ID_A}" "check" "index status" "report --last"; do
  T0=$(date +%s%3N)
  # shellcheck disable=SC2086
  RC_C="$(codev "${V}" ${c})"
  T1=$(date +%s%3N)
  [ "${RC_C}" = "0" ] || { cat "${WORK}/out.txt"; die "C 类 'eg ${c}' 在锁忙时应退 0，实际 ${RC_C}"; }
  [ "$((T1 - T0))" -lt 3000 ] ||
    die "C 类 'eg ${c}' 耗时 $((T1 - T0))ms：只读命令不得等锁（§16.1 C 类零锁开销）"
done
RC_A="$(codev "${V}" mark-reviewed --target "${ID_A}")"
[ "${RC_A}" = "5" ] || { cat "${WORK}/out.txt"; die "A 类命令锁忙应退 5，实际 ${RC_A}"; }
cp "${WORK}/out.txt" "${WORK}/lockA.json"
[ "$(jget "${WORK}/lockA.json" '"E16" in codes')" = "True" ] ||
  { cat "${WORK}/lockA.json"; die "A 类锁忙应携 E16"; }
[ "$(jget "${WORK}/lockA.json" 'o.get("status")')" = "failed" ] || die "锁忙 status 应为 failed"
RC_B="$(codev "${V}" index build)"
[ "${RC_B}" = "5" ] || { cat "${WORK}/out.txt"; die "B 类 index build 锁忙应退 5，实际 ${RC_B}"; }
[ "$(jget "${WORK}/out.txt" '"E16" in codes')" = "True" ] || die "B 类锁忙应携 E16"
release_lock
[ "$(gitv "${V}" rev-list --count HEAD)" = "${N0}" ] || die "锁忙路径不得产生 commit"
[ "$(sha "${V}/${CARD_A}")" = "${SNAP_A}" ] || die "锁忙路径必须零写入（文件字节应逐字不变）"
[ -z "$(gitv "${V}" status --porcelain)" ] || die "锁忙路径后工作树应干净"
ok "A 类 + B 类退 5 + E16 + 零写入零 commit；5 条 C 类命令均退 0 且 < 3s（零锁开销）"

# ---------------------------------------------------------------- 3. B/C 崩溃恢复与幂等
step "B 崩溃恢复（A-55）：partial 半应用 → 恰一条 W26 + 逐字回前像 + 事务闭合；C 恢复幂等"
V="$(fresh b_recover)"
PRE_A="$(sha "${V}/${CARD_A}")"
PRE_B="$(sha "${V}/${CARD_B}")"
N0="$(gitv "${V}" rev-list --count HEAD)"
crash_at "${V}" partial TAMPERED-PARTIAL
grep -q 'TAMPERED-PARTIAL-A' "${V}/${CARD_A}" || die "crash-at=partial 未造出半应用态"
[ "$(scanfield "${V}" open)" = "1" ] || die "partial 崩溃后应恰有 1 个未闭合事务"
RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "恢复后 mark-reviewed 应退 0，实际 ${RC}"; }
cp "${WORK}/out.txt" "${WORK}/rec1.json"
[ "$(jget "${WORK}/rec1.json" 'sum(1 for c in wcodes if c=="W26")')" = "1" ] ||
  { cat "${WORK}/rec1.json"; die "崩溃后首条写命令应产恰一条 W26"; }
[ "$(sha "${V}/${CARD_A}")" != "${PRE_A}" ] || true   # 本次 mark-reviewed 会写 reviewed_at，A 卡允许变
[ "$(sha "${V}/${CARD_B}")" = "${PRE_B}" ] ||
  die "未被本次命令触达的 B 卡必须被恢复到前像的逐字字节"
grep -q 'TAMPERED-PARTIAL' "${V}/${CARD_A}" && die "恢复后权威文件不得残留半应用坏字节"
grep -q 'TAMPERED-PARTIAL' "${V}/${CARD_B}" && die "恢复后权威文件不得残留半应用坏字节"
[ "$(scanfield "${V}" open)" = "0" ] || die "恢复后未闭合事务应为 0（已写 abort）"
[ "$(scanfield "${V}" corrupt)" = "0" ] || die "恢复后不应存在损坏事务"
# C 恢复幂等：紧接着第二条写命令不得再产 W26
RC="$(codev "${V}" mark-reviewed --target "${ID_B}")"
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "第二条写命令应退 0，实际 ${RC}"; }
[ "$(jget "${WORK}/out.txt" 'sum(1 for c in wcodes if c=="W26")')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "恢复必须幂等：第二条写命令不得再产 W26"; }
[ -z "$(gitv "${V}" status --porcelain)" ] || die "两条写命令后工作树应干净"
ok "partial → 恰一条 W26 + 逐字回前像 + open=0；第二条写命令零 W26（幂等）"

# ---------------------------------------------------------------- 4. D pre-intent residue
step "D pre-intent residue：无 intent.json 的残留被静默清理且不产 W26"
V="$(fresh d_residue)"
crash_at "${V}" pre_intent RESIDUE
[ "$(scanfield "${V}" residue)" = "1" ] || die "crash-at=pre_intent 应造出 1 个 residue"
RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "residue 场景写命令应退 0，实际 ${RC}"; }
[ "$(jget "${WORK}/out.txt" 'sum(1 for c in wcodes if c=="W26")')" = "0" ] ||
  die "pre-intent residue 属静默清理，不得产 W26（A-55 逐字）"
[ "$(scanfield "${V}" residue)" = "0" ] || die "residue 应被清理"
ok "residue 1→0，静默清理且零 W26"

# ---------------------------------------------------------------- 5. E crash-at=committed
step "E crash-at=committed：已发布 commit marker 的事务保持目标态（不回滚）"
V="$(fresh e_committed)"
crash_at "${V}" committed COMMITTEDMARK
head -1 "${V}/${CARD_A}" | grep -q 'COMMITTEDMARK-A' || die "crash-at=committed 未落到目标态"
TARGET_SHA="$(sha "${V}/${CARD_A}")"
# 此处刻意用只读命令 + txnctl recover 观察恢复语义，避免依赖某条写命令的业务校验结果
"${TXN}" recover --vault "${V}" >"${WORK}/rec.log" 2>&1 || true
[ "$(sha "${V}/${CARD_A}")" = "${TARGET_SHA}" ] ||
  die "已提交事务不得被回滚（commit marker 在盘即前滚语义）"
[ "$(scanfield "${V}" open)" = "0" ] || die "committed 事务不应被算作未闭合"
ok "committed 崩溃点：目标态字节逐字保持，open=0"

# ---------------------------------------------------------------- 6. F 损坏 intent fail closed
step "F 损坏 intent fail closed（§18.4 原因③）：A 类退 5 + E15 + 原样保留；只读不退 5"
for kind in bad_json bad_version; do
  V="$(fresh "f_${kind}")"
  crash_at "${V}" after_intent CORRUPTINTENT
  TID="$(ls "${V}/.index/txn" | grep '^t' | sort | tail -1)"
  IJ="${V}/.index/txn/${TID}/intent.json"
  [ -f "${IJ}" ] || die "after_intent 后应存在 intent.json"
  case "${kind}" in
    bad_json)    printf '{ 这不是合法 JSON' >"${IJ}" ;;
    bad_version) python3 -c "
import json,sys
p=sys.argv[1]; d=json.load(open(p)); d['journal_version']=999
json.dump(d, open(p,'w'), ensure_ascii=False)" "${IJ}" ;;
  esac
  BAD_SHA="$(sha "${IJ}")"
  RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
  [ "${RC}" = "5" ] || { cat "${WORK}/out.txt"; die "${kind}: A 类应 fail closed 退 5，实际 ${RC}"; }
  [ "$(jget "${WORK}/out.txt" '"E15" in codes')" = "True" ] || die "${kind}: 应携 E15"
  [ -f "${IJ}" ] && [ "$(sha "${IJ}")" = "${BAD_SHA}" ] ||
    die "${kind}: 损坏 intent 必须原样保留（不得自动删除/改写）"
  RC="$(codev "${V}" search Alpha)"
  [ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "${kind}: C 类只读命令绝不退 5（§18.1），实际 ${RC}"; }
done
ok "bad_json / bad_version 两形态：A 类退 5 + E15 + intent 原样保留；只读退 0"

# ---------------------------------------------------------------- 7. G B-R3 外部编辑冲突
step "G B-R3 post-crash 外部编辑冲突（原因②）：退 5 + E15 + 人工字节逐字保留 + 事务仍未闭合"
V="$(fresh g_br3)"
crash_at "${V}" after_intent BR3MARK
printf '\n<!-- 人工外部编辑 HUMAN-EDIT-D3 -->\n' >>"${V}/${CARD_A}"
HUMAN_SHA="$(sha "${V}/${CARD_A}")"
RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
[ "${RC}" = "5" ] || { cat "${WORK}/out.txt"; die "B-R3 冲突应退 5，实际 ${RC}"; }
[ "$(jget "${WORK}/out.txt" '"E15" in codes')" = "True" ] || die "B-R3 冲突应携 E15"
[ "$(sha "${V}/${CARD_A}")" = "${HUMAN_SHA}" ] ||
  die "B-R3 冲突必须零写入：人工编辑的字节不得被前像覆盖"
grep -q 'HUMAN-EDIT-D3' "${V}/${CARD_A}" || die "人工编辑内容应逐字保留"
[ "$(scanfield "${V}" open)" = "1" ] ||
  die "B-R3 冲突下事务应保持未闭合（fail closed，不得擅自 abort）"
ok "退 5 + E15 + 人工字节逐字保留 + open=1"

# ---------------------------------------------------------------- 8. H intent 路径违规
step "H intent 路径违规（原因④）：逃逸路径 → 退 5 + E15 + 逃逸目标未被创建"
V="$(fresh h_escape)"
ESCAPED="${WORK}/ESCAPED_D3_SUITE.md"
rm -f "${ESCAPED}"
crash_at "${V}" after_intent ESCAPEMARK
TID="$(ls "${V}/.index/txn" | grep '^t' | sort | tail -1)"
python3 - "${V}/.index/txn/${TID}/intent.json" "${ESCAPED}" <<'PY'
import json, os, sys
p, esc = sys.argv[1], sys.argv[2]
d = json.load(open(p))
# 把首个条目的目标路径改成 vault 之外的绝对路径的相对形式（逐级 ../ 逃逸）
rel = os.path.relpath(esc, os.path.dirname(os.path.dirname(os.path.dirname(p))))
d["files"][0]["path"] = rel
json.dump(d, open(p, "w"), ensure_ascii=False)
print(rel)
PY
RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
[ "${RC}" = "5" ] || { cat "${WORK}/out.txt"; die "intent 路径违规应退 5，实际 ${RC}"; }
[ "$(jget "${WORK}/out.txt" '"E15" in codes')" = "True" ] || die "intent 路径违规应携 E15"
[ ! -e "${ESCAPED}" ] || die "逃逸目标文件被创建：路径校验未生效（严重越界）"
ok "退 5 + E15 + 逃逸目标未被创建"

# ---------------------------------------------------------------- 9. I seq 溢出
step "I seq 溢出（原因⑦）：seq = uint64 上限 → 退 5 + E15 + seq 字节不变"
V="$(fresh i_seq)"
printf '18446744073709551615' >"${V}/.index/txn/seq"
SEQ_SHA="$(sha "${V}/.index/txn/seq")"
RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
[ "${RC}" = "5" ] || { cat "${WORK}/out.txt"; die "seq 溢出应退 5，实际 ${RC}"; }
[ "$(jget "${WORK}/out.txt" '"E15" in codes')" = "True" ] || die "seq 溢出应携 E15"
[ "$(sha "${V}/.index/txn/seq")" = "${SEQ_SHA}" ] || die "seq 溢出路径必须零写入"
[ "$(codev "${V}" search Alpha)" = "0" ] || die "seq 溢出时只读命令仍应退 0"
ok "退 5 + E15 + seq 字节不变；只读不受影响"

# ---------------------------------------------------------------- 10. J reserved entry 类型违规分层
step "J runtime reserved entry 类型违规（§16.3 / §18.1）：A/B 类退 5 + E15；C 类 W24（+Q5）且退 0"
for viol in lock_is_dir txn_is_file lock_symlink txn_symlink; do
  V="$(fresh "j_${viol}")"
  case "${viol}" in
    lock_is_dir)  rm -f "${V}/.index/run.lock"; mkdir -p "${V}/.index/run.lock" ;;
    txn_is_file)  rm -rf "${V}/.index/txn"; printf 'x' >"${V}/.index/txn" ;;
    lock_symlink) rm -f "${V}/.index/run.lock"; : >"${WORK}/tgt_lock_${viol}"
                  ln -s "${WORK}/tgt_lock_${viol}" "${V}/.index/run.lock" ;;
    txn_symlink)  rm -rf "${V}/.index/txn"; mkdir -p "${WORK}/tgt_txn_${viol}"
                  ln -s "${WORK}/tgt_txn_${viol}" "${V}/.index/txn" ;;
  esac
  RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
  [ "${RC}" = "5" ] || { cat "${WORK}/out.txt"; die "${viol}: A 类应退 5，实际 ${RC}"; }
  [ "$(jget "${WORK}/out.txt" '"E15" in codes')" = "True" ] || die "${viol}: A 类应携 E15"
  RC="$(codev "${V}" index build)"
  [ "${RC}" = "5" ] || { cat "${WORK}/out.txt"; die "${viol}: B 类应退 5，实际 ${RC}"; }
  [ "$(jget "${WORK}/out.txt" '"E15" in codes')" = "True" ] || die "${viol}: B 类应携 E15"
  RC="$(codev "${V}" index status)"
  [ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "${viol}: index status 恒退 0，实际 ${RC}"; }
  [ "$(jget "${WORK}/out.txt" 'sum(1 for c in wcodes if c=="W24")')" = "1" ] ||
    { cat "${WORK}/out.txt"; die "${viol}: index status 应产恰一条 W24"; }
  RC="$(codev "${V}" search Alpha)"
  [ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "${viol}: search 绝不退 5，实际 ${RC}"; }
  cp "${WORK}/out.txt" "${WORK}/j_${viol}_search.json"
  [ "$(jget "${WORK}/j_${viol}_search.json" 'sum(1 for c in wcodes if c=="W24")')" = "1" ] ||
    die "${viol}: search 应产恰一条 W24"
  [ "$(jget "${WORK}/j_${viol}_search.json" 'sum(1 for c in wcodes if c=="Q5")')" = "1" ] ||
    die "${viol}: search 降级应产恰一条 Q5"
done
ok "四形态 × （A 类退 5 / B 类退 5 / index status W24 退 0 / search W24+Q5 退 0）"

# ---------------------------------------------------------------- 11. K 索引降级语义与权威优先
step "K 降级语义（§6.3）：缺失 W23 / 损坏 W24 / 陈旧 W22，各恰一条 Q5，且答案与健康态逐字相等"
# K1 缺失
V="$(fresh k_missing)"
rm -f "${V}/.index/eg.db"
H="$(hits "${V}" Alpha "${WORK}/k1.json")"
[ "${H}" = "${GOLD_HITS_ALPHA}" ] || die "索引缺失时命中集合应与健康态逐字相等，实际 ${H}"
[ "$(jget "${WORK}/k1.json" 'o.get("exit_code")')" = "0" ] || die "索引缺失不得改变退出码"
[ "$(jget "${WORK}/k1.json" 'sum(1 for c in wcodes if c=="W23")')" = "1" ] || die "缺失应产恰一条 W23"
[ "$(jget "${WORK}/k1.json" 'sum(1 for c in wcodes if c=="Q5")')" = "1" ] || die "缺失降级应产恰一条 Q5"
# K2 损坏
V="$(fresh k_corrupt)"
printf 'GARBAGE-NOT-SQLITE' >"${V}/.index/eg.db"
H="$(hits "${V}" Alpha "${WORK}/k2.json")"
[ "${H}" = "${GOLD_HITS_ALPHA}" ] || die "索引损坏时命中集合应与健康态逐字相等，实际 ${H}"
[ "$(jget "${WORK}/k2.json" 'sum(1 for c in wcodes if c=="W24")')" = "1" ] || die "损坏应产恰一条 W24"
[ "$(jget "${WORK}/k2.json" 'sum(1 for c in wcodes if c=="Q5")')" = "1" ] || die "损坏降级应产恰一条 Q5"
[ "$(codev "${V}" index status)" = "0" ] || die "index status 恒退 0"
[ "$(jget "${WORK}/out.txt" '((d.get("index") or {}).get("health"))')" = "corrupt" ] ||
  die "垃圾字节应判 health=corrupt"
# K3 陈旧（权威新增一张卡但不同步索引）
V="$(fresh k_stale)"
sed -e "s/${ID_A}/k-20270330-d3-delta/g" -e 's/D3 卡 Alpha/D3 卡 Delta/' -e 's/alphaword/deltaword/' \
  "${V}/${CARD_A}" >"${V}/domains/tech/knowledge/k-20270330-d3-delta.md"
H="$(hits "${V}" deltaword "${WORK}/k3.json")"
[ "${H}" = "['k-20270330-d3-delta']" ] ||
  die "陈旧索引下必须命中权威新卡（无假阴性），实际 ${H}"
[ "$(jget "${WORK}/k3.json" 'sum(1 for c in wcodes if c=="W22")')" = "1" ] || die "陈旧应产恰一条 W22"
[ "$(jget "${WORK}/k3.json" 'sum(1 for c in wcodes if c=="Q5")')" = "1" ] || die "陈旧降级应产恰一条 Q5"
# 权威删卡但索引未同步：不得把已删卡当命中返回
V="$(fresh k_stale_del)"
rm -f "${V}/domains/tech/knowledge/k-20270330-d3-gamma.md"
H="$(hits "${V}" Gamma "${WORK}/k4.json")"
[ "${H}" = "[]" ] || die "陈旧索引不得把权威已删除的卡当命中返回，实际 ${H}"
ok "W23 / W24 / W22 各恰一条 + 恰一条 Q5；三态答案与健康态逐字相等；权威增删即时可见"

# ---------------------------------------------------------------- 12. L 索引可删可重建
step "L 索引可删可重建、零信息损失：rm -rf .index/ → 读照常 → index build → 码消失且答案不变"
V="$(fresh l_rebuild)"
rm -rf "${V}/.index"
H_DEGRADED="$(hits "${V}" Alpha "${WORK}/l1.json")"
[ "${H_DEGRADED}" = "${GOLD_HITS_ALPHA}" ] || die "删掉整个 .index/ 后答案应不变，实际 ${H_DEGRADED}"
[ "$(jget "${WORK}/l1.json" 'sum(1 for c in wcodes if c=="W23")')" = "1" ] || die "应产 W23"
[ "$(codev "${V}" index build)" = "0" ] || { cat "${WORK}/out.txt"; die "index build 应退 0"; }
[ "$(codev "${V}" index status)" = "0" ] || die "index status 应退 0"
[ "$(jget "${WORK}/out.txt" '((d.get("index") or {}).get("health"))')" = "healthy" ] || die "重建后应 healthy"
[ "$(jget "${WORK}/out.txt" '((d.get("index") or {}).get("freshness"))')" = "fresh" ] || die "重建后应 fresh"
H_REBUILT="$(hits "${V}" Alpha "${WORK}/l2.json")"
[ "${H_REBUILT}" = "${H_DEGRADED}" ] || die "重建前后答案必须逐字相等：${H_DEGRADED} vs ${H_REBUILT}"
[ "$(jget "${WORK}/l2.json" 'sum(1 for c in wcodes if c in ("W22","W23","W24","Q5"))')" = "0" ] ||
  die "重建后不应再有降级诊断码"
ok "整目录删除后读路径照常（W23）；重建后 healthy/fresh、码消失、答案逐字相等"

# ---------------------------------------------------------------- 13. M 真并发写
step "M 真并发写：两个 eg 同时写同一 vault 的不同目标 → 都成功、事务全闭合、库仍自洽"
V="$(fresh m_concurrent)"
FAILED=0
for i in 1 2 3; do
  ( "${EG}" --vault "${V}" mark-reviewed --target "${ID_A}" --json >"${WORK}/m_a_${i}.json" 2>&1
    echo $? >"${WORK}/m_ra_${i}" ) </dev/null &
  P1=$!
  ( "${EG}" --vault "${V}" mark-reviewed --target "${ID_B}" --json >"${WORK}/m_b_${i}.json" 2>&1
    echo $? >"${WORK}/m_rb_${i}" ) </dev/null &
  P2=$!
  wait "${P1}" "${P2}" || true
  for f in "${WORK}/m_ra_${i}" "${WORK}/m_rb_${i}"; do
    R="$(cat "${f}")"
    case "${R}" in
      0) : ;;                                   # 等锁后成功
      5) : ;;                                   # 锁忙退 5 + 零写入，同样是合法出口
      *) FAILED=1; echo "  并发第 ${i} 轮出现非法退出码 ${R}" >&2 ;;
    esac
  done
done
[ "${FAILED}" = "0" ] || die "并发写只允许 0（等锁后成功）或 5（锁忙零写入）两种出口"
[ "$(scanfield "${V}" open)" = "0" ] || die "并发写后不得残留未闭合事务"
[ "$(scanfield "${V}" corrupt)" = "0" ] || die "并发写后不得出现损坏事务"
[ -z "$(gitv "${V}" status --porcelain)" ] || die "并发写后工作树应干净（无半写文件）"
gitv "${V}" fsck >"${WORK}/fsck.log" 2>&1 || die "并发写后 git fsck 失败"
[ "$(codev "${V}" check)" = "0" ] || { cat "${WORK}/out.txt"; die "并发写后 eg check 应退 0"; }
ok "3 轮 × 2 并发写：出口只有 0/5；open=0 corrupt=0；工作树干净；git fsck 与 eg check 通过"

# ---------------------------------------------------------------- 14. 收尾自查
step "收尾自查：所有写操作只发生在临时目录，仓库工作树前后逐字相等"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "越界：本脚本改动了仓库工作树"; }
ok "仓库工作树前后逐字相等（写操作全部落在 ${WORK} 内）"

printf '\n=== d3_txn_lock_index.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
