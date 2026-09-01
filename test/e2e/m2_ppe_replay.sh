#!/usr/bin/env bash
# AIME PPE 端到端链路的**离线回放**脚本（T-evergreen.s1_main_flow-158614-028）。
#
# 本脚本回放「收录 → 查询 → 判断结论落盘 → 写入 → 再查询验证」五步全链路，
# 并对留痕目录 `test/e2e/testdata/ppe/` 与证据报告
# `teamwork/projects/evergreen/s1_main_flow/docs/specs/2026-10-06-m2-ppe-agent-e2e-evidence.md`
# 做交叉核对。回放用的 ChangePlan **只有一个来源**：`test/e2e/testdata/ppe/plan.json`
# （本脚本正文不含任何 ChangePlan 字面量，反证见 §2 与 T-…-028 Acceptance C 段）。
#
# 回放不调用任何模型、不做网络请求、不依赖 PPE API：语义判断已凝固在 plan.json 里，
# 因此可离线重复执行、可逐字复算。
#
# 诚实边界（务必读）：本脚本证明的是**链路可离线复算**，不证明「计划由真实 Agent 生成」。
# 后者是外部环境事实，只能由 `testdata/ppe/session.md` 与证据报告登记；本脚本第 8 步
# 反过来断言这两处的「来源登记」与「是否已执行真实会话」结论彼此一致，
# 防止有人把回放基线冒充成真实会话产物。
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出；
# 全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash test/e2e/m2_ppe_replay.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PARENT="$(cd "${REPO_ROOT}/.." && pwd)"
PPE_DIR="${REPO_ROOT}/test/e2e/testdata/ppe"
ARTICLE="${REPO_ROOT}/test/e2e/testdata/verification-key-to-ai.txt"
EVID_REL="projects/evergreen/s1_main_flow/docs/specs/2026-10-06-m2-ppe-agent-e2e-evidence.md"
EVID="${PARENT}/teamwork/${EVID_REL}"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m2-ppe.XXXXXX")"
EG="${WORK}/eg"

STEP=0
PASS=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

BEFORE_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# 会话里出现的三个 ID（与 plan.json / 留痕文件同源）。
SRC_ID="s-20261006-verification-the-key-to-ai"
NEW_CARD="k-20261006-verification-first"
OLD_CARD="k-20260918-scaling-compute"
REL_TYPE="limits"
REL_REASON="可自验证是前提，不满足该前提时既有卡的结论受限"

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
EG_VERSION="$("${EG}" --version </dev/null | head -1)"
ok "二进制就绪：${EG_VERSION}"

# ---------------------------------------------------------------- 1. 留痕完整性
step "留痕完整性：六个文件齐备且非空（plan.json ≥ 200 字节）"
for f in plan.json commands.log query-before.txt query-after.txt git-log.txt session.md; do
  [ -s "${PPE_DIR}/${f}" ] || die "留痕缺失或为空：test/e2e/testdata/ppe/${f}"
done
PLAN_BYTES="$(wc -c <"${PPE_DIR}/plan.json" | tr -d ' ')"
[ "${PLAN_BYTES}" -ge 200 ] || die "plan.json 仅 ${PLAN_BYTES} 字节（< 200）"
if grep -nE "占位串|范例值|示例值" "${PPE_DIR}/plan.json"; then die "plan.json 含占位串"; fi
ok "六个留痕文件齐备；plan.json ${PLAN_BYTES} 字节、无占位串"

step "commands.log 形态：≥ 5 行、逐行 <UTC 时间> <命令> exit=<码>、五步命令齐备"
CMD_LINES="$(wc -l <"${PPE_DIR}/commands.log" | tr -d ' ')"
[ "${CMD_LINES}" -ge 5 ] || die "commands.log 仅 ${CMD_LINES} 行（< 5）"
BAD="$(grep -vcE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z .+ exit=[0-9]+$' \
  "${PPE_DIR}/commands.log" || true)"
