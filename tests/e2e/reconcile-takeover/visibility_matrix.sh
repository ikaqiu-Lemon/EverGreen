#!/usr/bin/env bash
# owner 裁决②（deprecated 关系端点默认隐藏 + --include-deprecated 显式筛选）端到端矩阵脚本
# —— T-evergreen.s1_main_flow-158614-061 的 E2E 交付物。
#
# 判据来源：M4 可见性合同 §3.2（四象限真值表 + 正交性）、§3.3（「对端」定义与 replaced_by 链两方向）、
# §3.4（flag 逐字、作用面恰 2 命令）、§3.5（data 键集合不扩张）、§3.5.2（Q4 口径与 Q3 正交）。
#
# 本脚本按 **G1 / G2 / G4 / G5 / G10** 造数据，分别在**无 flag / 有 flag** 下跑 `eg rel` 与
# `eg card show`（正/反向 = VisibleEndpoints 四个调用点全覆盖），逐格断言：
#   G1  deprecated 对端默认隐藏（默认视图不含 c；且产恰一条 Q4）；
#   G2  --include-deprecated 后 deprecated 对端展示且人类可读输出带 [失效]（且 Q4 消失）；
#   G4  已删除对端（deleted-active）在**任何 flag 下**都隐藏（删除维度 ⟂ deprecated 维度）；
#   G10 deprecated+deleted 对端在**任何 flag 下**都隐藏（已删除维度未被 deprecated 的 include 替换/放开）；
#   G5  replaced_by 链两方向：旧卡（deprecated）默认可见新卡（active）；
#       新卡（active）默认隐藏旧卡（deprecated），--include-deprecated 后显示（反向 relations_in）。
# 另钉三条全局不变量（所有组合都成立）：
#   · data 键序**逐字不变**：rel 恒五键、card 恒十七键（keys_unsorted 保留原文次序）；
#   · 所有查询**退出码恒 0**（读路径不因 flag 变非零）；
#   · 全程**零写入零 commit**：git status --porcelain 计数与 git log 计数逐字不变、HEAD 不动。
#
# 越界（本层一律不做）：不改扫描面、不改 Q1~Q3 语义、不测写命令、不测 flag 拒绝面（那在 G7 单测里）。
#
# 约束：离线、可重复、无外部依赖（bash / coreutils / git / go / jq）；任一断言不成立立刻非零退出；
# 全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/reconcile-takeover/visibility_matrix.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m4-visibility.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
KDIR_REL='domains/ai-infra/knowledge'

STEP=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
gitv() { git -C "${VAULT}" "$@"; }
porcelain_count() { gitv status --porcelain | wc -l | tr -d ' '; }
logcount() { gitv log --oneline | wc -l | tr -d ' '; }

# jq_json 跑一次 `eg --json <args>` 到 out.json，断言退 0，回显 out.json 路径。
run_json() {
  local out="${WORK}/out.json"
  local code=0
  eg --json "$@" >"${out}" 2>"${WORK}/err.txt" || code=$?
  [ "${code}" = "0" ] || { cat "${out}" "${WORK}/err.txt"; die "eg --json $* 退出码=${code}，期望 0"; }
  echo "${out}"
}

# targets_out / froms_in 取 data.relations_out[].target / data.relations_in[].from（升序、逗号分隔）。
targets_out() { jq -r '[.data.relations_out[].target] | sort | join(",")' "$1"; }
froms_in()    { jq -r '[.data.relations_in[].from] | sort | join(",")' "$1"; }
warn_codes()  { jq -r '[.warnings[].code] | join(",")' "$1"; }
# ── 【阶段化重钉：M5 索引降级留痕】────────────────────────────────────────
# M4 期本脚本用 `warn_codes == ""` 表达「业务语义面没有诊断」，那时读路径不接索引，
# 空集合恰好等价。T-…-067 之后读路径接索引，本脚本的语料**不建索引**，于是每条读命令都会
# 如实带上「索引缺失 → 全量扫描降级」的留痕 `W23` + `Q5`（合同 §6.2：只报不阻断，
# 结果仍以 Markdown 为准）。继续要求空集合等于要求「读路径不许如实留痕」，是拿旧事实判当期红。
# 故拆成两侧、都精确，不放宽：
#   · 业务面 warn_codes_biz()：**扣掉且仅扣掉** M5 降级二码后必须逐字为空（夹带任何其它码即红）；
#   · 索引面 degrade_codes()：降级留痕本身必须逐字恰 `W23,Q5`（多一条少一条都红，
#     顺序也钉住），即「降级这件事被正面断言」，而不是被忽略。
warn_codes_biz()  { jq -r '[.warnings[].code | select(. != "W23" and . != "Q5")] | join(",")' "$1"; }
degrade_codes()   { jq -r '[.warnings[].code | select(. == "W23" or . == "Q5")] | join(",")' "$1"; }
# assert_degraded 断言「无索引读路径的降级留痕逐字恰 W23,Q5」。
assert_degraded() { [ "$(degrade_codes "$2")" = "W23,Q5" ] ||
  { cat "$2"; die "$1：无索引读路径的降级留痕应逐字恰 W23,Q5，实际「$(degrade_codes "$2")」"; }; }
