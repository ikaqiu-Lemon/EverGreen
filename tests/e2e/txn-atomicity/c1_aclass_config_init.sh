#!/usr/bin/env bash
# 批次C1 · I-…-021（P0）判据：`eg config set` / `eg init` 必须是 **A 类写路径**
# —— 取 `vault/.index/run.lock`、过 S2 崩溃恢复屏障，且**绝不**把半应用坏态提交进 Git 历史。
#
# 判据来源（声明面，逐字）：
#   `2027-01-24-m6-atomicity-and-strict-check-contract.md`
#     §16.1 命令三类别：**A 类 = 写权威 Markdown 的命令**，一律「持 run.lock + 过恢复屏障」；
#     §1 A-52：任一时刻同一 vault 至多一个写事务（互斥真源是内核 flock）；
#     §1 A-55：崩溃恢复是写命令的启动 hook，「解析参数之后、拿锁之后、任何写入之前」执行；
#     §4 诊断码表 `E16 lock_timeout`（锁忙）/ `W26`（恢复留痕）；§6.1 退出码 5 = 需人工/被阻断。
#   `internal/git/doc.go:11`：提交范围唯一口径 `git add -A`（本次改动与工作区既有改动一并提交）
#     —— 正因为口径是 add -A，**未经恢复屏障就 commit** 必然把上一次崩溃留下的半应用字节
#     一起写进权威历史，这是 I-…-021 判 P0 的直接后果面。
#
# 为什么需要这支 suite：D3 审计实测 `config set` / `init` 两条命令
#   ① 外部持锁时**不等待、不退 5**，直接写 `evergreen.yml` 并产生 commit（A-52 被绕过）；
#   ② 崩溃半应用态下**不产 W26、不回滚**，并把坏态字节 `git add -A` 进 HEAD（A-55 被绕过）。
# 既有 suite（`run_lock.sh` / `crash_recovery.sh` / `d3_txn_lock_index.sh`）一律用
# `mark-reviewed` 等命令驱动锁与恢复，从未从产品外部锁死这两条命令，因此缺陷可长期存活。
#
# 本 suite 锁死的判据（真实二进制 + 真实外部持锁 + 真实崩溃；事实只回读文件字节 / git 自己 /
# txnctl scan / `--json` 信封，不看实现自报）：
#   A 锁忙：外部持 run.lock 时 `config set` 与（幂等路径的）`init` 一律退 5 + `E16`，
#     且 `evergreen.yml` 字节不变、`git rev-list --count HEAD` 不变（零写入零 commit）。
#   B 恢复屏障（config set）：`crash-at=partial` 半应用态下 `config set` 退 0、产**恰一条 W26**、
#     权威卡逐字回到前像、坏态字节**不进 HEAD**、事务闭合（open=0）、配置改写照常生效。
#   C 恢复屏障（init 幂等路径）：同一崩溃态下 `eg init` 退 0、产**恰一条 W26**、
#     坏态字节不进 HEAD、事务闭合，且**不改**任何既存文件字节（幂等）。
#   D 首次引导豁免：全新空目录 `eg init` 退 0，且 F1 的 [S1] 列不变 —— `.index/` 不留
#     `eg.db`（索引仍是 S4 的事，init 不替用户建索引）。
#   E 恢复幂等：B 段之后紧接的第二条 `config set` **不再**产 W26。
#
# 刻意**不**在本 suite 断言（边界，避免把判据摊成别的 task）：
#   · 锁等待 `W28` 落在信封哪个桶 → I-…-016（D2 登记）；本 suite 只断言退 5 / `E16` / 零写入。
#   · 既有改动披露口径 → I-…-023（另有 `c1_preexisting_disclosure.sh`）。
#   · 崩溃恢复后首条写命令的退出码语义 → I-…-022（另有 `c1_recover_first_write.sh`）。
#
# 约束：离线、零交互、可重复、无外部依赖（bash / coreutils / git / go / python3）；
# 一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/c1_aclass_config_init.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c1-aclass-config-init.XXXXXX")"
BASEV="${WORK}/base"
EG="${WORK}/eg"
TXN="${WORK}/txnctl"
HOLD_PID=""

# 锁等待上限压到 1.5s：本 suite 要反复验证「锁忙必须退 5」，默认 10s 会让整支 suite
# 白等几十秒。上限本身是环境参数（EG_LOCK_TIMEOUT_MS），压短不改变判据。
export EG_LOCK_TIMEOUT_MS=1500

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

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 键级判定"

