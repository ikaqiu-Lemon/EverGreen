#!/usr/bin/env bash
# 批次C1 · I-…-022（P1/major）判据：**崩溃恢复之后的第一条写命令必须照常完成**
# —— 不得因为「plan.base 快照采在 S2 恢复屏障之前」而误退 3 / `status=partial` / 零写入。
#
# 判据来源（声明面，逐字）：
#   `2027-01-24-m6-atomicity-and-strict-check-contract.md` §1 **A-55**：
#     「恢复是**写命令的启动 hook**，在解析参数之后、拿锁之后、任何写入之前执行」
#     —— 恢复是对**上一次**中断事务的清理，不该让**本次**请求失败；
#   `2026-09-01-eg-cli-contract.md` §7 退出码表：`3` = 「**部分写入被跳过**」，
#     `status="partial"` = 部分成功 —— 零写入零 commit 的情形不满足该语义；
#   同合同 B3（写前复核）：`plan.base` 是「文件自 `eg context` 读取以来未变化」的版本依据
#     —— 因此**用户提供**的 base 必须原样生效（这条保护绝不能因为本修复被削弱）。
#
# 为什么需要这支 suite：D3 审计实测，`crash-at=partial`（真实回滚过文件字节）之后，
# `deprecate` / `edit` / `rel add` 这三条**自算 base** 的命令首次执行必然
# 退 3 + `status=partial` + `W6/content_hash_mismatch` + 零写入零 commit，再跑一次才成功。
# 既有 suite（`crash_recovery.sh` / `d3_txn_lock_index.sh`）一律用 `mark-reviewed` 驱动恢复，
# 刻意避开了这一面，因此缺陷可长期存活。
#
# 本 suite 锁死的判据（真实二进制 + 真实 kill -9 崩溃；事实只回读文件字节 / git / txnctl scan /
# `--json` 信封）：
#   A 崩溃后首写（自算 base 的三条命令 deprecate / edit / rel add，各自独立 vault）：
#     退 **0** + `status=completed` + 恰一条 `W26` + **零** `content_hash_mismatch`
#     + 命令语义真的生效（status 落 deprecated / 分区真的追加 / 关系真的建立）
#     + commit 恰 +1 + 事务闭合（open=0）+ 工作树干净。
#   B 干净库对照：同一条命令在无崩溃的库上退 0 且**零** `W26`（证明 A 段测的是恢复面）。
#   C **保护未被弱化**（本 suite 最重要的一段）：用户提供的 `plan.base` 一律不重算 ——
#     `eg apply` 携**故意写错**的 base 时仍必须退 3 + `content_hash_mismatch` + 目标零写入；
#     且崩溃恢复态下同样如此（恢复不给用户的过期 plan「放行」）。
#   D 第二次执行不再产 `W26`（恢复只发生一次，且 A 段的成功不是靠重试换来的）。
#
# 刻意**不**在本 suite 断言：`W28` 落桶（I-…-016）、既有改动披露（I-…-023）、
# A 类锁覆盖面（I-…-021，另有 `c1_aclass_config_init.sh`）。
#
# 约束：离线、零交互、可重复、无外部依赖（bash / coreutils / git / go / python3）；
# 一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/c1_recover_first_write.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c1-recover-first-write.XXXXXX")"
BASEV="${WORK}/base"
EG="${WORK}/eg"
TXN="${WORK}/txnctl"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 键级判定"

CARD_A="domains/tech/knowledge/k-20270412-c1r-alpha.md"
CARD_B="domains/tech/knowledge/k-20270412-c1r-beta.md"
ID_A="k-20270412-c1r-alpha"
ID_B="k-20270412-c1r-beta"

egv()  { "${EG}" --vault "$1" "${@:2}" </dev/null; }
codev() { local v="$1"; shift; local c=0; egv "${v}" "$@" --json >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gitv() { git -C "$1" "${@:2}"; }
sha()  { sha256sum "$1" | awk '{print $1}'; }
readexact() { local s; s=$(cat "$1"; printf X); printf '%s' "${s%X}"; }
scanfield() {
  "${TXN}" scan --vault "$1" 2>/dev/null | tail -1 |
    tr ' ' '\n' | awk -F= -v k="$2" '$1==k {print $2}'
}
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
# C2 · I-…-015 起：B2/B3 跳过的**归因枚举**（file_changed / content_hash_mismatch …）不再占用
# `data.errors[].code`（那里现在是编号 E24），它的家是 `report.skipped[].kind` / `.cause`。
# 因此本 suite 的取材面同步搬家：`causes` 直接读 skipped[] 的两个枚举位 —— 判据本体不变
# （B3 保护仍必须判 content_hash_mismatch），只是从「码位」改到「归因位」。
skipped = ((d.get("report") or {}).get("skipped")) or []
causes = []
for it in skipped:
    if isinstance(it, dict):
        causes.append(it.get("cause") or "")
        causes.append(it.get("kind") or "")
print(eval(sys.argv[2]))
PY
}

