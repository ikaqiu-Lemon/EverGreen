#!/usr/bin/env bash
# 批次C1 · I-…-023（P1/major）判据：**凡产生 commit 的写命令，都必须如实披露
# 「本次 commit 一并提交了工作区既有改动」** —— 披露口径全 CLI 统一，不允许一条命令说、
# 另一条命令闷着。
#
# 判据来源（声明面，逐字）：
#   `internal/git/doc.go:11`：「提交范围唯一口径：Add 走 `git add -A` —— 本次写入的改动
#     与**工作区已有改动一并**进本次 commit」；
#   `2026-08-31-m1-task-review-round3-final.md:133` 选定口径 **A**：「构造无关脏文件 →
#     该文件**出现在**本次 commit 的 `--name-only` 中，且报告中**如实列出**它属于既有改动
#     （例如进 `notes[]`/`git` 段说明）」。
#
# 为什么需要这支 suite：D3 审计实测，`add -A` 的行为如实存在，但**只有 `eg config set`**
# 在信封里给出了 commit 文件面，`mark-reviewed` / `capture` 这类直写命令的报告对既有改动
# 零披露 —— 用户手工编辑中的半成品被静默提交进权威 Git 历史，只有事后
# `git show --name-only` 才能发现（I-…-023）。plan 写链（deprecate 等）本来就有这条 I1
# info（`internal/cli/recover_hook.go` S7 + `tests/_staged/internal/cli/report_test.go`），
# 因此本 suite 同时把它当**回归护栏**锁住：修复不得把已有的那条披露改掉或降级。
#
# 本 suite 锁死的判据（真实二进制；事实只回读 git / `--json` 信封）：
#   A 直写命令（`mark-reviewed` / `capture`）在脏工作区上：退 0 + commit 真的含那个无关
#     文件（`--name-only`）+ 报告里**有**一条 info 级披露，点名该文件并给出既有改动计数。
#   B plan 写链（`deprecate`）同一场景：同样退 0 + 同样点名披露（既有行为，回归护栏）。
#   C `eg config set` 同一场景：退 0 + 披露（与 A / B 同一口径，不再是唯一如实的孤例）。
#   D 干净库对照：同一批命令零该披露（修复不得给干净库打噪声）。
#   E 披露必须与事实一致：点名的路径恰是 `git show --name-only` 里那个无关文件，
#     且计数与无关脏文件个数相等。
#
# 刻意**不**在本 suite 断言：选择性提交 / 工作区隔离（`add -A` 是既定声明面，本批次不改
# 提交范围）、A 类锁覆盖面（I-…-021）、恢复后首写（I-…-022）。
#
# 约束：离线、零交互、可重复、无外部依赖（bash / coreutils / git / go / python3）；
# 一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/c1_addall_disclosure.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c1-addall-disclosure.XXXXXX")"
BASEV="${WORK}/base"
EG="${WORK}/eg"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

command -v python3 >/dev/null || die "本脚本用 python3 做 JSON 键级判定"

CARD_A="domains/tech/knowledge/k-20270412-c1d-alpha.md"
CARD_B="domains/tech/knowledge/k-20270412-c1d-beta.md"
ID_A="k-20270412-c1d-alpha"
ID_B="k-20270412-c1d-beta"
DIRTY_MARK="UNRELATED-DIRTY-C1D"

