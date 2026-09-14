#!/usr/bin/env bash
# M3 `execution=failed` 逐路径报告端到端脚本（T-evergreen.s1_main_flow-158614-036）。
#
# 判据来源：提案合同 `2026-10-10-m3-proposal-state-contract.md`
#   §4.1 execution 三态与各自的必写字段
#   §4.2 status × execution 的 4 × 3 可达矩阵（approved × failed 是 ✅ 格）
#   §4.3 `execution=failed` 的两个新键（written_paths / unwritten_paths）与两侧一致性：
#        并集 == 本提案影响文件全集、交集为空、unwritten_paths ⊇ 报告 skipped[] 中属本提案者
#   §8.3 `skipped[].kind` 恰两值（复用 file_changed / content_hash_mismatch，不新造第三值）
# 以及 `2026-10-10-m3-user-authorization-contract.md` §5 的 **B4**（失败一律保留磁盘现状、
#   不做破坏性还原）与 §2.6 #39（`execution.*` 为 CLI 独占，手工改写不被采信）。
#
# 四组断言：
#   ① **部分写入 → 退 3**：同一次 apply 里第一个文件写成、第二个文件因块级凭据过期被跳过；
#   ② **逐路径报告**：`--json` 的 `execution.written_paths` / `unwritten_paths` **均非空**，
#      并集恰等于提案 targets 的影响文件全集、交集为空，`unwritten_paths` 覆盖报告
#      `skipped[]` 里属本提案的条目；
#   ③ **两侧同源（I-…-002 的形态反证）**：提案回写进 `execution` 的两个数组与报告 JSON 里的
#      两个数组**逐字相同** —— 只有一处在数路径（internal/proposal 的 PathLedger），
#      报告不再数第二遍；
#   ④ **B4 / 正交**：已写文件的改动**仍在磁盘上**、未写文件**逐字节未变**、用户分区逐字保留、
#      提案的 `status` 仍是 approved（执行结果不回写用户决定），且脚本全程不出现
#      `git checkout --` / `git reset --hard` / 删文件。
#
# 为什么用「delete 引用提案 + 同 plan 内的 replace_block 制造部分写入」：
#   `delete` 的逐文件落盘时序属 T-…-041（本 task 只提供 execution 的记录与报告能力），
#   因此本脚本不假装 delete 已经会写盘，而是让同一次 apply 里对**提案影响文件**的两次
#   真实写入尝试（一成一跳）产生事实，再断言这些事实被逐路径记进 execution 与报告。
#   这样断言的每一条都是真发生过的磁盘事实，没有任何桩。
#
# 为什么不用只读目录制造失败：本仓的 e2e 在 CI / 容器里常以 uid=0 运行，
#   root 会绕过目录权限位（chmod 555 照样能写），只读目录**制造不出**稳定的写失败；
#   块级凭据过期（base_block_hash 陈旧）是 B3 的**真实**跳过路径，逐次可复算。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/ops-diagnostics/execution_failed.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-exec.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
PLAN="${WORK}/plan.json"

KDIR_REL='domains/ai-infra/knowledge'
CARD_A="k-20260901-attention"   # 提案影响文件之一：本次**写成**
CARD_B="k-20260815-rnn"         # 提案影响文件之二：本次**未写**（块级凭据过期）
REL_A="${KDIR_REL}/${CARD_A}.md"
REL_B="${KDIR_REL}/${CARD_B}.md"
PID="p-20260701-001"
PREL="proposals/${PID}.md"
OLD_A='- 历史块：为什么需要缩放？'
BLOCK_A='- 当前有效块：多头注意力的头数如何选？'
BLOCK_B='- 当前有效块：循环网络的长程依赖如何验证？'
NEW_A='- 当前有效块：多头注意力的头数与序列长度如何权衡？'
USER_LINE='我自己的理解：先看 QKV。'

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
gitv() { git -C "${VAULT}" "$@"; }
filehash() { printf 'sha256:%s' "$(sha256sum "${VAULT}/$1" | cut -d' ' -f1)"; }
rawhash() { sha256sum "${VAULT}/$1" | cut -d' ' -f1; }
blockhash() { printf '%s' "$1" | sha256sum | cut -c1-16; }
# 本脚本模拟「用户在命令行上敲命令」，故一律带 --user-request：
# plan 里的 initiator: user 只有配上它才构成用户显式路径 P-U（反伪造条款 N-1）。
apply() { cat >"${PLAN}"; eg_code apply --plan "${PLAN}" --json --user-request; }
# paths_of 从报告 JSON 里取某个路径数组的成员（每行一条，升序）。
paths_of() { grep -o "\"$1\":\[[^]]*\]" "${WORK}/out.txt" | head -1 |
  grep -o '"[^"]*\.md"' | tr -d '"' | sort; }