data_keys()   { jq -c '.data | keys_unsorted' "$1"; }

# assert_eq 逐字比对两个字符串。
assert_eq() { [ "$2" = "$3" ] || die "$1：期望「$3」实际「$2」"; }

REL_KEYS='["id","relations_out","relations_in","scanned_files","skipped_files"]'
CARD_KEYS='["id","title","domain","status","deprecated","created_at","updated_at","path","tags","markers","sections","unknown_sections","sources","relations_out","relations_in","deleted","unreviewed"]'

writecard() { # writecard <id> <body-frontmatter-tail>
  local id="$1"; shift
  mkdir -p "${VAULT}/${KDIR_REL}"
  {
    printf -- '---\nid: %s\n' "${id}"
    printf '%s' "$1"
    printf -- '---\n\n## 知识内容\n\n正文。\n'
  } >"${VAULT}/${KDIR_REL}/${id}.md"
}

# ---------------------------------------------------------------- 0. 构建与建库
step "构建 eg（CGO_ENABLED=0，与发布口径一致）+ 建库"
command -v jq >/dev/null || die "本脚本用 jq 解析 --json 信封"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
eg init --domain ai-infra >/dev/null 2>&1 || die "eg init 失败"
eg config set default_domain ai-infra >/dev/null 2>&1 || die "config set 失败"

# hub（active）→ supports 四个对端：b(active) / c(deprecated) / d(deleted-active) / e(deprecated+deleted)。
writecard k-20260902-b $'title: B\nstatus: active\ncreated_at: \'2026-09-02\'\nupdated_at: \'2026-09-02T10:00:00+08:00\'\nreviewed_at: \'2026-09-02T10:00:00+08:00\'\nsources: []\n'
writecard k-20260903-c $'title: C\nstatus: deprecated\ncreated_at: \'2026-09-03\'\nupdated_at: \'2026-09-03T10:00:00+08:00\'\nreviewed_at: \'2026-09-03T10:00:00+08:00\'\nsources: []\n'
writecard k-20260904-d $'title: D\nstatus: active\ncreated_at: \'2026-09-04\'\nupdated_at: \'2026-09-04T10:00:00+08:00\'\nreviewed_at: \'2026-09-04T10:00:00+08:00\'\ndeleted_at: \'2026-09-05T10:00:00+08:00\'\ndeleted_reason: 测试删除\nsources: []\n'
writecard k-20260905-e $'title: E\nstatus: deprecated\ncreated_at: \'2026-09-05\'\nupdated_at: \'2026-09-05T10:00:00+08:00\'\nreviewed_at: \'2026-09-05T10:00:00+08:00\'\ndeleted_at: \'2026-09-06T10:00:00+08:00\'\ndeleted_reason: 测试删除\nsources: []\n'
writecard k-20260910-hub $'title: HUB\nstatus: active\ncreated_at: \'2026-09-10\'\nupdated_at: \'2026-09-10T10:00:00+08:00\'\nreviewed_at: \'2026-09-10T10:00:00+08:00\'\nsources: []\nrelations:\n  - type: supports\n    target: k-20260902-b\n    reason: active peer\n  - type: supports\n    target: k-20260903-c\n    reason: deprecated peer\n  - type: supports\n    target: k-20260904-d\n    reason: deleted active peer\n  - type: supports\n    target: k-20260905-e\n    reason: deprecated+deleted peer\n'
# replaced_by 链：old(deprecated) --supports--> new(active)；old 自带结构化 replaced_by 指针。
writecard k-20260921-new $'title: NEW\nstatus: active\ncreated_at: \'2026-09-21\'\nupdated_at: \'2026-09-21T10:00:00+08:00\'\nreviewed_at: \'2026-09-21T10:00:00+08:00\'\nsources: []\n'
writecard k-20260920-old $'title: OLD\nstatus: deprecated\ncreated_at: \'2026-09-20\'\nupdated_at: \'2026-09-20T10:00:00+08:00\'\nreviewed_at: \'2026-09-20T10:00:00+08:00\'\nreplaced_by:\n  target: k-20260921-new\n  reason: 被新卡取代\nsources: []\nrelations:\n  - type: supports\n    target: k-20260921-new\n    reason: 旧结论支持新结论\n'

gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m4 可见性矩阵语料"
[ "$(porcelain_count)" = "0" ] || die "前置：工作区必须干净"
LOG_BASE="$(logcount)"
HEAD_BASE="$(gitv rev-parse HEAD)"
ok "库就绪：8 张卡，git log=${LOG_BASE}，HEAD=${HEAD_BASE}"

# ---------------------------------------------------------------- 1. G1 默认隐藏 deprecated
step "G1 deprecated 对端默认隐藏：rel / card show hub 默认视图只见 active(b)，且产恰一条 Q4"
for cmd in "rel k-20260910-hub" "card show k-20260910-hub"; do
  # shellcheck disable=SC2086
  J="$(run_json ${cmd})"
  assert_eq "G1 ${cmd} 默认 relations_out" "$(targets_out "${J}")" "k-20260902-b"
  [ "$(jq -r '[.warnings[]|select(.code=="Q4")] | length' "${J}")" = "1" ] ||
    { cat "${J}"; die "G1 ${cmd} 默认应恰一条 Q4，实际 warn=$(warn_codes "${J}")"; }
  [ "$(jq -r '.warnings[]|select(.code=="Q4")|.path' "${J}")" = "(汇总)" ] || die "G1 Q4 path 必须逐字 (汇总)"
  [ "$(jq -r '.warnings[]|select(.code=="Q4")|.op_index' "${J}")" = "-1" ] || die "G1 Q4 op_index 必须 -1"
done
ok "G1 默认视图只见 active(b)，c/d/e 全隐藏，Q4 口径逐字成立"

# ---------------------------------------------------------------- 2. G2 include 展示 + [失效]
step "G2 --include-deprecated 后 deprecated 对端展示且带 [失效]，Q4 消失（已删除对端仍隐藏）"
for cmd in "rel k-20260910-hub" "card show k-20260910-hub"; do
  # shellcheck disable=SC2086
  J="$(run_json ${cmd} --include-deprecated)"
  assert_eq "G2 ${cmd} include relations_out" "$(targets_out "${J}")" "k-20260902-b,k-20260903-c"
  [ "$(warn_codes_biz "${J}")" = "" ] ||
    die "G2 ${cmd} include 下业务面不得再产 Q4/任何其它码，实际 warn=$(warn_codes "${J}")"
  assert_degraded "G2 ${cmd} include" "${J}"
  # 人类可读输出：deprecated 对端带 [失效]
  # shellcheck disable=SC2086
  eg ${cmd} --include-deprecated >"${WORK}/human.txt" 2>/dev/null
  grep -Fq 'k-20260903-c[失效]' "${WORK}/human.txt" ||
    { cat "${WORK}/human.txt"; die "G2 ${cmd} 人类可读输出缺 c 的 [失效] 标记"; }
done
ok "G2 include 后见 [b,c] 且 c 带 [失效]、Q4 消失；d/e（已删除）仍隐藏"

# ---------------------------------------------------------------- 3. G4 删除维度正交
step "G4 已删除对端（deleted-active d）在无 flag / 有 flag 下都隐藏（删除 ⟂ deprecated）"
for mode in "" "--include-deprecated"; do
  # shellcheck disable=SC2086
  J="$(run_json rel k-20260910-hub ${mode})"
  echo "$(targets_out "${J}")" | grep -Fq 'k-20260904-d' &&
    die "G4 mode='${mode}' 竟展示了已删除对端 d：删除维度必须任何 flag 下都隐藏"
  ok "G4 mode='${mode:-默认}'：d 隐藏"