CARD_A="domains/tech/knowledge/k-20270412-c1-alpha.md"
CARD_B="domains/tech/knowledge/k-20270412-c1-beta.md"
ID_A="k-20270412-c1-alpha"

egv()  { "${EG}" --vault "$1" "${@:2}" </dev/null; }
codev() { local v="$1"; shift; local c=0; egv "${v}" "$@" --json >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gitv() { git -C "$1" "${@:2}"; }
sha()  { sha256sum "$1" | awk '{print $1}'; }
readexact() { local s; s=$(cat "$1"; printf X); printf '%s' "${s%X}"; }
scanfield() {
  "${TXN}" scan --vault "$1" 2>/dev/null | tail -1 |
    tr ' ' '\n' | awk -F= -v k="$2" '$1==k {print $2}'
}
# jget：codes = 全部诊断桶的 code 全集（存在性判定）；wcodes = 仅信封 warnings[]（计数判定）
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

# crash_at <vault> <crash-at> <标记前缀>：真实崩溃在指定阶段（半应用 = 只 rename 了一部分）
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

acquire_lock() {
  local v="$1" rdy="${WORK}/lockrdy.$$"
  rm -f "${rdy}"
  "${TXN}" lock-hold --vault "${v}" --hold-ms 60000 --ready "${rdy}" \
    --argv "txnctl lock-hold(C1 suite)" >"${WORK}/hold.log" 2>&1 &
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
fresh() { local v="${WORK}/$1"; rm -rf "${v}"; cp -a "${BASEV}" "${v}"; echo "${v}"; }

# ---------------------------------------------------------------- 0. 构建
step "构建 eg 与 txnctl（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${TXN}" ./tests/lib/txnctl) || die "编译 txnctl 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed
step "seed：init → config set → 两张卡（真实主链路 capture + apply）"
mkdir -p "${BASEV}"
[ "$(codev "${BASEV}" init --domain tech)" = "0" ] || { cat "${WORK}/out.txt"; die "eg init 失败"; }
[ "$(codev "${BASEV}" config set default_domain tech)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "config set 失败"; }
BODY='C1 判据语料：A 类写命令必须持锁并过恢复屏障，配置写入同样是权威写入。'
BODY="${BODY}${BODY}${BODY}"
printf '%s\n' "${BODY}" >"${WORK}/art.md"
[ "$(codev "${BASEV}" capture --url https://example.com/c1 --title 'C1 判据原文' \
      --reason 'C1 修复批次' --body-file "${WORK}/art.md")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "eg capture 失败"; }
SRC="$(ls "${BASEV}/sources" | head -1 | sed 's/\.md$//')"
[ -n "${SRC}" ] || die "capture 后 sources/ 为空"
BASE_HASH="$(egv "${BASEV}" context --source "${SRC}" --json |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
[ -n "${BASE_HASH}" ] || die "context 未回显 unprocessed.md 的 base hash"
cat >"${WORK}/seed_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "C1 判据 seed",
  "requirement_ids": ["EG-FIX-C1"],
  "base": { "unprocessed.md": "${BASE_HASH}" },
  "ops": [
   { "op": "write_note", "source": "${SRC}", "note_id": "n-20270412-c1", "title": "C1 判据材料笔记",
     "sections": { "材料提炼": "- 配置写入也是权威写入。\n", "Agent 分析": "- 仅用于 C1 判据。\n" },
     "output_cards": [{ "card": "k-20270412-c1-alpha", "mode": "新建" },
                      { "card": "k-20270412-c1-beta", "mode": "新建" }] },
   { "op": "create_card", "card_id": "k-20270412-c1-alpha", "title": "C1 卡 Alpha", "tags": ["fix"],
     "sources": [{ "source": "${SRC}", "note": "n-20270412-c1", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "A 类命令的互斥真源是内核 flock。alphaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } },
   { "op": "create_card", "card_id": "k-20270412-c1-beta", "title": "C1 卡 Beta", "tags": ["fix"],
     "sources": [{ "source": "${SRC}", "note": "n-20270412-c1", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "恢复屏障必须早于任何一次业务写。betaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } }
  ] }
PLAN
[ "$(codev "${BASEV}" apply --plan "${WORK}/seed_plan.json")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "seed 用 apply 应退 0"; }
[ -z "$(gitv "${BASEV}" status --porcelain)" ] || die "seed 后基准 vault 工作树应干净"
ok "基准 vault 就绪：2 卡、工作树干净、commit 数 $(gitv "${BASEV}" rev-list --count HEAD)"

# ---------------------------------------------------------------- 2. A 锁忙
step "A（§16.1 / A-52）：外部持锁时 config set 与 init 必须退 5 + E16 + 零写入零 commit"
V="$(fresh a_lockbusy)"
N0="$(gitv "${V}" rev-list --count HEAD)"
CFG_SHA0="$(sha "${V}/evergreen.yml")"
acquire_lock "${V}"

RC="$(codev "${V}" config set domains lab)"
cp "${WORK}/out.txt" "${WORK}/cfg_lock.json"
[ "${RC}" = "5" ] || { cat "${WORK}/cfg_lock.json"; die "config set 锁忙应退 5（A 类），实际 ${RC}"; }
[ "$(jget "${WORK}/cfg_lock.json" '"E16" in codes')" = "True" ] ||
  { cat "${WORK}/cfg_lock.json"; die "config set 锁忙应携 E16"; }
[ "$(sha "${V}/evergreen.yml")" = "${CFG_SHA0}" ] ||
  die "config set 锁忙必须零写入：evergreen.yml 字节被改了"
[ "$(gitv "${V}" rev-list --count HEAD)" = "${N0}" ] ||
  die "config set 锁忙必须零 commit：commit 数从 ${N0} 变成 $(gitv "${V}" rev-list --count HEAD)"
[ ! -d "${V}/domains/lab" ] || die "config set 锁忙必须零写入：不得建出 domains/lab/"
ok "config set 锁忙：退 5 + E16 + evergreen.yml 字节不变 + commit 数不变 + 未建领域目录"

RC="$(codev "${V}" init --domain tech)"
cp "${WORK}/out.txt" "${WORK}/init_lock.json"
[ "${RC}" = "5" ] || { cat "${WORK}/init_lock.json"; die "init（幂等路径）锁忙应退 5，实际 ${RC}"; }
[ "$(jget "${WORK}/init_lock.json" '"E16" in codes')" = "True" ] ||
  { cat "${WORK}/init_lock.json"; die "init 锁忙应携 E16"; }
[ "$(gitv "${V}" rev-list --count HEAD)" = "${N0}" ] || die "init 锁忙必须零 commit"
ok "init（幂等路径）锁忙：退 5 + E16 + 零 commit"

# 对照：C 类只读命令在同一把锁忙时照常退 0（证明这一段测的是 A 类语义，不是把库测挂了）
for c in "search alphaword" "card show ${ID_A}" "config get default_domain"; do
  # shellcheck disable=SC2086
  RC_C="$(codev "${V}" ${c})"
  [ "${RC_C}" = "0" ] || { cat "${WORK}/out.txt"; die "C 类 'eg ${c}' 锁忙应退 0，实际 ${RC_C}"; }
done
ok "对照：C 类 search / card show / config get 在同一锁忙下一律退 0"
release_lock

# ---------------------------------------------------------------- 3. B 恢复屏障（config set）
step "B（A-55）：半应用崩溃态下 config set 必须先恢复（恰一条 W26）、坏态不得进 HEAD"
V="$(fresh b_recover_config)"
PRE_A="$(sha "${V}/${CARD_A}")"
PRE_B="$(sha "${V}/${CARD_B}")"
N0="$(gitv "${V}" rev-list --count HEAD)"
crash_at "${V}" partial "TAMPERED-HALF"
[ -n "$(gitv "${V}" status --porcelain)" ] || die "crash-at=partial 后工作树应是崩溃态（有改动）"
[ "$(scanfield "${V}" open)" = "1" ] || die "崩溃后应有 1 个未闭合事务"

RC="$(codev "${V}" config set domains lab)"
cp "${WORK}/out.txt" "${WORK}/cfg_recover.json"
[ "${RC}" = "0" ] || { cat "${WORK}/cfg_recover.json"; die "恢复后 config set 应退 0，实际 ${RC}"; }
[ "$(jget "${WORK}/cfg_recover.json" 'wcodes.count("W26")')" = "1" ] ||
  { cat "${WORK}/cfg_recover.json"; die "config set 必须过 S2 并留**恰一条** W26"; }
[ "$(sha "${V}/${CARD_A}")" = "${PRE_A}" ] || die "S2 必须把 Alpha 卡逐字回滚到前像"
[ "$(sha "${V}/${CARD_B}")" = "${PRE_B}" ] || die "S2 必须把 Beta 卡逐字回滚到前像"
[ "$(scanfield "${V}" open)" = "0" ] || die "config set 之后未闭合事务应为 0"
if gitv "${V}" grep -q "TAMPERED-HALF" HEAD -- 2>/dev/null; then
  gitv "${V}" show --name-only --oneline HEAD
  die "P0 后果面：半应用坏态字节被 config set 提交进了 HEAD"
fi
grep -q "^  - lab$" "${V}/evergreen.yml" || die "恢复之后 config set 本身仍应生效（domains 追加 lab）"
[ "$(gitv "${V}" rev-list --count HEAD)" = "$((N0 + 1))" ] ||
  die "config set 应恰产生一次 commit：$(gitv "${V}" rev-list --count HEAD) vs $((N0 + 1))"
[ -z "$(gitv "${V}" status --porcelain)" ] || die "config set 之后工作树应干净（回滚 + 提交都已完成）"
ok "config set：退 0 + 恰一条 W26 + 两卡逐字回前像 + 坏态未进 HEAD + 配置生效 + 事务闭合"

# ---------------------------------------------------------------- 4. E 恢复幂等
step "E：紧接的第二条 config set 不再产 W26（恢复只发生一次）"
RC="$(codev "${V}" config set domains ops)"
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "第二条 config set 应退 0，实际 ${RC}"; }
[ "$(jget "${WORK}/out.txt" 'wcodes.count("W26")')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "恢复已完成，第二条命令不得再产 W26"; }
ok "恢复幂等：第二条 config set 退 0 且零 W26"

# ---------------------------------------------------------------- 5. C 恢复屏障（init 幂等路径）
step "C（A-55）：半应用崩溃态下 eg init 同样必须先恢复，且不改既存文件字节"
V="$(fresh c_recover_init)"
PRE_A="$(sha "${V}/${CARD_A}")"
PRE_CFG="$(sha "${V}/evergreen.yml")"
crash_at "${V}" partial "TAMPERED-INIT"
RC="$(codev "${V}" init --domain tech)"
cp "${WORK}/out.txt" "${WORK}/init_recover.json"
[ "${RC}" = "0" ] || { cat "${WORK}/init_recover.json"; die "恢复后 init 应退 0，实际 ${RC}"; }
[ "$(jget "${WORK}/init_recover.json" 'wcodes.count("W26")')" = "1" ] ||
  { cat "${WORK}/init_recover.json"; die "init 必须过 S2 并留**恰一条** W26"; }
[ "$(sha "${V}/${CARD_A}")" = "${PRE_A}" ] || die "S2 必须把 Alpha 卡逐字回滚到前像"
[ "$(sha "${V}/evergreen.yml")" = "${PRE_CFG}" ] || die "init 幂等：已存在的 evergreen.yml 一个字节都不改"
[ "$(scanfield "${V}" open)" = "0" ] || die "init 之后未闭合事务应为 0"
if gitv "${V}" grep -q "TAMPERED-INIT" HEAD -- 2>/dev/null; then
  die "P0 后果面：半应用坏态字节被 init 提交进了 HEAD"
fi
[ -z "$(gitv "${V}" status --porcelain)" ] || die "init 之后工作树应干净"
ok "init（幂等路径）：退 0 + 恰一条 W26 + 前像回滚 + 配置字节不变 + 坏态未进 HEAD"

# ---------------------------------------------------------------- 6. D 首次引导豁免
step "D（F1 [S1] 列）：全新目录 eg init 退 0，且不替用户建索引（无 .index/eg.db）"
NEWV="${WORK}/fresh_boot"
mkdir -p "${NEWV}"
RC="$(codev "${NEWV}" init --domain tech)"
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "全新目录 eg init 应退 0，实际 ${RC}"; }
[ -f "${NEWV}/evergreen.yml" ] || die "init 应生成 evergreen.yml"
[ -d "${NEWV}/.git" ] || die "init 应生成 .git/"
[ ! -f "${NEWV}/.index/eg.db" ] || die "init 不得替用户建索引（.index/eg.db 属 S4 / eg index build）"
[ "$(gitv "${NEWV}" rev-list --count HEAD)" = "1" ] || die "首次 init 应恰一次 commit"
ok "首次引导：退 0 + 骨架就绪 + 无 eg.db + 恰一次 commit"

printf '\n===== C1 · I-…-021 判据全部通过（%d 项断言）=====\n' "${PASS}"