# fm_seq 从提案 frontmatter 里取某个序列键的成员（每行一条，升序）。
fm_seq() { awk -v key="  $1:" '
  $0 == key { grab = 1; next }
  grab && /^    - / { sub(/^    - /, ""); gsub(/^'\''|'\''$/, ""); print; next }
  grab { grab = 0 }' "${VAULT}/${PREL}" | sort; }

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "eg init 建库 + 两张卡 + 一份已批准提案（提案子命令属 T-…-040，用夹具直落）"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/proposals"
cat >"${VAULT}/${REL_A}" <<CARD_A_EOF
---
id: ${CARD_A}
title: 注意力机制
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
---

# 注意力机制

## 知识内容

正文占位。

## 用户补充

${USER_LINE}

## 理解自检

${OLD_A}

${BLOCK_A}
CARD_A_EOF
cat >"${VAULT}/${REL_B}" <<CARD_B_EOF
---
id: ${CARD_B}
title: 循环网络的注意力局限
status: active
created_at: '2026-08-15'
updated_at: '2026-08-15T10:00:00+08:00'
---

# 循环网络的注意力局限

## 知识内容

正文占位。

## 理解自检

${BLOCK_B}
CARD_B_EOF

# 提案夹具：approved × execution.status=not_started（可达矩阵 ✅ 格），
# targets 恰两张卡 = 本次的影响文件全集；正文恰 7 个 H2（合同 §7.3，顺序固定）。
{
  printf -- '---\nid: %s\ntype: logical_delete\nstatus: approved\n' "${PID}"
  printf "created_at: '2026-07-01'\n"
  printf 'targets:\n  - %s\n  - %s\n' "${CARD_A}" "${CARD_B}"
  printf 'impact:\n  exits_default_view:\n    - %s\n  cards_losing_support: []\n' "${CARD_A}"
  printf '  affected_material_rels: 0\n  affected_relations: 0\n  stale_reviews: []\n'
  printf "decision:\n  result: 'approved'\n  reason: '用户已批准删除'\n  superseded_by: ''\n"
  printf "execution:\n  status: not_started\n  attempted_at: ''\n  reason: ''\n  git_commit: ''\n"
  printf '  written_paths: []\n  unwritten_paths: []\n'
  printf -- '---\n\n# 提案：逻辑删除两张过时卡\n'
  for sec in 推荐修改 理由与证据 '影响的文件、领域与关系' 执行后状态 不执行的影响 替代方案 可应用内容; do
    printf '\n## %s\n\n%s 的正文占位。\n' "${sec}" "${sec}"
  done
} >"${VAULT}/${PREL}"

gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 execution=failed 语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
BASE_COMMITS="$(gitv log --oneline | wc -l | tr -d ' ')"
HASH_B_BEFORE="$(rawhash "${REL_B}")"
printf '%s\n%s\n' "${REL_A}" "${REL_B}" | sort >"${WORK}/targets.txt"
ok "库就绪：2 张卡 + 1 份 approved 提案（targets 恰 2 条），基线 commit 数 ${BASE_COMMITS}"

# ---------------------------------------------------------------- 2. ① 部分写入
step "① 同一次 apply：第一个文件写成、第二个文件因块级凭据过期被跳过 → 退 3"
BH_A="$(blockhash "${BLOCK_A}")"
BH_STALE="$(blockhash '- 当前有效块：这一行从来没在 B 卡上出现过。')"
C="$(apply <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra",
 "reason":"按已批准提案处理两张卡，其中一张的块级凭据已过期",
 "base":{"${REL_A}":"$(filehash "${REL_A}")","${REL_B}":"$(filehash "${REL_B}")"},
 "ops":[{"op":"delete","target":"${CARD_A}","reason":"内容重复且无引用","initiator":"user",
         "proposal":"${PID}"},
        {"op":"replace_block","target":"${CARD_A}","section":"理解自检","initiator":"user",
         "reason":"自检问题升级","base_block_hash":"${BH_A}","block":"${NEW_A}\n"},
        {"op":"replace_block","target":"${CARD_B}","section":"理解自检","initiator":"user",
         "reason":"自检问题升级","base_block_hash":"${BH_STALE}","block":"- 当前有效块：应当被拒绝。\n"}]}