# crash_partial <vault> <标记前缀>：真实半应用崩溃（只 rename 了一部分目标就 kill -9）
crash_partial() {
  local v="$1" mark="$2" pa pb rdy="${WORK}/rdy.$$"
  pa=$(readexact "${v}/${CARD_A}"; printf X); pa=${pa%X}
  pb=$(readexact "${v}/${CARD_B}"; printf X); pb=${pb%X}
  rm -f "${rdy}"
  "${TXN}" crash-commit --vault "${v}" --crash-at partial --ready "${rdy}" \
    --file "${CARD_A}|false|${pa}|${mark}-A
" --file "${CARD_B}|false|${pb}|${mark}-B
" >"${WORK}/crash.log" 2>&1 &
  local p=$!
  local i
  for i in $(seq 1 100); do [ -f "${rdy}" ] && break; sleep 0.05; done
  kill -9 "${p}" 2>/dev/null || true
  wait "${p}" 2>/dev/null || true
  [ -f "${rdy}" ] || die "crash-commit 未到达崩溃点 partial（见 ${WORK}/crash.log）"
  [ "$(scanfield "${v}" open)" = "1" ] || die "崩溃后应恰有 1 个未闭合事务"
}
fresh() { local v="${WORK}/$1"; rm -rf "${v}"; cp -a "${BASEV}" "${v}"; echo "${v}"; }