[ "${BAD}" = "0" ] || die "commands.log 有 ${BAD} 行不符合 <UTC 时间> <命令> exit=<码>"
for pat in 'eg capture' 'eg context' 'eg apply'; do
  grep -Fq "${pat}" "${PPE_DIR}/commands.log" || die "commands.log 缺 ${pat}"
done
grep -qE 'eg (search|card show|rel) ' "${PPE_DIR}/commands.log" || die "commands.log 缺查询命令"
[ "$(grep -c 'exit=0$' "${PPE_DIR}/commands.log")" = "${CMD_LINES}" ] ||
  die "commands.log 存在非 0 退出码的命令，需在证据报告的人工干预记录里说明"
ok "commands.log ${CMD_LINES} 行、格式全部合规、capture/context/查询/apply 齐备"

# commands.log 的命令种类序列（供第 3 步与实际回放序列逐字比对）。
label_seq() { # $1 = commands.log 路径
  sed -E 's/^[^ ]+ //; s/ exit=[0-9]+$//' "$1" | sed -E '
    s/^eg capture .*/capture/;
    s/^eg context .*/context/;
    s/^eg search .*/search/;
    s/^eg card show .*/card_show/;
    s/^eg rel add .*/rel_add/;
    s/^eg rel [^-].*/rel/;
    s/^eg apply .*/apply/'
}

# ---------------------------------------------------------------- 2. 计划来源唯一
step "计划来源唯一：只从 testdata/ppe/plan.json 读，脚本正文零 ChangePlan 字面量"
SELF="${BASH_SOURCE[0]}"
[ "$(grep -c 'testdata/ppe/plan.json' "${SELF}")" -ge 1 ] || die "脚本未引用 testdata/ppe/plan.json"
if grep -nE '"(plan_version|ops)"[[:space:]]*:' "${SELF}"; then
  die "脚本正文出现 ChangePlan 字面量，计划来源必须唯一"
fi
if grep -nE '"op"[[:space:]]*:[[:space:]]*"' "${SELF}"; then
  die "脚本正文出现 op 字面量，计划来源必须唯一"
fi
ok "计划来源唯一（引用 1 处、字面量 0 处）"

# ---------------------------------------------------------------- 3. 回放五步链路
# replay OUT_DIR：在全新临时 vault 里回放五步，产物写入 OUT_DIR。
replay() {
  local out="$1"
  local vault="${out}/vault"
  mkdir -p "${out}"
  : >"${out}/labels.txt"

  local rc
  run() { # run <label> <命令…>
    local label="$1"; shift
    rc=0
    "${EG}" --vault "${vault}" "$@" </dev/null >"${out}/last.out" 2>"${out}/last.err" || rc=$?
    printf '%s\n' "${label}" >>"${out}/labels.txt"
    [ "${rc}" = "0" ] || { cat "${out}/last.err" >&2; die "回放命令 ${label} 退出码 ${rc}"; }
  }

  # —— 环境准备（属会话前的环境交付物，不计入会话命令序列）——
  "${EG}" --vault "${vault}" init --domain ai-infra </dev/null >/dev/null
  "${EG}" --vault "${vault}" config set domains ai-infra,ops </dev/null >/dev/null
  "${EG}" --vault "${vault}" config set default_domain ai-infra </dev/null >/dev/null
  mkdir -p "${vault}/domains/ai-infra/knowledge"
  cp "${PPE_DIR}/seed-${OLD_CARD}.md" "${vault}/domains/ai-infra/knowledge/${OLD_CARD}.md"
  rm -rf "${vault}/.git"
  git -C "${vault}" init -q
  git -C "${vault}" config user.name eg
  git -C "${vault}" config user.email eg@example.com

  # —— 第 1 步：收录 ——
  run capture capture --url http://www.incompleteideas.net/IncIdeas/KeytoAI.html \
    --title "Verification, The Key to AI" --body-file "${ARTICLE}" \
    --reason "AIME PPE 端到端语料" --domain ai-infra --captured-at 2026-10-06T09:00:00+08:00
  # —— 第 2 步：查询（取 base 与候选卡，并复核既有卡与既有关系）——
  run context context --source "${SRC_ID}"
  run search search 算力
  run card_show card show "${OLD_CARD}"
  run rel rel "${OLD_CARD}"
  # —— 第 3 / 4 步：判断结论落盘 + 写入（计划来自留痕目录，逐字使用）——
  run apply apply --plan "${PPE_DIR}/plan.json"
  # —— 写入前查询（同一条查询命令、同一渲染模式）——
  run rel rel "${NEW_CARD}"
  cp "${out}/last.out" "${out}/query-before.txt"
  # —— 第 5 步：写入关系 + 再查询验证 ——
  run rel_add rel add "${NEW_CARD}" "${REL_TYPE}" "${OLD_CARD}" --reason "${REL_REASON}"
  run rel rel "${NEW_CARD}"
  cp "${out}/last.out" "${out}/query-after.txt"

  git -C "${vault}" log --oneline >"${out}/git-log.txt"
  git -C "${vault}" log --reverse --pretty=%s >"${out}/subjects.txt"
  git -C "${vault}" status --porcelain >"${out}/vault-status.txt"
}