JSON
)"
[ "${C}" = "3" ] || { cat "${WORK}/out.txt"; die "部分写入应退 3，实退 ${C}"; }
grep -Fq '"kind":"file_changed"' "${WORK}/out.txt" || die "跳过的 kind 必须是 file_changed"
grep -Fq '"cause":"content_hash_mismatch"' "${WORK}/out.txt" ||
  die "跳过的 cause 必须是 content_hash_mismatch"
grep -Fq -- "${NEW_A}" "${VAULT}/${REL_A}" || die "第一个文件应已写入新块"
ok "① 退 3；第一个文件写成、第二个文件落进 skipped{file_changed / content_hash_mismatch}"

# ---------------------------------------------------------------- 3. ② 逐路径报告
step "② --json 的 execution.written_paths / unwritten_paths 均非空，并集 == 影响文件全集、交集为空"
grep -Fq '"execution":{"status":"failed"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "报告 proposals[] 里必须有 execution.status=failed"; }
paths_of written_paths >"${WORK}/w.txt"
paths_of unwritten_paths >"${WORK}/u.txt"
[ -s "${WORK}/w.txt" ] || { cat "${WORK}/out.txt"; die "written_paths 必须非空"; }
[ -s "${WORK}/u.txt" ] || { cat "${WORK}/out.txt"; die "unwritten_paths 必须非空"; }
grep -Fqx "${REL_A}" "${WORK}/w.txt" || die "写成的 ${REL_A} 必须在 written_paths 里"
grep -Fqx "${REL_B}" "${WORK}/u.txt" || die "未写的 ${REL_B} 必须在 unwritten_paths 里"
[ -z "$(comm -12 "${WORK}/w.txt" "${WORK}/u.txt")" ] || die "两个数组的交集必须为空"
cat "${WORK}/w.txt" "${WORK}/u.txt" | sort >"${WORK}/union.txt"
diff -u "${WORK}/targets.txt" "${WORK}/union.txt" ||
  die "written_paths ∪ unwritten_paths 必须恰等于提案 targets 的影响文件全集"
# unwritten_paths ⊇ 报告 skipped[] 中属本提案的条目（这里恰是 REL_B 一条）。
grep -o '"locator":"[^"]*"' "${WORK}/out.txt" | sed 's/"locator":"//;s/"$//' | sort -u >"${WORK}/sk.txt"
grep -Fqx "${REL_B}" "${WORK}/sk.txt" || { cat "${WORK}/sk.txt"; die "skipped[].locator 应能定位到未写文件"; }
while read -r loc; do
  if grep -Fqx "${loc}" "${WORK}/targets.txt" && ! grep -Fqx "${loc}" "${WORK}/u.txt"; then
    die "报告 skipped[] 里属本提案的 ${loc} 未出现在 unwritten_paths"
  fi
done <"${WORK}/sk.txt"
ok "② 逐路径齐全：已写 $(wc -l <"${WORK}/w.txt") 条、未写 $(wc -l <"${WORK}/u.txt") 条，并集 == 全集、交集为空"

# ---------------------------------------------------------------- 4. ③ 两侧同源
step "③ 提案回写的两个数组与报告 JSON 逐字相同（唯一计数来源，I-…-002 的形态反证）"
grep -Eq "^  status: '?failed'?$" "${VAULT}/${PREL}" || { sed -n '1,30p' "${VAULT}/${PREL}";
  die "提案的 execution.status 必须回写成 failed"; }
