#!/usr/bin/env bash
# D2 审计：错误码 / 退出码 / 授权 / Markdown-Git 权威一致性（system_assurance · T-…-010）。
#
# 判据来源（声明面）：
#   `2026-09-01-eg-cli-contract.md` §3 `--json` 信封 / §4 退出码 0–6 / §5 诊断条目；
#   `2026-10-10-m3-user-authorization-contract.md` 矩阵 #3、§1 反伪造条款 N-1、§7.1 X1/X2；
#   `2026-11-07-m4-consistency-and-recovery-contract.md` B3 跳过 / B4 提交失败语义；
#   `2027-01-24-m6-atomicity-and-strict-check-contract.md` §6 check 七检查、E16 锁不可用；
#   `2026-09-19-m2-query-contract.md` §6 只读零副作用；
#   EPIC「Markdown 是唯一权威来源，派生索引可删可重建、零信息损失」。
#
# 为什么需要这支 suite：D2 审计（审计方法合同 `2027-03-10-system-audit-method-contract.md`
# §2 三面对照）把下列条目判为「声明面有、实现面实测一致、但当前测试体系没有任何 suite
# 直接锁死」。一致条目同样必须落成当前系统的可执行判据 —— 否则下一轮回归无从判红。
#
# 本 suite 锁死的判据（全部在真实临时 vault 上驱动真实二进制；事实只回读文件字节 /
# git 自己 / eg 自己的 `--json` 信封，不看实现自报）：
#   A 信封 ↔ 退出码派生：进程退出码与 `exit_code` 逐一相等；`ok == (exit_code == 0)`；
#     `status` 取值 0→completed、1/2→failed（4→partial、5→failed 见 E/F 段）。
#   B Markdown 权威优于派生索引：手工改卡标题 + 插入独有词、**不重建索引** →
#     `card show` 返回手工现态、`search` 命中新词，两者带 W22（陈旧）+ Q5（降级全量扫描）；
#     `index status` 报 freshness=stale / changed_modified=1 且 health=healthy；
#     随后 `index build` → W22 消失而**答案逐字不变**（索引可重建、零信息损失）。
#   C 授权双载体：CLI 即 P-U 载体（`eg deprecate --reason` 无 `--user-request` 退 0 并落
#     `status: deprecated`，矩阵 #3 P-U ✅ / §7.1「需确认=否」）；plan 内伪造 op 级
#     `initiator: user` 而命令行不给 `--user-request` → 退 2 + W7 + 目标卡 sha256 逐字不变
#     （N-1 反伪造：文件内容不能自证授权）；`deprecate` 缺 `--reason` → 退 1（X2）。
#   D 退出码 3 / B3：base 不覆盖目标卡（content_hash_mismatch）→ 退 3 + status=partial +
#     skipped[] 非空 + 零写入零 commit。
#   E 退出码 4 / B4：Git 提交失败（pre-commit hook 强制非零）→ 退 4 + status=partial +
#     commit 数不变 + **工作树保留已写内容**（不破坏性回滚）+ report.txn_id 已分配 +
#     report.git.commit 为空。
#   F 退出码 5 / E16：外部进程持 `.index/run.lock` 的 flock → 退 5 + status=failed +
#     诊断含 E16 + 零写入零 commit + 工作树逐字不变。
#   G `eg check` error 级 finding → 退 2：注入 duplicate_id（同 ID 两处落盘）→
#     `data.check.findings[]` 含 severity=error 的 duplicate_id、退 2、零 commit；
#     `reconcile --dry-run` 同样退 2 且零 commit（只读口径）。
#   H `rel remove` 命中 / 幂等：命中退 0 + relations 目标行消失 + commit +1；
#     重复 remove 退 0 + 诊断含 W10 + commit 数不变（幂等 no-op）。
#   I 只读查询诊断码（M2 §5.1）：`--to` 幽灵目标退 0 + 信封 `Q2` + **恰一条** `Q3`，结果为空；
#     无 Q1 / Q2 时 `Q3` 不出现（反向可判）；Q1–Q3 的 `level` 恒 `warning`。
#
# 刻意**不**在本 suite 断言的六条（已登记 issue，判据随修复任务落地，避免把缺陷锁成基线）：
#   · 只读命令分页非法退 4/partial（search/card show/rel）→ I-…-008（D2 交叉复现扩证）；
#   · error 级诊断 `code` 为空 / 被塞入非码域取值 → I-…-015；
#   · 锁等待 W28 落进 `data.errors[]`、信封 `warnings[]` 空 → I-…-016；
#   · 子命令位 `--json` 遇 flag 解析失败即丢失信封 → I-…-017；
#   · `report --last` 声明覆盖 capture 而实现只认 apply → I-…-018；
#   · 提案 `execution.attempted_at` 报告投影为空（`git_commit` 恒 null 属 A-32 裁决）→ I-…-020。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / python3 / flock
# 由 python3 fcntl 提供）；一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/cli-core/d2_error_authority.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 512

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-d2-error-authority.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
LOCK_PID=""

