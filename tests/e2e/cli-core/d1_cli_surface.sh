#!/usr/bin/env bash
# D1 审计：22 个 `eg` 命令的表面合同与用户工作流边界（system_assurance · T-…-009）。
#
# 判据来源（声明面）：
#   `2026-09-01-eg-cli-contract.md` §1 命令面 / §3 `--json` 信封五键 / §4 退出码；
#   `2026-09-19-m2-query-contract.md` §1.6 / §6（只读命令零副作用）；
#   `2026-10-10-m3-user-authorization-contract.md`（P-A / P-U 写权限矩阵、二次确认）；
#   `2026-10-10-m3-proposal-state-contract.md`（提案状态机与 delete 的提案前置）；
#   `2027-01-17-m5-release-and-version.md`（顶层命令注册表总量 = 22）。
#
# 为什么需要这支 suite：D1 审计（审计方法合同
# `2027-03-10-system-audit-method-contract.md` §2 三面对照）发现下列「声明面有、实现面
# 实测一致、但当前测试体系没有任何 suite 直接锁死」的条目。按合同 §4「不得以差异代替
# 修复」的同款纪律，一致条目也不许只写在报告里 —— 必须落成当前系统的可执行判据，
# 否则下一轮回归无从判红。
#
# 本 suite 锁死的判据（每条都在真实临时 vault 上驱动真实二进制，事实只回读文件字节 /
# git 自己 / eg 自己的 `--json` 信封，不看实现自报）：
#   A 命令注册面：顶层 `--help` 命令区条目数恰 22；22 个命令名逐一在册；
#     每个 `eg <cmd> --help` 退 0（`--help` 合同）。
#   B `--json` 信封面：22 个命令的裸调用输出恰含 `ok/data/warnings/exit_code/status`
#     五键（无论退出码），信封不因命令而缺键。
#   C 只读零副作用：9 条只读读路径（context/search/card show/rel/report/unreviewed/
#     check/index status/bench）前后 `git status --porcelain`、commit 数、
#     `domains|sources|proposals` 全量 sha256 逐字相等。
#   D 参数面退出码 1：缺必填 / 未知 flag / 多余位置参数 / 未知子命令。
#   E 目标不存在退出码 2（零写入）：deprecate/restore/mark-reviewed/edit/replaced-by。
#   F 授权面（P-U 🔴 分区）：`eg edit` 对 `用户补充` / `理解自检` / 未知分区 / 缺
#     `--user-request` 一律退 2，且目标文件 sha256 不变。
#   G 提案 → 删除授权链：approve 缺 `--user-request` 退 2；缺 `--confirm` 退 6 且零写入
#     零 commit；delete 无 `--proposal` 退 2；引用 pending 提案退 2；引用 approved 提案
#     但缺 `--confirm` 退 6；四条件齐备退 0 并落 `deleted_at`。
#   H 幂等面：同一条关系重复 `rel add` 退 0、`relations[]` 不增条目、不产生新 commit；
#     已 active 的卡再 `restore` 退 0 且 status 不变。
#
# 刻意**不**在本 suite 断言的两条（已登记 issue，判据随修复任务落地，避免把缺陷锁成基线）：
#   · 只读命令退出码闭集 {0,1}：`eg search --limit -1` 现退 4/partial → I-…-008；
#   · 自环关系应在校验阶段退 2：现退 3/partial → I-…-010。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / python3）；
# 一切写操作只发生在 mktemp -d 目录内；任何一条断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/cli-core/d1_cli_surface.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 512

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-d1-cli-surface.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
# code <args...>：回显退出码，stdout/stderr 落 out.txt（不吞输出，便于失败时取证）
code() { local c=0; eg "$@" >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gitv() { git -C "${VAULT}" "$@"; }
log_count() { gitv log --oneline | wc -l | tr -d ' '; }

command -v python3 >/dev/null || die "本脚本用 python3 做 --json 信封键级判定"

# 越界自查基线：脚本自身可能尚未 git add（新增 suite 的首轮运行），因此比对**前后差异**
# 而不是「工作树必须干净」—— 后者会把「新文件还没提交」误判成越界。
BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# 顶层命令注册表（M5 后恰 22 条，见 2027-01-17-m5-release-and-version.md）
CMDS=(init config capture context apply search card rel report deprecate restore
      replaced-by proposal delete undelete mark-reviewed unreviewed edit reconcile
      check index bench)

# envelope_keys <json文件>：回显缺失的信封键（空 = 五键齐全）
envelope_missing() {
  python3 - "$1" <<'PY'
import json, sys
need = {"ok", "data", "warnings", "exit_code", "status"}
raw = open(sys.argv[1], encoding="utf-8").read().strip()
# 人类可读行可能与 JSON 混排：取最后一个完整 JSON 对象
dec, obj = json.JSONDecoder(), None
for i, ch in enumerate(raw):
    if ch == "{":
        try:
            obj, _ = dec.raw_decode(raw[i:])
            break
        except ValueError:
            continue
if obj is None:
    print("NOT_JSON")
else:
    print(",".join(sorted(need - set(obj))))
PY
}

# snapshot：权威面全量事实（工作区状态 + commit 数 + 逐文件 sha256）
# 只枚举**已存在**的权威目录：proposals/ 在建提案前并不存在，`find` 对缺失路径会非零退出，
# 在 `set -e` 下会把「本来就没有提案」误判成脚本失败。
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

# ---------------------------------------------------------------- 1. A 命令注册面
step "A 命令注册面：顶层 --help 命令区恰 22 条，且逐条 --help 退 0"
eg --help >"${WORK}/help.txt" 2>&1 || die "eg --help 应退 0"
python3 - "${WORK}/help.txt" >"${WORK}/listed.txt" <<'PY'
import re, sys
lines = open(sys.argv[1], encoding="utf-8").read().splitlines()
start = next(i for i, l in enumerate(lines) if re.match(r'^命令', l))
end = next(i for i, l in enumerate(lines[start:], start) if re.match(r'^全局 flag', l))
for l in lines[start + 1:end]:
    m = re.match(r'^  ([a-z][a-z-]*)', l)
    if m:
        print(m.group(1))
PY
LISTED="$(sort -u "${WORK}/listed.txt")"
LISTED_N="$(printf '%s\n' "${LISTED}" | grep -c . || true)"
[ "${LISTED_N}" = "22" ] ||
  die "顶层 --help 命令区应恰列 22 条命令，实际 ${LISTED_N} 条：$(printf '%s' "${LISTED}" | tr '\n' ' ')"
for c in "${CMDS[@]}"; do
  printf '%s\n' "${LISTED}" | grep -qx "${c}" || die "顶层 --help 未列出命令 ${c}"
done
for c in "${CMDS[@]}"; do
  [ "$(code "${c}" --help)" = "0" ] || { cat "${WORK}/out.txt"; die "eg ${c} --help 应退 0"; }
done
ok "命令区恰 22 条且与注册表逐一对应；22 个 <cmd> --help 全部退 0"

# ---------------------------------------------------------------- 2. seed vault
step "seed：走真实主链路 init → config → capture → context → apply（不手工造盘面）"
[ "$(code init --domain tech)" = "0" ] || die "eg init 失败"
[ "$(code config set default_domain tech)" = "0" ] || die "config set default_domain 失败"
BODY='D1 审计语料正文：常青目录的权威来源恒为 Markdown，派生索引恒可重建、删掉零信息损失。'
BODY="${BODY}${BODY}${BODY}"   # 撑过 200 字节，避免疑似截断告警干扰断言
printf '%s\n' "${BODY}" | "${EG}" --vault "${VAULT}" capture \
  --url https://example.com/d1 --title 'D1 审计原文' --reason 'D1 审计' --body-stdin >/dev/null 2>&1 ||
  die "eg capture 失败"
SRC="$(ls "${VAULT}/sources" | head -1 | sed 's/\.md$//')"
[ -n "${SRC}" ] || die "capture 后 sources/ 为空"
BASE="$(eg context --source "${SRC}" --json |
  python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
[ -n "${BASE}" ] || die "context 未回显 unprocessed.md 的 base hash"
# ChangePlan 落在 WORK 而非 VAULT 内：apply 按 `git add -A` 口径提交，plan 若在库内会被夹带
cat >"${WORK}/seed_plan.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "D1 审计 seed",
  "requirement_ids": ["EG-AUD-D1"],
  "base": { "unprocessed.md": "${BASE}" },
  "ops": [
   { "op": "write_note", "source": "${SRC}", "note_id": "n-20260901-d1", "title": "D1 审计材料笔记",
     "sections": { "材料提炼": "- 权威恒为 Markdown。\n", "Agent 分析": "- 仅用于表面合同断言。\n" },
     "output_cards": [{ "card": "k-20260901-d1-alpha", "mode": "新建" },
                      { "card": "k-20260901-d1-beta", "mode": "新建" }] },
   { "op": "create_card", "card_id": "k-20260901-d1-alpha", "title": "D1 审计卡 Alpha", "tags": ["audit"],
     "sources": [{ "source": "${SRC}", "note": "n-20260901-d1", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "权威恒为 Markdown。\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } },
   { "op": "create_card", "card_id": "k-20260901-d1-beta", "title": "D1 审计卡 Beta", "tags": ["audit"],
     "sources": [{ "source": "${SRC}", "note": "n-20260901-d1", "rel": "support", "reason": "原文给出依据" }],
     "sections": { "知识内容": "派生索引可重建。\n", "解释与依据": "- 依据。\n",
                   "条件与边界": "- 边界。\n", "理解自检": "- 自检？\n" } }
  ] }
PLAN
[ "$(code apply --plan "${WORK}/seed_plan.json")" = "0" ] ||
  { cat "${WORK}/out.txt"; die "seed 用 apply 应退 0"; }
[ -f "${VAULT}/domains/tech/knowledge/k-20260901-d1-alpha.md" ] || die "seed 未生成 alpha 卡"
[ -f "${VAULT}/domains/tech/knowledge/k-20260901-d1-beta.md" ] || die "seed 未生成 beta 卡"
[ "$(code index build)" = "0" ] || die "index build 失败（bench 前置要求索引 healthy）"
[ -z "$(gitv status --porcelain)" ] || die "seed 后工作区应干净（.index/ 已被 init 写进 .gitignore）"
ok "vault 就绪：1 篇原文 + 1 篇笔记 + 2 张卡，索引 healthy，工作区干净（commit 数 $(log_count)）"

# ---------------------------------------------------------------- 3. B 信封五键
step "B --json 信封面：22 个命令的裸调用恰含 ok/data/warnings/exit_code/status 五键"
for c in "${CMDS[@]}"; do
  code "${c}" --json >/dev/null || true
  miss="$(envelope_missing "${WORK}/out.txt")"
  [ -z "${miss}" ] || { cat "${WORK}/out.txt"; die "eg ${c} --json 信封缺键：${miss}"; }
done
ok "22 个命令的 --json 信封五键齐全（含参数非法/校验失败等非零路径）"

# ---------------------------------------------------------------- 4. C 只读零副作用
step "C 只读零副作用：9 条读路径前后权威面逐字相等（M2 §6）"
snapshot >"${WORK}/before.txt"
RO_OK=0
run_ro() { # $1=描述，其余=命令
  local desc="$1"; shift
  local c; c="$(code "$@")"
  [ "${c}" = "0" ] || { cat "${WORK}/out.txt"; die "只读命令应退 0：${desc}（实际 ${c}）"; }
  RO_OK=$((RO_OK + 1))
}
run_ro "context --source（只读加工上下文）" context --source "${SRC}"
run_ro "search" search 'D1'
run_ro "card show" card show k-20260901-d1-alpha
run_ro "rel 读路径" rel k-20260901-d1-alpha
run_ro "report --last" report --last
run_ro "unreviewed" unreviewed
run_ro "check" check
run_ro "index status" index status
run_ro "index status --strict" index status --strict
run_ro "bench" bench
snapshot >"${WORK}/after.txt"
diff -u "${WORK}/before.txt" "${WORK}/after.txt" >"${WORK}/ro.diff" ||
  { cat "${WORK}/ro.diff"; die "只读命令改动了权威面"; }
ok "${RO_OK} 条只读命令全部退 0，且 git 状态 / commit 数 / 全量 sha256 逐字相等"

# ---------------------------------------------------------------- 5. D 参数面退 1
step "D 参数面：缺必填 / 未知 flag / 多余位置参数 / 未知子命令一律退 1"
D1_CASES=(
  "search"
  "card show"
  "rel"
  "deprecate --reason r"
  "restore --reason r"
  "mark-reviewed"
  "replaced-by --target k-20260901-d1-alpha --reason r"
  "search D1 --nope"
  "card show k-20260901-d1-alpha extra"
  "unreviewed extra"
  "check extra"
  "index bogus"
  "proposal bogus"
  "config bogus"
)
for cs in "${D1_CASES[@]}"; do
  # shellcheck disable=SC2086
  c="$(code ${cs})"
  [ "${c}" = "1" ] || { cat "${WORK}/out.txt"; die "「eg ${cs}」应退 1，实际 ${c}"; }
done
ok "${#D1_CASES[@]} 条参数面用例全部退 1（用法错误与语义校验失败不混码）"

# ---------------------------------------------------------------- 6. E 目标不存在退 2
step "E 写命令的目标不存在：退 2 且零写入零 commit"
snapshot >"${WORK}/before.txt"
E_CASES=(
  "deprecate --target k-20260901-nope --reason r"
  "restore --target k-20260901-nope --reason r"
  "mark-reviewed --target k-20260901-nope"
  "edit --target k-20260901-nope --section 知识内容 --content x --user-request"
  "replaced-by --target k-20260901-nope --to k-20260901-d1-alpha --reason r"
)
for cs in "${E_CASES[@]}"; do
  # shellcheck disable=SC2086
  c="$(code ${cs})"
  [ "${c}" = "2" ] || { cat "${WORK}/out.txt"; die "「eg ${cs}」应退 2，实际 ${c}"; }
done
snapshot >"${WORK}/after.txt"
diff -q "${WORK}/before.txt" "${WORK}/after.txt" >/dev/null ||
  die "目标不存在的写命令产生了副作用"
ok "${#E_CASES[@]} 条目标不存在用例退 2，权威面零变化"

# ---------------------------------------------------------------- 7. F 授权面
step "F 授权面：eg edit 对 P-U 🔴 分区与缺佐证一律退 2，目标字节不变"
CARD_A="${VAULT}/domains/tech/knowledge/k-20260901-d1-alpha.md"
SHA_A="$(sha256sum "${CARD_A}" | awk '{print $1}')"
F_CASES=(
  "edit --target k-20260901-d1-alpha --section 用户补充 --content x --user-request"
  "edit --target k-20260901-d1-alpha --section 理解自检 --content x --user-request"
  "edit --target k-20260901-d1-alpha --section 不存在分区 --content x --user-request"
  "edit --target k-20260901-d1-alpha --section 知识内容 --content x"
)
for cs in "${F_CASES[@]}"; do
  # shellcheck disable=SC2086
  c="$(code ${cs})"
  [ "${c}" = "2" ] || { cat "${WORK}/out.txt"; die "「eg ${cs}」应退 2，实际 ${c}"; }
done
[ "$(sha256sum "${CARD_A}" | awk '{print $1}')" = "${SHA_A}" ] ||
  die "被拒绝的 edit 改动了目标文件字节"
ok "4 条授权面用例退 2（用户补充 / 理解自检 / 未知分区 / 缺 --user-request），目标 sha256 不变"

# ---------------------------------------------------------------- 8. G 提案→删除链
step "G 提案 → 删除授权链：2 / 6 / 0 三段码与二次确认的零写入语义"
[ "$(code proposal new --type logical_delete --target k-20260901-d1-beta --reason 'D1 审计')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "proposal new 应退 0"; }
PID="$(eg proposal list --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["proposals"][0]["id"])')"
[ -n "${PID}" ] || die "未能取到提案 id"
[ "$(code proposal approve "${PID}" --reason r)" = "2" ] ||
  { cat "${WORK}/out.txt"; die "approve 缺 --user-request 应退 2（U-12：Agent 不得自证批准）"; }
snapshot >"${WORK}/before.txt"
[ "$(code proposal approve "${PID}" --reason r --user-request)" = "6" ] ||
  { cat "${WORK}/out.txt"; die "approve 缺 --confirm 应退 6"; }
snapshot >"${WORK}/after.txt"
diff -q "${WORK}/before.txt" "${WORK}/after.txt" >/dev/null ||
  die "退 6（待二次确认）必须零写入零 commit"
[ "$(code delete --target k-20260901-d1-beta --reason r --user-request --confirm)" = "2" ] ||
  { cat "${WORK}/out.txt"; die "delete 无 --proposal 应退 2"; }
[ "$(code delete --target k-20260901-d1-beta --reason r --proposal "${PID}" --user-request --confirm)" = "2" ] ||
  { cat "${WORK}/out.txt"; die "delete 引用 pending 提案应退 2"; }
[ "$(code proposal approve "${PID}" --reason r --user-request --confirm)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "approve 四条件齐备应退 0"; }
[ "$(code delete --target k-20260901-d1-beta --reason r --proposal "${PID}" --user-request)" = "6" ] ||
  { cat "${WORK}/out.txt"; die "delete 缺 --confirm 应退 6"; }
[ "$(code delete --target k-20260901-d1-beta --reason r --proposal "${PID}" --user-request --confirm)" = "0" ] ||
  { cat "${WORK}/out.txt"; die "delete 四条件齐备应退 0"; }
grep -q '^deleted_at:' "${VAULT}/domains/tech/knowledge/k-20260901-d1-beta.md" ||
  die "delete 成功后应落 deleted_at"
ok "授权链 7 段码全部符合声明：approve 2/6/0、delete 2/2/6/0，且退 6 零写入"

# ---------------------------------------------------------------- 9. H 幂等面
step "H 幂等面：重复 rel add 不增条目不增 commit；已 active 卡再 restore 退 0"
[ "$(code rel add k-20260901-d1-alpha supports k-20260901-d1-beta --reason 'D1 审计')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "首次 rel add 应退 0"; }
N1="$(log_count)"
R1="$(grep -c "target: 'k-20260901-d1-beta'" "${CARD_A}" || true)"
[ "${R1}" = "1" ] || die "首次 rel add 后应恰 1 条 relations 目标行，实际 ${R1}"
[ "$(code rel add k-20260901-d1-alpha supports k-20260901-d1-beta --reason 'D1 审计')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "重复 rel add 应幂等退 0"; }
R2="$(grep -c "target: 'k-20260901-d1-beta'" "${CARD_A}" || true)"
[ "${R2}" = "1" ] || die "重复 rel add 产生了重复关系条目（${R2} 条）"
[ "$(log_count)" = "${N1}" ] || die "重复 rel add 产生了新 commit（幂等应零写入）"
[ "$(code restore --target k-20260901-d1-alpha --reason '已是 active')" = "0" ] ||
  { cat "${WORK}/out.txt"; die "对 active 卡 restore 应幂等退 0"; }
grep -q "^status: *'\{0,1\}active'\{0,1\}$" "${CARD_A}" ||
  die "幂等 restore 后 status 应仍为 active"
ok "重复 rel add 幂等（1 条关系、commit 数不变）；active 卡 restore 幂等退 0"

# ---------------------------------------------------------------- 10. 收尾自查
step "收尾自查：所有写操作只发生在临时目录，仓库工作树前后逐字相等"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "越界：本脚本改动了仓库工作树"; }
ok "仓库工作树前后逐字相等（写操作全部落在 ${WORK} 内）"

printf '\n=== d1_cli_surface.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