fm_seq written_paths >"${WORK}/fw.txt"
fm_seq unwritten_paths >"${WORK}/fu.txt"
diff -u "${WORK}/w.txt" "${WORK}/fw.txt" || die "提案里的 written_paths 与报告不一致（存在第二处计数）"
diff -u "${WORK}/u.txt" "${WORK}/fu.txt" || die "提案里的 unwritten_paths 与报告不一致（存在第二处计数）"
grep -Fq '未做任何还原' "${VAULT}/${PREL}" || die "execution.reason 必须写明 B4 口径（未做任何还原）"
grep -Fq "  attempted_at: '" "${VAULT}/${PREL}" || die "failed 必写 attempted_at"
ok "③ 报告与提案两侧逐字一致：已写 / 未写只有 PathLedger 一处在数"

# ---------------------------------------------------------------- 5. ④ B4 与正交
step "④ B4：已写文件的改动仍在、未写文件逐字节未变；正交：提案 status 仍是 approved"
grep -Fq -- "${NEW_A}" "${VAULT}/${REL_A}" || die "B4 被违反：已写入的改动被还原了"
grep -Fq -- "${OLD_A}" "${VAULT}/${REL_A}" || die "历史记录块必须逐字保留（只追加、永不改写）"
grep -Fq "${USER_LINE}" "${VAULT}/${REL_A}" || die "用户分区必须逐字保留（B2）"
[ "$(rawhash "${REL_B}")" = "${HASH_B_BEFORE}" ] || die "未写文件必须逐字节不变"
grep -Fq 'status: approved' "${VAULT}/${PREL}" ||
  die "两维正交：执行结果不得回写 status（应仍是 approved）"
grep -Fq "  result: 'approved'" "${VAULT}/${PREL}" || die "decision 必须逐字不变"
grep -Fq '## 可应用内容' "${VAULT}/${PREL}" || die "提案正文七分区必须逐字保留"
[ "$((BASE_COMMITS + 1))" = "$(gitv log --oneline | wc -l | tr -d ' ')" ] ||
  die "一次 apply = 一次 commit（部分成功照常提交已写内容）"
[ -z "$(gitv status --porcelain --untracked-files=no)" ] ||
  { gitv status --porcelain; die "已写内容与 execution 回写必须都在本次 commit 里"; }
# B4 的静态反证：产品代码里不得有任何破坏性 Git / 删文件动作（测试文件不参与判定）。
# 2026-09-08 随 M5 · T-…-065 按 U-02 同一先例重钉（只改**形态**，本体一格不放宽）：
# `internal/index/{build,rebuild}.go` 里的 `os.RemoveAll(dir)` 中 `dir` 恒是 `.index/`
# —— 那是**可完整重建的派生索引目录**，删掉零信息损失，属「重建」语义，
# 与 B4 / U-02 禁止的「回滚权威数据」是两件事。因此判据拆成四格，后三格是**新增的加严**：
#   ① 摘掉这两处白名单后，internal/ 非测试源的破坏性动作仍恒 0（原判据原样复算）；
#   ② `internal/index` 内不得出现 `checkout --` / `reset --hard`（Git 面一个字都不许碰）；
#   ③ `internal/index` 的 `RemoveAll` 参数只许逐字 `dir`（换成任何别的路径都要重新登记）；
#   ④ `internal/index` 非测试源不得出现权威产物根字面量或 `..`（它根本不认识权威文件在哪）。
# 2026-09-08 随 M5 · T-…-069 按同一先例再重钉一次（只改**形态**，本体一格不放宽）：
# T-…-068 纠正轮把 `eg bench` 的一次性目录 owner 收敛为 `internal/index/scratch.go` 的窄 API
# `NewScratch(prefix) (dir, cleanup, error)` —— 删除目标 `dir` **只可能**来自本包
# `os.MkdirTemp`，调用方拿到的是**已绑定该 dir 的 closure**，无从提供删除路径。
# 它删的是「本包刚亲手造出来的空目录」，与 B4 / U-02 禁止的「回滚权威数据」仍是两件事。
# 因此在原四格之外**再加四格加严**（全是新增等号，没有一格放宽）：
#   ⑤ 破坏性动作落点**集合**逐字恰 {build.go, rebuild.go, scratch.go}，且每个文件恰 1 处
#      （集合 + 每文件计数双侧锁死：多一处、少一处、换文件都要重新登记）；
#   ⑥ scratch.go 的删除目标**来源封闭**：`dir, err := os.MkdirTemp("", prefix)` 逐字恰 1 处、
#      `os.MkdirTemp` 全文恰 1 处、窄签名逐字在场，且该文件内 `dir string` 形参恰 0
#      （调用方连一个路径参数都传不进来）；
#   ⑦ 旧的「可对任意路径递归删除」形态 `RemoveScratch` 在 internal/ 与 cmd/ 的**非注释行**恒 0；
#   ⑧ 机器反证在册：scratch_test.go 的四条具名反证（含 AST 级）逐条在场。
DESTRUCT_ALL() { ( cd "${REPO_ROOT}" && grep -rnE 'checkout --|reset --hard|RemoveAll' internal/ ||
  true ) | { grep -v '_test\.go' || true; }; }