done

# ---------------------------------------------------------------- 4. G10 已删除维度未被替换
step "G10 deprecated+deleted 对端（e）在任何 flag 下都隐藏：已删除维度未被 include 放开"
for mode in "" "--include-deprecated"; do
  # shellcheck disable=SC2086
  J="$(run_json rel k-20260910-hub ${mode})"
  echo "$(targets_out "${J}")" | grep -Fq 'k-20260905-e' &&
    die "G10 mode='${mode}' 竟展示了 deprecated+deleted 对端 e：include 只放开 deprecated，不放开 deleted"
  ok "G10 mode='${mode:-默认}'：e 隐藏"
done

# ---------------------------------------------------------------- 5. G5 replaced_by 链两方向
step "G5 replaced_by 链两方向：旧卡默认可见新卡；新卡默认隐藏旧卡，include 后显示（反向）"
# 旧卡（deprecated）默认视图：正向可见新卡（active），无 Q4（其对端是 active）。
J="$(run_json rel k-20260920-old)"
assert_eq "G5 OLD 默认 relations_out" "$(targets_out "${J}")" "k-20260921-new"
[ "$(warn_codes_biz "${J}")" = "" ] ||
  die "G5 OLD 默认业务面不应产 Q4（对端为 active），实际 $(warn_codes "${J}")"
assert_degraded "G5 OLD 默认" "${J}"
# 新卡（active）默认视图：反向隐藏旧卡（deprecated），产 Q4。
J="$(run_json rel k-20260921-new)"
assert_eq "G5 NEW 默认 relations_in" "$(froms_in "${J}")" ""
[ "$(jq -r '[.warnings[]|select(.code=="Q4")]|length' "${J}")" = "1" ] || die "G5 NEW 默认应产恰一条 Q4"
# 新卡 include：反向显示旧卡。
J="$(run_json rel k-20260921-new --include-deprecated)"
assert_eq "G5 NEW include relations_in" "$(froms_in "${J}")" "k-20260920-old"
# card show 侧同样成立（反向 relations_in 走 card.go 调用点）。
J="$(run_json card show k-20260921-new)"
assert_eq "G5 NEW card 默认 relations_in" "$(froms_in "${J}")" ""
J="$(run_json card show k-20260921-new --include-deprecated)"
assert_eq "G5 NEW card include relations_in" "$(froms_in "${J}")" "k-20260920-old"
ok "G5 两方向成立：旧→新默认可见；新→旧默认隐藏、include 后显示（rel 与 card show 一致）"

# ---------------------------------------------------------------- 6. data 键序逐字不变（四组合）
step "data 键序逐字不变：rel 恒五键、card show 恒十七键（默认 / include 各一遍）"
for mode in "" "--include-deprecated"; do
  # shellcheck disable=SC2086
  J="$(run_json rel k-20260910-hub ${mode})"
  assert_eq "rel(${mode:-默认}) data 键序" "$(data_keys "${J}")" "${REL_KEYS}"
  # shellcheck disable=SC2086
  J="$(run_json card show k-20260910-hub ${mode})"
  assert_eq "card(${mode:-默认}) data 键序" "$(data_keys "${J}")" "${CARD_KEYS}"
done
ok "四组合下 data 键序逐字等于当前冻结面（rel 五键 / card 十七键），未新增第 N+1 键"

# ---------------------------------------------------------------- 7. 零写入零 commit
step "全程零写入零 commit：porcelain=0、git log 计数不变、HEAD 不动"
[ "$(porcelain_count)" = "0" ] || { gitv status --porcelain; die "查询产生了脏变更（读路径必须零写入）"; }
[ "$(logcount)" = "${LOG_BASE}" ] || die "git log 计数从 ${LOG_BASE} 变为 $(logcount)（读路径不得 commit）"
[ "$(gitv rev-parse HEAD)" = "${HEAD_BASE}" ] || die "HEAD 移动了（读路径不得改历史）"
ok "porcelain=0、git log=${LOG_BASE}、HEAD=${HEAD_BASE}：零写入零 commit"

printf '\n=== 全部 %d 步通过：G1/G2/G4/G5/G10 四象限逐格成立，data 键序逐字不变，零写入零 commit ===\n' "${STEP}"
exit 0