STEP=0
PASS=0
cleanup() {
  [ -n "${LOCK_PID}" ] && kill "${LOCK_PID}" 2>/dev/null || true
  rm -rf "${WORK}"
}
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
# code <args...>：回显退出码，stdout+stderr 落 out.txt（不吞输出，便于失败时取证）
code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
log_count() { gitv log --oneline | wc -l | tr -d ' '; }
sha() { sha256sum "$1" | awk '{print $1}'; }

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 键级判定与 flock 持锁"

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# jget <json文件> <python表达式>：o = 信封根对象，打印表达式求值结果
jget() {
  python3 - "$1" "$2" <<'PY'
import json, sys
raw = open(sys.argv[1], encoding="utf-8").read()
dec, o = json.JSONDecoder(), None
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            o, _ = dec.raw_decode(raw[i:])
            break
        except ValueError:
            continue
if o is None:
    print("NOT_JSON"); sys.exit(0)
d = o.get("data") or {}
# 诊断条目在实现里分布于 data.errors[] / data.warnings[] / data.report.warnings[] / 信封 warnings[]
codes = []
for bucket in (o.get("warnings"), d.get("errors"), d.get("warnings"),
               (d.get("report") or {}).get("warnings")):
    for it in (bucket or []):
        if isinstance(it, dict):
            codes.append(it.get("code") or "")
print(eval(sys.argv[2]))
PY
}