IDX_WHITELIST='^internal/index/(build|rebuild|scratch)\.go:[0-9]+:[[:space:]]*(_ = |if err := |return )os\.RemoveAll\(dir\)'
# ── C2a·M6 现态重钉（合同 §16.2 / §19 / §20 RemoveAll(dir) 禁令收紧；沿用本脚本既有「非注释行」判据形态，非放宽）──
# M6 · R9/R10 把 build/rebuild 的失败清理与重建从「RemoveAll 整个 .index/ 目录」收紧为「只删本次产物 / 逐项删非
# runtime-reserved」，故 build.go 那处历史 `os.RemoveAll(dir)` **实码已删除**，仅留一行**注释**逐字记明其
# 「在 M6 语境下永久禁用」。B4 破坏性动作本体一格不放宽——把「可执行行」与「注释说明」分离：破坏性**可执行行**
# （剔除行首注释后）减索引白名单后恒 0（原判据原样复算，且比原来更严：build/rebuild 旧的整目录删除已消失，
# 只剩 scratch.go 删本包自建临时目录），并新增正面现态锁：build.go 零可执行 RemoveAll。
DESTRUCT_CODE() { DESTRUCT_ALL | { grep -vE ':[0-9]+:[[:space:]]*//' || true; }; }
N="$( DESTRUCT_CODE | { grep -vE "${IDX_WHITELIST}" || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] ||
  { DESTRUCT_CODE | { grep -vE "${IDX_WHITELIST}" || true; }
    die "产品代码出现破坏性动作（B4）（可执行行摘掉 M5 派生索引重建白名单后仍命中 ${N} 处）"; }
BUILD_RM_CODE="$( { cd "${REPO_ROOT}" && grep -Hrn 'RemoveAll' internal/index/build.go || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
[ "${BUILD_RM_CODE}" = "0" ] ||
  die "internal/index/build.go 出现可执行 RemoveAll（${BUILD_RM_CODE} 处）：M6 R9/R10 已把整目录删除禁掉"
N="$( { cd "${REPO_ROOT}" && grep -rnE 'checkout --|reset --hard' internal/index/ || true; } |
  wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/index 出现 Git 破坏性动作（${N} 处）"