# assert_recovered_ok <vault> <落地json> <说明>：崩溃后首写的公共判据
assert_recovered_ok() {
  local v="$1" j="$2" what="$3"
  [ "$(jget "${j}" 'o.get("exit_code")')" = "0" ] ||
    { cat "${j}"; die "${what}：崩溃后首写应退 0"; }
  [ "$(jget "${j}" 'o.get("status")')" = "completed" ] ||
    { cat "${j}"; die "${what}：崩溃后首写 status 应为 completed"; }
  [ "$(jget "${j}" 'wcodes.count("W26")')" = "1" ] ||
    { cat "${j}"; die "${what}：应恰一条 W26（恢复留痕）"; }
  # 双侧都查：码位（历史取材面）+ 归因位（I-…-015 之后的真去处）——两处都不许出现，是加严。
  [ "$(jget "${j}" '"content_hash_mismatch" in codes')" = "False" ] ||
    { cat "${j}"; die "${what}：恢复后不得再判 content_hash_mismatch（base 应在 S2 之后重算）"; }
  [ "$(jget "${j}" '"content_hash_mismatch" in causes')" = "False" ] ||
    { cat "${j}"; die "${what}：恢复后 report.skipped[].cause 也不得出现 content_hash_mismatch"; }
  [ "$(scanfield "${v}" open)" = "0" ] || die "${what}：命令之后未闭合事务应为 0"
  [ -z "$(gitv "${v}" status --porcelain)" ] || die "${what}：命令之后工作树应干净"
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg 与 txnctl（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${TXN}" ./tests/lib/txnctl) || die "编译 txnctl 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed
step "seed：init → config set → capture → context → apply（两张卡）"
mkdir -p "${BASEV}"
[ "$(codev "${BASEV}" init --domain tech)" = "0" ] || { cat "${WORK}/out.txt"; die "eg init 失败"; }
[ "$(codev "${BASEV}" config set default_domain tech)" = "0" ] || die "config set 失败"
BODY='C1 判据语料：崩溃恢复只清理上一次中断的事务，不应让本次请求失败。'
BODY="${BODY}${BODY}${BODY}"
printf '%s\n' "${BODY}" >"${WORK}/art.md"
[ "$(codev "${BASEV}" capture --url https://example.com/c1r --title 'C1 恢复判据原文' \
      --reason 'C1 修复批次' --body-file "${WORK}/art.md")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "eg capture 失败"; }
SRC="$(ls "${BASEV}/sources" | head -1 | sed 's/\.md$//')"
BASE_HASH="$(egv "${BASEV}" context --source "${SRC}" --json |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
cat >"${WORK}/seed_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "C1 恢复判据 seed",
  "requirement_ids": ["EG-FIX-C1"],
  "base": { "unprocessed.md": "${BASE_HASH}" },
  "ops": [
   { "op": "write_note", "source": "${SRC}", "note_id": "n-20270412-c1r", "title": "C1 恢复材料笔记",
     "sections": { "材料提炼": "- 恢复是启动 hook。\n", "Agent 分析": "- 仅用于 C1 判据。\n" },
     "output_cards": [{ "card": "${ID_A}", "mode": "新建" }, { "card": "${ID_B}", "mode": "新建" }] },
   { "op": "create_card", "card_id": "${ID_A}", "title": "C1 恢复卡 Alpha", "tags": ["fix"],
     "sources": [{ "source": "${SRC}", "note": "n-20270412-c1r", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "恢复后第一条写命令应照常完成。alphaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } },
   { "op": "create_card", "card_id": "${ID_B}", "title": "C1 恢复卡 Beta", "tags": ["fix"],
     "sources": [{ "source": "${SRC}", "note": "n-20270412-c1r", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "用户提供的 base 永不重算。betaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } }
  ] }
PLAN
[ "$(codev "${BASEV}" apply --plan "${WORK}/seed_plan.json")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "seed 用 apply 应退 0"; }
[ -z "$(gitv "${BASEV}" status --porcelain)" ] || die "seed 后基准 vault 工作树应干净"
ok "基准 vault 就绪：2 卡、工作树干净、commit 数 $(gitv "${BASEV}" rev-list --count HEAD)"

# ---------------------------------------------------------------- 2. A/deprecate
step "A1（A-55）：崩溃后首条 deprecate 必须退 0 + 恰一条 W26 + 真的生效"
V="$(fresh a_deprecate)"
PRE_A="$(sha "${V}/${CARD_A}")"
N0="$(gitv "${V}" rev-list --count HEAD)"
crash_partial "${V}" "TAMPERED-DEP"
RC="$(codev "${V}" deprecate --target "${ID_A}" --reason '崩溃后首次写入')"
cp "${WORK}/out.txt" "${WORK}/dep.json"
[ "${RC}" = "0" ] || { cat "${WORK}/dep.json"; die "崩溃后首条 deprecate 应退 0，实际 ${RC}"; }
assert_recovered_ok "${V}" "${WORK}/dep.json" "deprecate"
grep -qE "^status: *'?deprecated'?$" "${V}/${CARD_A}" ||
  { grep -m1 '^status:' "${V}/${CARD_A}"; die "deprecate 必须真的落 status: deprecated"; }
[ "$(sha "${V}/${CARD_B}")" != "" ] || die "Beta 卡不该消失"
[ "$(gitv "${V}" rev-list --count HEAD)" = "$((N0 + 1))" ] ||
  die "deprecate 应恰 +1 次 commit（实得 $(gitv "${V}" rev-list --count HEAD)，期望 $((N0 + 1))）"
if gitv "${V}" grep -q "TAMPERED-DEP" HEAD -- 2>/dev/null; then
  die "崩溃坏态字节不得进 HEAD"
fi
[ "$(sha "${V}/${CARD_A}")" != "${PRE_A}" ] || die "deprecate 生效后 Alpha 卡字节应变化"
ok "deprecate：退 0 + completed + 恰一条 W26 + 零 mismatch + status 落 deprecated + commit +1"

step "D：紧接的第二条写命令不再产 W26（恢复只发生一次）"
RC="$(codev "${V}" mark-reviewed --target "${ID_B}")"
[ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "第二条写命令应退 0，实际 ${RC}"; }
[ "$(jget "${WORK}/out.txt" 'wcodes.count("W26")')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "恢复已完成，第二条命令不得再产 W26"; }
ok "恢复幂等：第二条写命令退 0 且零 W26"

# ---------------------------------------------------------------- 3. A/edit
# Schema v2（knowledge_opinion_split · T-…-003 / 契约 D-7）后 Knowledge 模板恰三分区
# `知识内容 / 条件与边界 / 用户补充`：上面 seed 用的 v1 plan 里 `解释与依据` / `理解自检`
# 只记 I1、**不落盘**，故本步编辑的分区重钉为 v2 实存分区 `条件与边界`。
# 本步判据（崩溃后首条 edit 退 0 + 恰一条 W26 + 分区真的追加 + commit +1）与分区名无关，
# 且下面额外反证「v2 卡上不存在的存量分区必须退 3 且零写入」，覆盖面不减。
step "A2：崩溃后首条 edit 必须退 0 + 恰一条 W26 + 分区真的追加"
V="$(fresh a_edit)"
N0="$(gitv "${V}" rev-list --count HEAD)"
crash_partial "${V}" "TAMPERED-EDIT"
printf '%s\n' '- 崩溃恢复之后追加的一行判据内容。' >"${WORK}/edit_body.md"
RC="$(codev "${V}" edit --target "${ID_A}" --section '条件与边界' \
        --content "${WORK}/edit_body.md" --user-request)"
cp "${WORK}/out.txt" "${WORK}/edit.json"
[ "${RC}" = "0" ] || { cat "${WORK}/edit.json"; die "崩溃后首条 edit 应退 0，实际 ${RC}"; }
assert_recovered_ok "${V}" "${WORK}/edit.json" "edit"
grep -q '崩溃恢复之后追加的一行判据内容' "${V}/${CARD_A}" || die "edit 必须真的把内容写进分区"
[ "$(gitv "${V}" rev-list --count HEAD)" = "$((N0 + 1))" ] || die "edit 应恰 +1 次 commit"
ok "edit：退 0 + completed + 恰一条 W26 + 零 mismatch + 分区已追加 + commit +1"

step "A2'：v2 卡上编辑不存在的存量分区（解释与依据）必须退 3 + 零写入 + 零 commit"
PRE_A2="$(sha "${V}/${CARD_A}")"
N1="$(gitv "${V}" rev-list --count HEAD)"
RC="$(codev "${V}" edit --target "${ID_A}" --section '解释与依据' \
        --content "${WORK}/edit_body.md" --user-request)"
[ "${RC}" = "3" ] || { cat "${WORK}/out.txt"; die "编辑不存在的分区应退 3，实际 ${RC}"; }
[ "$(sha "${V}/${CARD_A}")" = "${PRE_A2}" ] || die "被拒的 edit 必须零字节落盘"
[ "$(gitv "${V}" rev-list --count HEAD)" = "${N1}" ] || die "被拒的 edit 必须零 commit"
ok "存量分区不存在：退 3 + 字节不变 + 零 commit（Schema v2 模板收敛后的正确处置）"

# ---------------------------------------------------------------- 4. A/rel add
step "A3：崩溃后首条 rel add 必须退 0 + 恰一条 W26 + 关系真的建立"
V="$(fresh a_reladd)"
N0="$(gitv "${V}" rev-list --count HEAD)"
crash_partial "${V}" "TAMPERED-REL"
RC="$(codev "${V}" rel add "${ID_A}" supports "${ID_B}" --reason '崩溃后首次建关系')"
cp "${WORK}/out.txt" "${WORK}/rel.json"
[ "${RC}" = "0" ] || { cat "${WORK}/rel.json"; die "崩溃后首条 rel add 应退 0，实际 ${RC}"; }
assert_recovered_ok "${V}" "${WORK}/rel.json" "rel add"
grep -q "${ID_B}" "${V}/${CARD_A}" || die "rel add 必须真的把关系写进 Alpha 卡"
[ "$(gitv "${V}" rev-list --count HEAD)" = "$((N0 + 1))" ] || die "rel add 应恰 +1 次 commit"
ok "rel add：退 0 + completed + 恰一条 W26 + 零 mismatch + 关系已建立 + commit +1"

# ---------------------------------------------------------------- 5. B 干净库对照
step "B 对照：干净库上同一批命令退 0 且零 W26（证明上面测的是恢复面）"
V="$(fresh b_clean)"
for c in "deprecate --target ${ID_A} --reason 干净库对照" \
         "mark-reviewed --target ${ID_B}"; do
  # shellcheck disable=SC2086
  RC="$(codev "${V}" ${c})"
  [ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "干净库 'eg ${c}' 应退 0，实际 ${RC}"; }
  [ "$(jget "${WORK}/out.txt" 'wcodes.count("W26")')" = "0" ] ||
    die "干净库不得出现 W26：eg ${c}"
done
ok "干净库对照：两条命令退 0 且零 W26"

# ---------------------------------------------------------------- 6. C 保护未被弱化
step "C（B3）：用户提供的 plan.base 一律不重算 —— 故意写错的 base 必须仍判 mismatch"
V="$(fresh c_userbase)"
# 与 store.ContentHash 同形（sha256:<64 hex>），但绝不可能是任何文件的真实哈希。
STALE="sha256:$(printf '0%.0s' $(seq 1 64))"
cat >"${WORK}/stale_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "过期 base 必须被拦",
  "requirement_ids": ["EG-FIX-C1"],
  "base": { "${CARD_A}": "${STALE}" },
  "ops": [
   { "op": "edit_section", "target": "${ID_A}", "section": "解释与依据",
     "content": "- 这一行绝不该被写进去。\n", "initiator": "user" }
  ] }
PLAN
PRE_A="$(sha "${V}/${CARD_A}")"
N0="$(gitv "${V}" rev-list --count HEAD)"
RC="$(codev "${V}" apply --plan "${WORK}/stale_plan.json" --user-request)"
cp "${WORK}/out.txt" "${WORK}/stale_clean.json"
[ "${RC}" = "3" ] || { cat "${WORK}/stale_clean.json"; die "干净库上过期 base 应退 3，实际 ${RC}"; }
[ "$(jget "${WORK}/stale_clean.json" '"content_hash_mismatch" in causes')" = "True" ] ||
  { cat "${WORK}/stale_clean.json"; die "过期 base 必须判 content_hash_mismatch（B3 保护）"; }
# I-…-015 加严：归因留在 skipped[].cause 的同时，errors[] 必须给出**编号域内**的码（E24），
# 让调用方能按码路由而不是猜中文 message。
[ "$(jget "${WORK}/stale_clean.json" '"E24" in codes')" = "True" ] ||
  { cat "${WORK}/stale_clean.json"; die "B3 跳过必须在 data.errors[] 给出编号码 E24（I-…-015）"; }
[ "$(sha "${V}/${CARD_A}")" = "${PRE_A}" ] || die "被拦的写入必须零字节落盘"
[ "$(gitv "${V}" rev-list --count HEAD)" = "${N0}" ] || die "被拦的写入必须零 commit"
ok "干净库：用户提供的过期 base → 退 3 + content_hash_mismatch + 零写入零 commit"

step "C'：同一条过期 base 的 plan 在**崩溃恢复态**下同样必须被拦（恢复不给过期 plan 放行）"
V="$(fresh c_userbase_crash)"
PRE_A="$(sha "${V}/${CARD_A}")"
N0="$(gitv "${V}" rev-list --count HEAD)"
crash_partial "${V}" "TAMPERED-STALE"
RC="$(codev "${V}" apply --plan "${WORK}/stale_plan.json" --user-request)"
cp "${WORK}/out.txt" "${WORK}/stale_crash.json"
[ "${RC}" = "3" ] || { cat "${WORK}/stale_crash.json"; die "崩溃态下过期 base 仍应退 3，实际 ${RC}"; }
# 与 C 段同一处置（I-…-015 取材面搬家）：归因读 skipped[].cause，另加严要求 code 落 E24。
[ "$(jget "${WORK}/stale_crash.json" '"content_hash_mismatch" in causes')" = "True" ] ||
  { cat "${WORK}/stale_crash.json"; die "崩溃态下过期 base 仍必须判 content_hash_mismatch"; }
[ "$(jget "${WORK}/stale_crash.json" '"E24" in codes')" = "True" ] ||
  { cat "${WORK}/stale_crash.json"; die "崩溃态下 B3 跳过也必须给出编号码 E24（I-…-015）"; }
[ "$(jget "${WORK}/stale_crash.json" 'wcodes.count("W26")')" = "1" ] ||
  { cat "${WORK}/stale_crash.json"; die "崩溃态下仍应恰一条 W26（恢复照做，只是本次写被拦）"; }
[ "$(sha "${V}/${CARD_A}")" = "${PRE_A}" ] || die "被拦的写入必须零字节落盘（前像已由 S2 还原）"
[ "$(gitv "${V}" rev-list --count HEAD)" = "${N0}" ] || die "被拦的写入必须零 commit"
ok "崩溃态：过期 base 仍被拦（退 3 + mismatch + 恰一条 W26 + 零写入零 commit）"

printf '\n===== C1 · I-…-022 判据全部通过（%d 项断言）=====\n' "${PASS}"