egv()   { "${EG}" --vault "$1" "${@:2}" </dev/null; }
codev() { local v="$1"; shift; local c=0; egv "${v}" "$@" --json >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gitv()  { git -C "$1" "${@:2}"; }
fresh() { local v="${WORK}/$1"; rm -rf "${v}"; cp -a "${BASEV}" "${v}"; echo "${v}"; }

# dirty <vault>：在 Beta 卡上造一处**与本次写入无关**的未提交编辑。
dirty() {
  printf '\n<!-- 用户手工未提交编辑 %s -->\n' "${DIRTY_MARK}" >>"$1/${CARD_B}"
  [ -n "$(gitv "$1" status --porcelain)" ] || die "前置条件：应造出脏工作区"
}

# disclose <json> <点名路径>：报告里是否有一条**info 级**披露，同时提到「既有改动」与该路径。
# 三个诊断桶全扫（信封 warnings[] / data.warnings[] / data.report.warnings[]）：
# 披露落在哪个桶是实现选择，判据只要求「用户能在产物里看到」。
disclose() {
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
buckets = (o.get("warnings"), d.get("warnings"), (d.get("report") or {}).get("warnings"))
hits = 0
for b in buckets:
    for it in (b or []):
        if not isinstance(it, dict):
            continue
        msg = it.get("message") or ""
        if "既有改动" in msg and sys.argv[2] in msg and (it.get("level") or "") == "info":
            hits += 1
print(hits)
PY
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译 eg 失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. seed
step "seed：init → config set → capture → context → apply（两张卡）"
mkdir -p "${BASEV}"
[ "$(codev "${BASEV}" init --domain tech)" = "0" ] || { cat "${WORK}/out.txt"; die "eg init 失败"; }
[ "$(codev "${BASEV}" config set default_domain tech)" = "0" ] || die "config set 失败"
BODY='C1 判据语料：既有改动被一并提交时必须如实披露，不得静默。'
BODY="${BODY}${BODY}${BODY}"
printf '%s\n' "${BODY}" >"${WORK}/art.md"
[ "$(codev "${BASEV}" capture --url https://example.com/c1d --title 'C1 披露判据原文' \
      --reason 'C1 修复批次' --body-file "${WORK}/art.md")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "eg capture 失败"; }
SRC="$(ls "${BASEV}/sources" | head -1 | sed 's/\.md$//')"
BASE_HASH="$(egv "${BASEV}" context --source "${SRC}" --json |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
cat >"${WORK}/seed_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "C1 披露判据 seed",
  "requirement_ids": ["EG-FIX-C1"],
  "base": { "unprocessed.md": "${BASE_HASH}" },
  "ops": [
   { "op": "write_note", "source": "${SRC}", "note_id": "n-20270412-c1d", "title": "C1 披露材料笔记",
     "sections": { "材料提炼": "- 既有改动必须披露。\n", "Agent 分析": "- 仅用于 C1 判据。\n" },
     "output_cards": [{ "card": "${ID_A}", "mode": "新建" }, { "card": "${ID_B}", "mode": "新建" }] },
   { "op": "create_card", "card_id": "${ID_A}", "title": "C1 披露卡 Alpha", "tags": ["fix"],
     "sources": [{ "source": "${SRC}", "note": "n-20270412-c1d", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "直写命令也必须披露既有改动。alphaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } },
   { "op": "create_card", "card_id": "${ID_B}", "title": "C1 披露卡 Beta", "tags": ["fix"],
     "sources": [{ "source": "${SRC}", "note": "n-20270412-c1d", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "本卡只用来当无关脏文件。betaword\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } }
  ] }
PLAN
[ "$(codev "${BASEV}" apply --plan "${WORK}/seed_plan.json")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "seed 用 apply 应退 0"; }
[ -z "$(gitv "${BASEV}" status --porcelain)" ] || die "seed 后基准 vault 工作树应干净"
ok "基准 vault 就绪：2 卡、工作树干净、commit 数 $(gitv "${BASEV}" rev-list --count HEAD)"

# ---------------------------------------------------------------- 2. A1 直写：mark-reviewed
step "A1：脏工作区上的 mark-reviewed（直写）—— 无关改动进 HEAD 且必须被点名披露"
V="$(fresh a_markreviewed)"
N0="$(gitv "${V}" rev-list --count HEAD)"
dirty "${V}"
RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
cp "${WORK}/out.txt" "${WORK}/mr.json"
[ "${RC}" = "0" ] || { cat "${WORK}/mr.json"; die "mark-reviewed 应退 0，实际 ${RC}"; }
[ "$(gitv "${V}" rev-list --count HEAD)" = "$((N0 + 1))" ] || die "mark-reviewed 应恰 +1 次 commit"
gitv "${V}" show --name-only --format= HEAD | grep -Fq "${CARD_B}" ||
  { gitv "${V}" show --name-only --format= HEAD; die "add -A 口径：无关脏文件应出现在本次 commit"; }
gitv "${V}" grep -q "${DIRTY_MARK}" HEAD -- >/dev/null 2>&1 ||
  die "无关脏改动的字节应已进权威历史（这正是必须披露的事实）"
[ "$(disclose "${WORK}/mr.json" "${CARD_B}")" -ge 1 ] ||
  { cat "${WORK}/mr.json"; die "mark-reviewed 必须如实披露既有改动并点名 ${CARD_B}（口径 A）"; }
ok "mark-reviewed：无关改动进 HEAD，且报告有 info 级点名披露"

# ---------------------------------------------------------------- 3. A2 直写：capture
step "A2：脏工作区上的 capture（直写）—— 同样必须点名披露"
V="$(fresh a_capture)"
dirty "${V}"
printf '%s\n' "${BODY}" >"${WORK}/art2.md"
# 标题里的 ascii 段决定 ID 的 slug（internal/cli/capture.go:565），故 A2 必须换一个**不同**的
# ascii 段（C1D2），否则与 seed 那篇同日同 slug 撞成 `sources/s-...-c1.md` 已存在而退 1。
RC="$(codev "${V}" capture --url https://example.com/c1d-2 --title 'C1D2 披露判据第二篇' \
        --reason 'C1 修复批次' --body-file "${WORK}/art2.md")"
cp "${WORK}/out.txt" "${WORK}/cap.json"
[ "${RC}" = "0" ] || { cat "${WORK}/cap.json"; die "capture 应退 0，实际 ${RC}"; }
gitv "${V}" show --name-only --format= HEAD | grep -Fq "${CARD_B}" ||
  die "add -A 口径：capture 的 commit 同样应含无关脏文件"
[ "$(disclose "${WORK}/cap.json" "${CARD_B}")" -ge 1 ] ||
  { cat "${WORK}/cap.json"; die "capture 必须如实披露既有改动并点名 ${CARD_B}"; }
ok "capture：无关改动进 HEAD，且报告有 info 级点名披露"

# ---------------------------------------------------------------- 4. B plan 写链回归护栏
step "B：plan 写链（deprecate）的既有披露不得被改掉或降级（回归护栏）"
V="$(fresh b_deprecate)"
dirty "${V}"
RC="$(codev "${V}" deprecate --target "${ID_A}" --reason '披露判据：plan 写链')"
cp "${WORK}/out.txt" "${WORK}/dep.json"
[ "${RC}" = "0" ] || { cat "${WORK}/dep.json"; die "deprecate 应退 0，实际 ${RC}"; }
[ "$(disclose "${WORK}/dep.json" "${CARD_B}")" -ge 1 ] ||
  { cat "${WORK}/dep.json"; die "plan 写链必须保留 info 级点名披露"; }
ok "deprecate：既有的 info 级点名披露仍在"

# ---------------------------------------------------------------- 5. C config set 同口径
step "C：config set 与其余写命令同口径（不再是唯一如实的孤例）"
V="$(fresh c_configset)"
dirty "${V}"
RC="$(codev "${V}" config set default_domain tech)"
cp "${WORK}/out.txt" "${WORK}/cfg.json"
[ "${RC}" = "0" ] || { cat "${WORK}/cfg.json"; die "config set 应退 0，实际 ${RC}"; }
gitv "${V}" show --name-only --format= HEAD | grep -Fq "${CARD_B}" ||
  die "add -A 口径：config set 的 commit 同样应含无关脏文件"
[ "$(disclose "${WORK}/cfg.json" "${CARD_B}")" -ge 1 ] ||
  { cat "${WORK}/cfg.json"; die "config set 必须走与其余命令同一条 info 级点名披露"; }
ok "config set：披露口径与直写 / plan 写链一致"

# ---------------------------------------------------------------- 6. D 干净库零噪声
step "D 干净库对照：同一批命令零披露（修复不得给干净库打噪声）"
V="$(fresh d_clean)"
for c in "mark-reviewed --target ${ID_A}" "deprecate --target ${ID_B} --reason 干净库对照" \
         "config set default_domain tech"; do
  # shellcheck disable=SC2086
  RC="$(codev "${V}" ${c})"
  [ "${RC}" = "0" ] || { cat "${WORK}/out.txt"; die "干净库 'eg ${c}' 应退 0，实际 ${RC}"; }
  [ "$(disclose "${WORK}/out.txt" "${CARD_B}")" = "0" ] ||
    { cat "${WORK}/out.txt"; die "干净库不得出现既有改动披露：eg ${c}"; }
done
ok "干净库：三条命令退 0 且零披露"

# ---------------------------------------------------------------- 7. E 披露与事实一致
step "E：披露的计数与点名路径恰与 git show --name-only 的既有改动面一致"
V="$(fresh e_consistency)"
dirty "${V}"
# 再造第二处无关脏改动（未跟踪文件也算既有改动，一并进 commit）。
printf 'C1 披露判据：未跟踪的既有文件同样会被 add -A 带进 commit。\n' >"${V}/domains/tech/knowledge/untracked-c1d.md"
RC="$(codev "${V}" mark-reviewed --target "${ID_A}")"
cp "${WORK}/out.txt" "${WORK}/cons.json"
[ "${RC}" = "0" ] || { cat "${WORK}/cons.json"; die "mark-reviewed 应退 0，实际 ${RC}"; }
[ "$(disclose "${WORK}/cons.json" "${CARD_B}")" -ge 1 ] || die "披露应点名改动的 Beta 卡"
[ "$(disclose "${WORK}/cons.json" "domains/tech/knowledge/untracked-c1d.md")" -ge 1 ] ||
  { cat "${WORK}/cons.json"; die "披露应点名未跟踪的既有文件（它也被一并提交了）"; }
CNT="$(python3 - "${WORK}/cons.json" <<'PY'
import json, re, sys
raw = open(sys.argv[1], encoding="utf-8").read()
dec, o = json.JSONDecoder(), None
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            o, _ = dec.raw_decode(raw[i:]); break
        except ValueError:
            continue
d = o.get("data") if isinstance(o.get("data"), dict) else {}
best = 0
for b in (o.get("warnings"), d.get("warnings"), (d.get("report") or {}).get("warnings")):
    for it in (b or []):
        if isinstance(it, dict) and "既有改动" in (it.get("message") or ""):
            m = re.search(r"(\d+)\s*处", it["message"])
            if m:
                best = max(best, int(m.group(1)))
print(best)
PY
)"
[ "${CNT}" = "2" ] || { cat "${WORK}/cons.json"; die "披露计数应为 2（改动 1 + 未跟踪 1），实际 ${CNT}"; }
for f in "${CARD_B}" "domains/tech/knowledge/untracked-c1d.md"; do
  gitv "${V}" show --name-only --format= HEAD | grep -Fq "${f}" ||
    die "被披露的 ${f} 必须真的在本次 commit 里（披露不得凭空点名）"
done
ok "披露与事实一致：计数 2 + 两个路径都真的进了本次 commit"

printf '\n===== C1 · I-…-023 判据全部通过（%d 项断言）=====\n' "${PASS}"