N="$( { cd "${REPO_ROOT}" && grep -rn 'RemoveAll' internal/index/ || true; } |
  { grep -v '_test\.go' || true; } | { grep -v 'os\.RemoveAll(dir)' || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "internal/index 的 RemoveAll 参数不是逐字 dir（${N} 处）：换路径必须重新登记"
N="$( { cd "${REPO_ROOT}" && grep -rnE '"(domains|sources|proposals|reviews)"|"\.\."|\.\./' \
  internal/index/ || true; } | { grep -v '_test\.go' || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] ||
  die "internal/index 非测试源出现权威产物根或 ..（${N} 处）：索引包不认识权威文件在哪"
# ⑤ 落点集合 + 每文件计数双侧等号。
# ── C2a·M6 现态重钉（§16.2 R6-P0-2「RemoveAll 禁令」+ §16.4 R6-P0-4「.index/ 布局面现态重钉是显式例外」
#    + R9 §19.1/§19.2 + R10 §20 授权；保留历史事实 + 新增现态双侧锁，破坏面收缩为加严、非放宽）──
#    历史事实一格不放宽：破坏性删除只可能落在 internal/index，RemoveAll 参数只许逐字 `dir`、绝不碰 Git 面
#    （`checkout --` / `reset --hard` 恒 0，见上）、绝不认权威根或 `..`（见上）。
#    M6 现态（加严）：R9/R10 把 build/rebuild 的失败清理与重建从「RemoveAll 整个 .index/ 目录」收紧为「逐项
#    非递归 os.Remove（+ 条件化删本次自建空目录）」，以护住 run.lock inode 与 txn 日志——故**可执行**
#    `os.RemoveAll(dir)` 现只落在 scratch.go（删本包 MkdirTemp 自建临时目录）恰 1 处；build.go / rebuild.go
#    的**可执行** RemoveAll 恒 0（build.go 仅留一行注释记明该禁令）。破坏面由三文件收缩至一文件，是加严。
DESTRUCT_CODE | { grep -E "${IDX_WHITELIST}" || true; } | cut -d: -f1 | sort -u \
  >"${WORK}/destruct_sites.txt"
printf '%s\n' internal/index/scratch.go | sort -u >"${WORK}/want_destruct_sites.txt"
diff -u "${WORK}/want_destruct_sites.txt" "${WORK}/destruct_sites.txt" ||
  die "可执行 RemoveAll(dir) 落点集合与 M6 现态不符（R9 §19 / R10 §20 后应恰 scratch.go 一处）"
N="$( DESTRUCT_CODE | { grep -cE '^internal/index/scratch\.go:' || true; } | tail -1)"
[ "${N}" = "1" ] || die "internal/index/scratch.go 的可执行 RemoveAll(dir) 应恰 1 处（${N} 处）"
for f in build rebuild; do
  N="$( { cd "${REPO_ROOT}" && grep -Hrn 'RemoveAll' "internal/index/${f}.go" || true; } |
    { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
  [ "${N}" = "0" ] ||
    die "internal/index/${f}.go 出现可执行 RemoveAll（${N} 处）：M6 R9/R10 已把整目录删除收紧为非递归删除"
done
# ⑥ scratch.go 的删除目标来源封闭（dir 只能来自本包 os.MkdirTemp）。
SCRATCH="${REPO_ROOT}/internal/index/scratch.go"
[ -f "${SCRATCH}" ] || die "internal/index/scratch.go 缺失：一次性目录 owner 必须在册"
N="$( { grep -cF 'dir, err := os.MkdirTemp("", prefix)' "${SCRATCH}" || true; } | tail -1)"
[ "${N}" = "1" ] || die "scratch.go 的 dir 不是逐字来自 os.MkdirTemp(\"\", prefix)（${N} 处）"
N="$( { grep -nF 'os.MkdirTemp' "${SCRATCH}" || true; } |
  { grep -vE '^[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
[ "${N}" = "1" ] || die "scratch.go 非注释行的 os.MkdirTemp 应恰 1 处（${N} 处）"
N="$( { grep -cF 'func NewScratch(prefix string) (string, func() error, error) {' "${SCRATCH}" || true; } | tail -1)"
[ "${N}" = "1" ] || die "scratch.go 缺窄签名 NewScratch(prefix string) (string, func() error, error)"
N="$( { grep -nE '\bdir string\b' "${SCRATCH}" || true; } |
  { grep -vE '^[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "scratch.go 非注释行出现 dir string 形参（${N} 处）：调用方不得提供删除路径"
# ⑦ 旧的宽 API 形态不许回退（注释里的历史说明不算实现）。
N="$( { cd "${REPO_ROOT}" && grep -rn 'RemoveScratch' internal/ cmd/ || true; } |
  { grep -vE ':[0-9]+:[[:space:]]*//' || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "RemoveScratch（可传任意路径的删除 API）在非注释行出现（${N} 处）"
# ⑧ 机器反证逐条在册。
for t in TestDeletionTargetTakingAPIsAreExactlyTheIndexDirPair \
         TestNewScratchNeverDeletesItsParameter \
         TestNewScratchPrefixIsNotAPath \
         TestNewScratchCleanupIsBoundToItsOwnDir; do
  grep -qF "func ${t}(" "$(eg_test_path internal/index/scratch_test.go)" ||
    die "scratch_test.go 缺具名反证 ${t}"
done
ok "④ 已写改动保留、未写文件零改动、status / decision / 七分区逐字不变，一次 apply 一次 commit"

printf '\n=== 全部 %d 步、%d 条断言通过：execution=failed 逐路径报告成立（T-…-036）===\n' \
  "${STEP}" "${PASS}"