step "第一遍回放：capture → 查询 → apply → 查询 → rel add → 再查询"
replay "${WORK}/r1"
ok "五步全部退 0（含 eg apply --plan testdata/ppe/plan.json）"

step "动词序列：git log 恰 3 条，逐字为 capture( → process( → relate("
[ "$(wc -l <"${WORK}/r1/git-log.txt" | tr -d ' ')" = "3" ] ||
  { cat "${WORK}/r1/git-log.txt"; die "git log 条数必须恰为 3"; }
mapfile -t SUBJ <"${WORK}/r1/subjects.txt"
EXPECT_PREFIX=("capture(" "process(" "relate(")
for i in 0 1 2; do
  case "${SUBJ[$i]}" in
    "${EXPECT_PREFIX[$i]}"*) ;;
    *) die "第 $((i + 1)) 条 commit 主题为「${SUBJ[$i]:-<空>}」，应以 ${EXPECT_PREFIX[$i]} 开头" ;;
  esac
done
[ -z "$(cat "${WORK}/r1/vault-status.txt")" ] || die "回放结束时 vault 应干净"
ok "动词序列 capture( → process( → relate( 逐字成立；vault 干净"

step "写入前后同一条查询：写入前不含目标关系、写入后含目标关系"
REL_LINE_RE="${NEW_CARD} --${REL_TYPE}--> ${OLD_CARD}"
if grep -Fq "${REL_LINE_RE}" "${WORK}/r1/query-before.txt"; then
  die "写入前的查询输出不应含目标关系"
fi
grep -Fq "正向关系 relations_out[]：无" "${WORK}/r1/query-before.txt" ||
  { cat "${WORK}/r1/query-before.txt"; die "写入前应显示正向关系为空"; }
grep -Fq "${REL_LINE_RE}" "${WORK}/r1/query-after.txt" ||
  { cat "${WORK}/r1/query-after.txt"; die "写入后的查询输出应含目标关系"; }
ok "反正断言成立：写入前无 ${REL_TYPE} 关系、写入后有 ${REL_LINE_RE}"

step "回放产物与留痕逐字一致（query-before / query-after / 动词序列 / 命令种类序列）"
diff -u "${PPE_DIR}/query-before.txt" "${WORK}/r1/query-before.txt" ||
  die "留痕 query-before.txt 与回放输出不一致"
diff -u "${PPE_DIR}/query-after.txt" "${WORK}/r1/query-after.txt" ||
  die "留痕 query-after.txt 与回放输出不一致"