# snapshot：权威面全量事实（工作区状态 + commit 数 + 逐文件 sha256）
snapshot() {
  gitv status --porcelain | sort
  echo "--commits $(log_count)"
  ( cd "${VAULT}" || exit 0
    for d in domains sources proposals reviews; do
      [ -d "${d}" ] && find "${d}" -type f
    done | sort | xargs -r sha256sum ) || true
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed
step "seed：走真实主链路 init → config → capture → context → apply → index build"
[ "$(code init --domain tech)" = "0" ] || die "eg init 失败"
[ "$(code config set default_domain tech)" = "0" ] || die "config set default_domain 失败"
BODY='D2 审计语料正文：错误码与退出码是对外合同，授权只能由发起进程在命令行给出，权威恒为 Markdown。'
BODY="${BODY}${BODY}${BODY}"
printf '%s\n' "${BODY}" >"${WORK}/art.md"
[ "$(code capture --url https://example.com/d2 --title 'D2 审计原文' \
      --reason 'D2 审计' --body-file "${WORK}/art.md")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "eg capture 失败"; }
SRC="$(ls "${VAULT}/sources" | head -1 | sed 's/\.md$//')"
[ -n "${SRC}" ] || die "capture 后 sources/ 为空"
BASE="$(eg context --source "${SRC}" --json |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
[ -n "${BASE}" ] || die "context 未回显 unprocessed.md 的 base hash"
cat >"${WORK}/seed_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "D2 审计 seed",
  "requirement_ids": ["EG-AUD-D2"],
  "base": { "unprocessed.md": "${BASE}" },
  "ops": [
   { "op": "write_note", "source": "${SRC}", "note_id": "n-20270325-d2", "title": "D2 审计材料笔记",
     "sections": { "材料提炼": "- 退出码是对外合同。\n", "Agent 分析": "- 仅用于 D2 判据。\n" },
     "output_cards": [{ "card": "k-20270325-d2-alpha", "mode": "新建" },
                      { "card": "k-20270325-d2-beta", "mode": "新建" }] },
   { "op": "create_card", "card_id": "k-20270325-d2-alpha", "title": "D2 审计卡 Alpha", "tags": ["audit"],
     "sources": [{ "source": "${SRC}", "note": "n-20270325-d2", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "退出码是对外合同，不可二次分配。\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } },
   { "op": "create_card", "card_id": "k-20270325-d2-beta", "title": "D2 审计卡 Beta", "tags": ["audit"],
     "sources": [{ "source": "${SRC}", "note": "n-20270325-d2", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "授权只能由发起进程在命令行给出。\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } }
  ] }
PLAN
[ "$(code apply --plan "${WORK}/seed_plan.json")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "seed 用 apply 应退 0"; }
CARD_A="${VAULT}/domains/tech/knowledge/k-20270325-d2-alpha.md"
CARD_B="${VAULT}/domains/tech/knowledge/k-20270325-d2-beta.md"
[ -f "${CARD_A}" ] && [ -f "${CARD_B}" ] || die "seed 未生成两张卡"
[ "$(code index build)" = "0" ] || die "index build 失败"
[ -z "$(gitv status --porcelain)" ] || die "seed 后工作区应干净"
ok "vault 就绪：1 篇原文 + 1 篇笔记 + 2 张卡，索引 healthy，工作区干净（commit 数 $(log_count)）"

# ---------------------------------------------------------------- 2. A 信封 ↔ 退出码
step "A 信封 ↔ 退出码派生：exit_code == 进程退出码、ok == (exit_code==0)、status 取值受控"
check_envelope() {  # <期望退出码> <期望status> <命令...>
  local want_rc="$1" want_st="$2"; shift 2
  local rc=0
  eg "$@" --json >"${WORK}/env.json" 2>&1 || rc=$?
  [ "${rc}" = "${want_rc}" ] ||
    { cat "${WORK}/env.json"; die "eg $* 期望退 ${want_rc}，实际 ${rc}"; }
  local got_rc got_ok got_st
  got_rc="$(jget "${WORK}/env.json" 'o.get("exit_code")')"
  got_ok="$(jget "${WORK}/env.json" 'o.get("ok")')"
  got_st="$(jget "${WORK}/env.json" 'o.get("status")')"
  [ "${got_rc}" = "${want_rc}" ] ||
    die "eg $*：进程退 ${want_rc} 但信封 exit_code=${got_rc}（合同 §3 信封须与退出码一致）"
  if [ "${want_rc}" = "0" ]; then
    [ "${got_ok}" = "True" ] || die "eg $*：退 0 时 ok 应为 true，实际 ${got_ok}"
  else
    [ "${got_ok}" = "False" ] || die "eg $*：非零退出时 ok 应为 false，实际 ${got_ok}"
  fi
  [ "${got_st}" = "${want_st}" ] ||
    die "eg $*：期望 status=${want_st}，实际 ${got_st}"
}
check_envelope 0 completed card show k-20270325-d2-alpha
check_envelope 0 completed search 退出码
check_envelope 0 completed check
check_envelope 1 failed   card show k-nope
check_envelope 2 failed   mark-reviewed --target k-20260101-ghost
printf '' >"${WORK}/empty.md"
check_envelope 2 failed   capture --url https://example.com/d2-empty --title '空正文' \
                                  --reason 'D2 审计' --body-file "${WORK}/empty.md"
ok "6 个样本上 exit_code / ok / status 三者与进程退出码严格一致（0→completed，1/2→failed）"

# ---------------------------------------------------------------- 3. B Markdown 权威
step "B Markdown 权威优于派生索引：手工编辑不重建索引，读路径以 Markdown 现态为准"
BEFORE_TITLE_HIT="$(eg search 桦木林 --json | python3 -c '
import json,sys
raw=sys.stdin.read(); dec=json.JSONDecoder(); o=None
for i,ch in enumerate(raw):
    if ch=="{":
        try: o,_=dec.raw_decode(raw[i:]); break
        except ValueError: continue
print(len((o.get("data") or {}).get("hits") or []))')"
[ "${BEFORE_TITLE_HIT}" = "0" ] || die "前置：独有词『桦木林』应尚未出现在库内"
python3 - "${CARD_A}" <<'PY'
import sys
p = sys.argv[1]
s = open(p, encoding="utf-8").read()
s = s.replace("D2 审计卡 Alpha", "手工改后标题 Zeta")
s = s.replace("退出码是对外合同，不可二次分配。", "手工插入的独有词 桦木林 也应可检索。")
open(p, "w", encoding="utf-8").write(s)
PY
eg card show k-20270325-d2-alpha --json >"${WORK}/show_stale.json" 2>&1 ||
  { cat "${WORK}/show_stale.json"; die "陈旧索引下 card show 应仍退 0"; }
grep -q '手工改后标题 Zeta' "${WORK}/show_stale.json" ||
  die "card show 未返回手工编辑后的标题：派生索引覆盖了 Markdown 权威"
[ "$(jget "${WORK}/show_stale.json" '"W22" in codes')" = "True" ] ||
  die "陈旧索引下 card show 未报 W22（索引已陈旧）"
eg search 桦木林 --json >"${WORK}/search_stale.json" 2>&1 ||
  { cat "${WORK}/search_stale.json"; die "陈旧索引下 search 应仍退 0"; }
STALE_HITS="$(jget "${WORK}/search_stale.json" 'len((d.get("hits") or []))')"
[ "${STALE_HITS}" = "1" ] ||
  die "陈旧索引下 search 应命中手工插入词（Markdown 权威），实际命中 ${STALE_HITS}"
[ "$(jget "${WORK}/search_stale.json" '"W22" in codes and "Q5" in codes')" = "True" ] ||
  die "陈旧索引下 search 应同时报 W22（陈旧）与 Q5（降级全量扫描）"
eg index status --json >"${WORK}/idx_stale.json" 2>&1 || die "index status 应退 0"
[ "$(jget "${WORK}/idx_stale.json" '(d.get("index") or {}).get("freshness")')" = "stale" ] ||
  die "index status 应报 freshness=stale"
[ "$(jget "${WORK}/idx_stale.json" '(d.get("index") or {}).get("health")')" = "healthy" ] ||
  die "手工编辑只使索引陈旧，不应使其 health 变坏（可重建 ≠ 已损坏）"
[ "$(jget "${WORK}/idx_stale.json" '(d.get("index") or {}).get("changed_modified")')" = "1" ] ||
  die "index status 应精确报 changed_modified=1"
# index build 的声明语义是「已存在且 healthy → no-op」（`eg index --help` 子命令表逐字）：
# 陈旧≠损坏，所以 build **不**负责收敛陈旧；收敛入口是 sync / rebuild。此处把这条易被
# 误解的分工也锁死，避免日后把「build 不收敛」当缺陷或反向把它改成隐式重建。
eg index build --json >"${WORK}/idx_build.json" 2>&1 || die "index build 应退 0"
[ "$(jget "${WORK}/idx_build.json" '(d.get("index") or {}).get("action")')" = "noop" ] ||
  die "索引存在且 healthy（仅陈旧）时 index build 应为 no-op（--help 子命令表）"
eg index status --json >"${WORK}/idx_after_build.json" 2>&1 || die "index status 应退 0"
[ "$(jget "${WORK}/idx_after_build.json" '(d.get("index") or {}).get("freshness")')" = "stale" ] ||
  die "index build 是 no-op，不应把 stale 收敛成 fresh（分工：sync / rebuild 才收敛）"
[ "$(code index sync)" = "0" ] || { cat "${WORK}/out.txt"; die "index sync 应退 0"; }
eg index status --json >"${WORK}/idx_fresh.json" 2>&1 || die "index status 应退 0"
[ "$(jget "${WORK}/idx_fresh.json" '(d.get("index") or {}).get("freshness")')" = "fresh" ] ||
  die "index sync 后应收敛为 fresh"
eg search 桦木林 --json >"${WORK}/search_fresh.json" 2>&1 || die "收敛后 search 应退 0"
FRESH_HITS="$(jget "${WORK}/search_fresh.json" 'len((d.get("hits") or []))')"
[ "${FRESH_HITS}" = "${STALE_HITS}" ] ||
  die "索引收敛改变了答案（陈旧 ${STALE_HITS} 条 vs 收敛后 ${FRESH_HITS} 条）：违反零信息损失"
[ "$(jget "${WORK}/search_fresh.json" '"W22" in codes')" = "False" ] ||
  die "index sync 后仍报 W22（陈旧未清）"
gitv add -A >/dev/null 2>&1 && gitv -c user.email=d2@example.com -c user.name=d2 \
  commit -q -m 'edit(tech): D2 手工编辑权威 Markdown（审计取证）' >/dev/null 2>&1
ok "手工改 Markdown 后读路径逐字反映现态（W22+Q5 降级），重建索引答案不变（零信息损失）"

# ---------------------------------------------------------------- 4. C 授权双载体
step "C 授权双载体：CLI 即 P-U；plan 内容不能自证授权（N-1）；--reason 必填（X2）"
SHA_B_0="$(sha "${CARD_B}")"
N0="$(log_count)"
cat >"${WORK}/forge_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "D2 伪造授权探测",
  "requirement_ids": ["EG-AUD-D2"],
  "base": {},
  "ops": [
   { "op": "deprecate", "initiator": "user", "target": "k-20270325-d2-beta",
     "reason": "plan 内自称用户发起" }
  ] }
PLAN
FORGE_RC=0
eg apply --plan "${WORK}/forge_plan.json" --json >"${WORK}/forge.json" 2>&1 || FORGE_RC=$?
[ "${FORGE_RC}" = "2" ] ||
  { cat "${WORK}/forge.json"; die "plan 内伪造 initiator: user 且命令行无 --user-request 应退 2，实际 ${FORGE_RC}"; }
[ "$(jget "${WORK}/forge.json" '"W7" in codes')" = "True" ] ||
  die "伪造授权应报 W7（状态类 op 缺用户发起授权）"
[ "$(sha "${CARD_B}")" = "${SHA_B_0}" ] || die "伪造授权路径改动了目标卡（应零写入）"
[ "$(log_count)" = "${N0}" ] || die "伪造授权路径产生了 commit（应零提交）"
[ "$(code deprecate --target k-20270325-d2-beta)" = "1" ] ||
  { cat "${WORK}/out.txt"; die "deprecate 缺 --reason 应退 1（X2 --reason 必填）"; }
[ "$(sha "${CARD_B}")" = "${SHA_B_0}" ] || die "参数非法路径改动了目标卡"
[ "$(code deprecate --target k-20270325-d2-beta --reason 'D2 用户显式路径')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "CLI 是 P-U 载体：eg deprecate --reason 应退 0（矩阵 #3、§7.1 需确认=否）"; }
grep -qE "^status: *'?deprecated'?$" "${CARD_B}" ||
  { grep -n '^status' "${CARD_B}"; die "CLI deprecate 后 status 应为 deprecated"; }
[ "$(log_count)" = "$((N0 + 1))" ] || die "CLI deprecate 应恰产生 1 个 commit"
ok "伪造 initiator 退 2/W7 零写入；缺 --reason 退 1；终端显式命令直接生效并落 deprecated"

# ---------------------------------------------------------------- 5. D 退出码 3 / B3
step "D 退出码 3 / B3：base 不覆盖目标（content_hash_mismatch）→ 退 3 + partial + 零写入"
SNAP_BEFORE="$(snapshot)"
cat >"${WORK}/stale_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "D2 B3 跳过探测",
  "requirement_ids": ["EG-AUD-D2"],
  "base": {},
  "ops": [
   { "op": "restore", "initiator": "user", "target": "k-20270325-d2-beta",
     "reason": "base 未覆盖目标卡" }
  ] }
PLAN
SKIP_RC=0
eg apply --plan "${WORK}/stale_plan.json" --user-request --json >"${WORK}/skip.json" 2>&1 || SKIP_RC=$?
[ "${SKIP_RC}" = "3" ] ||
  { cat "${WORK}/skip.json"; die "base 不覆盖目标应退 3（B3 跳过），实际 ${SKIP_RC}"; }
[ "$(jget "${WORK}/skip.json" 'o.get("status")')" = "partial" ] ||
  die "B3 跳过时 status 应为 partial"
SKIPPED_N="$(jget "${WORK}/skip.json" 'len(((d.get("report") or {}).get("skipped")) or [])')"
[ "${SKIPPED_N}" -ge 1 ] || die "B3 跳过时 report.skipped[] 应非空（可解释哪一 op 被跳过）"
[ "${SNAP_BEFORE}" = "$(snapshot)" ] || die "B3 跳过路径改动了权威面（应零写入零提交）"
ok "退 3 + status=partial + skipped[] 可解释 + 权威面逐字不变"

# ---------------------------------------------------------------- 6. E 退出码 4 / B4
step "E 退出码 4 / B4：Git 提交失败 → 退 4 + partial + 保留已写内容 + txn_id 已分配"
HOOK="${VAULT}/.git/hooks/pre-commit"
mkdir -p "$(dirname "${HOOK}")"
printf '#!/bin/sh\necho "D2 审计：强制拒绝提交" >&2\nexit 1\n' >"${HOOK}"
chmod +x "${HOOK}"
N1="$(log_count)"
FAIL_RC=0
eg rel add k-20270325-d2-alpha supports k-20270325-d2-beta --reason 'D2 B4 探测' --json \
  >"${WORK}/b4.json" 2>&1 || FAIL_RC=$?
[ "${FAIL_RC}" = "4" ] || { cat "${WORK}/b4.json"; die "Git 提交失败应退 4，实际 ${FAIL_RC}"; }
[ "$(jget "${WORK}/b4.json" 'o.get("status")')" = "partial" ] ||
  die "Git 提交失败时 status 应为 partial（磁盘已写、提交未成）"
[ "$(log_count)" = "${N1}" ] || die "提交失败却产生了 commit"
[ -n "$(gitv status --porcelain)" ] ||
  die "B4 要求保留已写内容供用户处置，实际工作树干净（发生了破坏性回滚）"
TXN="$(jget "${WORK}/b4.json" '((d.get("report") or {}).get("txn_id")) or ""')"
[ -n "${TXN}" ] && [ "${TXN}" != "None" ] ||
  die "提交失败路径 report.txn_id 应已分配（事务已开启），实际为空"
GC="$(jget "${WORK}/b4.json" 'str(((d.get("report") or {}).get("git") or {}).get("commit"))')"
[ "${GC}" = "None" ] || [ "${GC}" = "" ] ||
  die "提交失败时 report.git.commit 应为空，实际 ${GC}"
grep -q "target: 'k-20270325-d2-beta'" "${CARD_A}" ||
  die "B4 语义要求关系已落盘（只是没提交），实际卡内无该关系"
rm -f "${HOOK}"
gitv add -A >/dev/null 2>&1 && gitv -c user.email=d2@example.com -c user.name=d2 \
  commit -q -m 'relate(tech): D2 补提交 B4 遗留（审计取证）' >/dev/null 2>&1
ok "退 4 + partial + 零 commit + 内容保留在工作树 + txn_id 已分配 + git.commit 空"

# ---------------------------------------------------------------- 7. F 退出码 5 / E16
step "F 退出码 5 / E16：外部进程持 .index/run.lock → 退 5 + failed + 零写入零提交"
[ -e "${VAULT}/.index/run.lock" ] || die "前置：index build 后应存在 .index/run.lock"
python3 - "${VAULT}/.index/run.lock" >"${WORK}/lock.log" 2>&1 <<'PY' &
import fcntl, sys, time
f = open(sys.argv[1], "a+")
fcntl.flock(f, fcntl.LOCK_EX)
print("LOCKED", flush=True)
time.sleep(60)
PY
LOCK_PID=$!
for _ in $(seq 1 50); do grep -q LOCKED "${WORK}/lock.log" 2>/dev/null && break; sleep 0.1; done
grep -q LOCKED "${WORK}/lock.log" || die "外部持锁进程未就绪"
SNAP_LOCK="$(snapshot)"
LOCK_RC=0
EG_LOCK_TIMEOUT_MS=800 "${EG}" --vault "${VAULT}" rel add \
  k-20270325-d2-beta supports k-20270325-d2-alpha --reason 'D2 E16 探测' --json \
  >"${WORK}/lock.json" 2>&1 </dev/null || LOCK_RC=$?
kill "${LOCK_PID}" 2>/dev/null || true; wait "${LOCK_PID}" 2>/dev/null || true; LOCK_PID=""
[ "${LOCK_RC}" = "5" ] ||
  { cat "${WORK}/lock.json"; die "锁不可用应退 5，实际 ${LOCK_RC}"; }
[ "$(jget "${WORK}/lock.json" 'o.get("status")')" = "failed" ] ||
  die "锁不可用时 status 应为 failed（零写入，不是 partial）"
[ "$(jget "${WORK}/lock.json" '"E16" in codes')" = "True" ] ||
  die "锁不可用应给出 E16 诊断"
[ "${SNAP_LOCK}" = "$(snapshot)" ] ||
  die "锁不可用路径改动了权威面（应零写入零提交）"
ok "退 5 + status=failed + E16 + 权威面逐字不变"

# ---------------------------------------------------------------- 8. G check 退 2
step "G eg check error 级 finding → 退 2：注入 duplicate_id（同 ID 两处落盘），只读零提交"
N2="$(log_count)"
DUP="${VAULT}/domains/tech/knowledge/zz-d2-dup.md"
cp "${CARD_A}" "${DUP}"
CHECK_RC=0
eg check --json >"${WORK}/chk.json" 2>&1 || CHECK_RC=$?
[ "${CHECK_RC}" = "2" ] || { cat "${WORK}/chk.json"; die "存在 error 级 finding 应退 2，实际 ${CHECK_RC}"; }
[ "$(jget "${WORK}/chk.json" '[f.get("check") for f in (((d.get("check") or {}).get("findings")) or []) if f.get("severity")=="error"]' \
   | grep -c duplicate_id)" -ge 1 ] ||
  { cat "${WORK}/chk.json"; die "check 未把同 ID 两处落盘报为 error 级 duplicate_id"; }
[ "$(log_count)" = "${N2}" ] || die "eg check 产生了 commit（应恒零提交）"
REC_RC=0
eg reconcile --dry-run --json >"${WORK}/rec.json" 2>&1 || REC_RC=$?
[ "${REC_RC}" = "2" ] || { cat "${WORK}/rec.json"; die "reconcile --dry-run 存在 error 级 finding 应退 2，实际 ${REC_RC}"; }
[ "$(log_count)" = "${N2}" ] || die "reconcile --dry-run 产生了 commit（应零提交）"
rm -f "${DUP}"
[ "$(code check)" = "0" ] || { cat "${WORK}/out.txt"; die "移除注入文件后 check 应回到退 0"; }
ok "duplicate_id 判 error 并退 2；check / reconcile --dry-run 均零提交；移除注入后回绿"

# ---------------------------------------------------------------- 9. H rel remove
step "H rel remove：命中退 0 并落盘 + commit；重复 remove 幂等退 0 + W10 + 零 commit"
grep -q "target: 'k-20270325-d2-beta'" "${CARD_A}" || die "前置：alpha 应已有指向 beta 的关系"
N3="$(log_count)"
[ "$(code rel remove k-20270325-d2-alpha supports k-20270325-d2-beta --reason 'D2 H 段')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "命中的 rel remove 应退 0"; }
! grep -q "target: 'k-20270325-d2-beta'" "${CARD_A}" ||
  die "rel remove 命中后关系目标行应消失"
[ "$(log_count)" = "$((N3 + 1))" ] || die "rel remove 命中应恰产生 1 个 commit"
N4="$(log_count)"
eg rel remove k-20270325-d2-alpha supports k-20270325-d2-beta --reason 'D2 H 段重复' --json \
  >"${WORK}/relrm.json" 2>&1 || { cat "${WORK}/relrm.json"; die "重复 rel remove 应幂等退 0"; }
[ "$(jget "${WORK}/relrm.json" '"W10" in codes')" = "True" ] ||
  { cat "${WORK}/relrm.json"; die "未命中的 rel remove 应报 W10（幂等 no-op）"; }
[ "$(log_count)" = "${N4}" ] || die "幂等 rel remove 产生了新 commit"
ok "命中删除落盘并提交；重复删除幂等退 0 + W10 + 零 commit"

# ---------------------------------------------------------------- 10. I 只读查询诊断码
step "I 只读查询诊断码：--to 幽灵目标退 0 + Q2 + 恰一条 Q3；无 Q1/Q2 时不出现 Q3（反向可判）"
eg rel k-20270325-d2-alpha --to k-20200101-ghost --json >"${WORK}/q2.json" 2>&1 ||
  { cat "${WORK}/q2.json"; die "--to 指向不存在的 ID 应退 0（M2 §5.1：Q2 不改退出码）"; }
[ "$(jget "${WORK}/q2.json" '"Q2" in [ (w.get("code") or "") for w in (o.get("warnings") or []) ]')" = "True" ] ||
  die "--to 幽灵目标应在信封 warnings[] 给出 Q2"
[ "$(jget "${WORK}/q2.json" 'sum(1 for w in (o.get("warnings") or []) if (w.get("code") or "")=="Q3")')" = "1" ] ||
  die "有 Q2 时应追加恰一条 Q3 汇总诊断（M2 §5.1）"
[ "$(jget "${WORK}/q2.json" 'all((w.get("level") or "")=="warning" for w in (o.get("warnings") or []))')" = "True" ] ||
  die "Q1–Q3 的 level 必须是 warning"
[ "$(jget "${WORK}/q2.json" 'len((d.get("relations") or d.get("results") or []))')" = "0" ] ||
  die "--to 指向不存在的 ID 时结果应为空"
# 反向可判：H 段已删掉 alpha 的唯一关系，此时无 Q1 / Q2，Q3 必须不出现
eg rel k-20270325-d2-alpha --json >"${WORK}/q3neg.json" 2>&1 || die "无悬空引用时 rel 应退 0"
[ "$(jget "${WORK}/q3neg.json" '"Q3" in [ (w.get("code") or "") for w in (o.get("warnings") or []) ]')" = "False" ] ||
  { cat "${WORK}/q3neg.json"; die "无 Q1/Q2 时不应出现 Q3（M2 §5.1 反向可判）"; }
ok "Q2 照实留痕且不改退出码；Q3 恰一条且仅在有 Q1/Q2 时出现"

# ---------------------------------------------------------------- 11. 收尾自查
step "收尾自查：所有写操作只发生在临时目录，仓库工作树前后逐字相等"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "越界：本脚本改动了仓库工作树"; }
ok "仓库工作树前后逐字相等（写操作全部落在 ${WORK} 内）"

printf '\n=== d2_error_authority.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