[ "$(wc -l <"${PPE_DIR}/git-log.txt" | tr -d ' ')" = "3" ] || die "留痕 git-log.txt 应为 3 行"
sed -E 's/^[0-9a-f]+ //; s/\(.*//' "${PPE_DIR}/git-log.txt" | tac >"${WORK}/log-verbs.txt"
printf 'capture\nprocess\nrelate\n' >"${WORK}/want-verbs.txt"
diff -u "${WORK}/want-verbs.txt" "${WORK}/log-verbs.txt" ||
  die "留痕 git-log.txt 的动词序列不是 capture → process → relate"
label_seq "${PPE_DIR}/commands.log" >"${WORK}/log-labels.txt"
diff -u "${WORK}/log-labels.txt" "${WORK}/r1/labels.txt" ||
  die "commands.log 的命令种类序列与实际回放序列不一致"
ok "四类留痕与回放产物逐字对齐"

# ---------------------------------------------------------------- 4. 可重复性
step "第二遍回放：动词序列与两次查询输出逐字相等"
replay "${WORK}/r2"
diff -u "${WORK}/r1/subjects.txt" "${WORK}/r2/subjects.txt" || die "两次回放的 commit 主题不一致"
diff -u "${WORK}/r1/query-before.txt" "${WORK}/r2/query-before.txt" || die "两次写入前查询输出不一致"
diff -u "${WORK}/r1/query-after.txt" "${WORK}/r2/query-after.txt" || die "两次写入后查询输出不一致"
diff -u "${WORK}/r1/labels.txt" "${WORK}/r2/labels.txt" || die "两次命令序列不一致"
ok "两遍回放逐字相等（可重复）"

# ---------------------------------------------------------------- 5. 证据报告交叉核对
step "证据报告存在且七节齐备"
[ -f "${EVID}" ] || die "缺证据报告：teamwork/${EVID_REL}"
for h in "## ① " "## ② " "## ③ " "## ④ " "## ⑤ " "## ⑥ " "## ⑦ "; do
  grep -Fq "${h}" "${EVID}" || die "证据报告缺小节 ${h}"
done
ok "证据报告七节齐备"

step "② 节的 eg --version 行与当前二进制、与 Makefile 版本号三方同值"
VER_NUM="$(printf '%s' "${EG_VERSION}" | sed -E 's/^eg ([^ ]+) .*/\1/')"
MK_VER="$(grep -E '^VERSION[[:space:]]*\?=' "${REPO_ROOT}/Makefile" |
  grep -oE '[0-9]+\.[0-9]+\.[0-9]+[^ ]*' | head -1)"
[ -n "${VER_NUM}" ] && [ "${VER_NUM}" = "${MK_VER}" ] ||
  die "二进制版本号 ${VER_NUM} 与 Makefile 版本号 ${MK_VER} 不同值"
grep -Fq "eg ${VER_NUM} (commit " "${EVID}" ||
  die "证据报告未登记 eg --version 的实际输出行（版本号 ${VER_NUM}）"
ok "版本号三方同值：${VER_NUM}"

step "③ 节命令序列表行数 == commands.log 行数"
TBL_ROWS="$(awk '/^## ③ /{f=1;next} /^## /{f=0} f && /^\| [0-9]+ \|/{n++} END{print n+0}' "${EVID}")"
[ "${TBL_ROWS}" = "${CMD_LINES}" ] ||
  die "③ 节命令行数 ${TBL_ROWS} ≠ commands.log 行数 ${CMD_LINES}"
ok "③ 节 ${TBL_ROWS} 行 == commands.log ${CMD_LINES} 行"

step "④ 节登记的 plan.json sha256 与仓内文件实测值逐字相等"
PLAN_SHA="$(sha256sum "${PPE_DIR}/plan.json" | cut -d' ' -f1)"
grep -Fq "${PLAN_SHA}" "${EVID}" || die "证据报告未登记 plan.json 的实测 sha256（${PLAN_SHA}）"
ok "sha256 一致：${PLAN_SHA:0:16}…"

step "⑤ 节动词序列与 git-log.txt 逐字一致"
grep -Fq "capture( → process( → relate(" "${EVID}" || die "⑤ 节未逐字写出动词序列"
ok "⑤ 节动词序列逐字一致"

step "⑥ 节逐字引用的关键行能在两侧留痕里 grep -F 到"
QUOTES=0
while IFS= read -r line; do
  file="$(printf '%s' "${line}" | sed -E 's/^> \[(query-(before|after)\.txt)\] .*/\1/')"
  body="$(printf '%s' "${line}" | sed -E 's/^> \[query-(before|after)\.txt\] //')"
  grep -Fq -- "${body}" "${PPE_DIR}/${file}" ||
    die "⑥ 节引用行在 ${file} 里 grep -F 不到：${body}"
  QUOTES=$((QUOTES + 1))
done < <(grep -E '^> \[query-(before|after)\.txt\] ' "${EVID}")
[ "${QUOTES}" -ge 2 ] || die "⑥ 节至少要逐字引用写入前后各一行，实际 ${QUOTES} 行"
ok "⑥ 节 ${QUOTES} 条引用行全部可在留痕里逐字命中"

step "⑦ 节：人工判定表 + 人工干预记录表在场"
grep -Fq "人工干预记录" "${EVID}" || die "⑦ 节缺人工干预记录表"
grep -Fq "证据文件:行号" "${EVID}" || die "⑦ 节人工判定表缺「证据文件:行号」列"
ok "⑦ 节两张表在场"

# ---------------------------------------------------------------- 6. 来源登记一致性
step "来源登记一致性：留痕与证据报告对「真实会话是否已执行」结论必须同真"
grep -Fq "脱敏登记" "${PPE_DIR}/session.md" || die "session.md 缺脱敏登记表"
grep -Fq "来源登记" "${PPE_DIR}/session.md" || die "session.md 缺来源登记表"
SESSION_NOT_RUN=0
if grep -Fq "真实 AIME PPE 会话未执行" "${PPE_DIR}/session.md"; then SESSION_NOT_RUN=1; fi
EVID_NOT_RUN=0
if grep -Fq "真实 AIME PPE 会话未执行" "${EVID}"; then EVID_NOT_RUN=1; fi
[ "${SESSION_NOT_RUN}" = "${EVID_NOT_RUN}" ] ||
  die "session.md 与证据报告对「真实会话是否已执行」结论不一致"
if [ "${SESSION_NOT_RUN}" = "1" ]; then
  grep -Fq "阻塞人" "${EVID}" || die "未执行真实会话时，证据报告必须写明阻塞人"
  grep -Fq "完成判据 8" "${EVID}" || die "未执行真实会话时，证据报告必须给出完成判据 8 的结论"
  grep -Fq "未达成" "${EVID}" || die "未执行真实会话时，完成判据 8 必须记为未达成"
  grep -Fq "离线回放基线" "${PPE_DIR}/session.md" ||
    die "未执行真实会话时，session.md 必须把 plan.json 登记为离线回放基线"
  printf '  [注意] 真实 AIME PPE 会话未执行：本脚本只证明链路可离线复算，完成判据 8 记为未达成。\n'
else
  grep -Fq "未经人工编辑" "${PPE_DIR}/session.md" ||
    die "已执行真实会话时，session.md 必须含「未经人工编辑」声明"
fi
ok "来源登记与证据报告结论同真（真实会话未执行=${SESSION_NOT_RUN}）"

# ---------------------------------------------------------------- 7. 工作区零污染
step "工作区零污染：脚本写操作只发生在 mktemp -d 内"
AFTER_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_STATUS}" = "${AFTER_STATUS}" ] || {
  diff <(printf '%s\n' "${BEFORE_STATUS}") <(printf '%s\n' "${AFTER_STATUS}") || true
  die "跑完脚本后 evergreen 工作区发生变化"
}
ok "evergreen 工作区与运行前逐字相同"

printf '\n=== m2_ppe_replay.sh 全部通过（%d 步 / %d 条断言）===\n' "${STEP}" "${PASS}"
