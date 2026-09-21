#!/usr/bin/env bash
# M3 授权路径端到端脚本（T-evergreen.s1_main_flow-158614-038）。
#
# 判据来源：`2026-10-10-m3-user-authorization-contract.md`
#   §1   两条路径（P-A / P-U）与**反伪造条款 N-1**：`initiator: user` 与命令行
#        `--user-request` 必须**同时**成立；plan 文件内容不能自证授权
#   §2.1 写权限矩阵第 12 行（**唯一的条件解锁行**）：P-A 对已有卡 🔴 / create_card 新建 ✅
#   §2.1 第 15 行「用户补充」：两条路径均 🔴，授权也解不开
#   §5   授权**不是**豁免：B1–B4 一字不改，`--user-request` 不放宽 content_hash 比对
#   §6   十三条不可授权项（本脚本取其中三条禁止措辞的全库反证）
#
# 五组断言：
#   ① 矩阵 #12 正负例：append_card 写「知识内容」→ 退 2 + 目标文件字节不变；
#      create_card 新建写「知识内容」→ 退 0（同一格的两个子情形，一次跑完）；
#   ② 反伪造：plan 内 initiator: user 但命令行缺 --user-request → 退 2；
#      补上 flag 后同一份 plan 不再因授权失败；
#   ③ 「用户补充」两条路径均退 2、字节不变；
#   ④ B3 不因授权豁免：--user-request + 文件被外部改动 → 退 3、报告里有 skipped；
#   ⑤ 三条禁止措辞在 internal/ 非测试文件零命中（pattern 在脚本内**拼接**构造，
#      不写完整字面量，否则脚本自己会被全库反证 grep 命中）。
#
# 为什么第 ① 组要断言「字节不变」而不只看退出码：退 2 只说明校验拒绝了，
#   字节不变才说明**整条 op 真的没执行**——这正是矩阵门闸与「先写后回滚」的分水岭。
#
# 为什么第 ④ 组把 --user-request 和外部改动叠在一起：这是「授权 ≠ 豁免」唯一能被
#   端到端证伪的形态。若哪天有人让用户显式路径跳过 content_hash 比对，这一步立刻红。
#
# 约束：离线、可重复执行、无外部依赖（bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互（stdin 接 /dev/null）。
#
# 用法：cd evergreen && bash tests/e2e/authorization/authorization.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m3-auth.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"
PLAN="${WORK}/plan.json"

KDIR_REL='domains/ai-infra/knowledge'
NDIR_REL='domains/ai-infra/notes'
CARD_A="k-20260901-attention"     # 已有卡：矩阵 #12 的 🔴 子情形靶子
CARD_NEW="k-20260905-scaling"     # create_card 新建：矩阵 #12 的 ✅ 子情形靶子
NOTE_ID="n-20260901-attention"
SRC_ID="s-20260901-attention"
REL_A="${KDIR_REL}/${CARD_A}.md"
REL_NEW="${KDIR_REL}/${CARD_NEW}.md"
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

# apply_agent 走 **Agent 自动路径**（P-A）：命令行**不给** --user-request。
apply_agent() { cat >"${PLAN}"; eg_code apply --plan "${PLAN}" --json; }
# apply_user 走 **用户显式路径**（P-U）：命令行给出 --user-request 作为佐证。
apply_user() { cat >"${PLAN}"; eg_code apply --plan "${PLAN}" --json --user-request; }

want_code() { grep -Fq "\"code\":\"$1\"" "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "期望诊断码 $1（$2）"; }; }

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. 建库 + 语料
step "eg init 建库 + 一篇原文 + 一篇笔记 + 一张已有卡"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 失败"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
mkdir -p "${VAULT}/${KDIR_REL}" "${VAULT}/${NDIR_REL}" "${VAULT}/sources"

cat >"${VAULT}/sources/${SRC_ID}.md" <<SRC_EOF
---
id: ${SRC_ID}
url: https://example.com/attention
title: 注意力机制入门
saved_at: '2026-09-01T09:00:00+08:00'
---

# 注意力机制入门

## 收录理由

- 讲清了 QKV 的来历

## 原文

注意力是一种加权聚合。
SRC_EOF

cat >"${VAULT}/${NDIR_REL}/${NOTE_ID}.md" <<NOTE_EOF
---
id: ${NOTE_ID}
source: ${SRC_ID}
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
---

# 注意力机制入门（材料笔记）

## 材料提炼

原文第 3 节给出该机制的定义。

## Agent 分析

与既有卡有重叠。

## 用户补充

## 存疑与待验证

## 产出知识卡
NOTE_EOF

cat >"${VAULT}/${REL_A}" <<CARD_EOF
---
id: ${CARD_A}
title: 注意力机制
status: active
created_at: '2026-09-01'
updated_at: '2026-09-01T10:00:00+08:00'
sources:
  - source: ${SRC_ID}
    note: ${NOTE_ID}
    rel: support
    reason: 原文第 3 节给出该机制的定义
---

# 注意力机制

## 知识内容

注意力是一种加权聚合。

## 解释与依据

- 依据原文第 3 节

## 条件与边界

- 仅适用于序列建模

## 用户补充

${USER_LINE}

## 理解自检

- 当前有效块：多头注意力的头数如何选？
CARD_EOF

gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "seed: m3 授权用例语料"
[ -z "$(gitv status --porcelain)" ] || die "前置条件：工作区必须干净"
HASH_A_BEFORE="$(rawhash "${REL_A}")"
ok "库就绪：1 篇原文 + 1 篇笔记 + 1 张已有卡（${CARD_A}）"

# ---------------------------------------------------------------- 2. ① 矩阵 #12
step "① 矩阵 #12 负例：append_card 写「知识内容」→ 退 2 + 目标文件字节不变"
C="$(apply_agent <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"Agent 想改核心结论",
 "requirement_ids":["EG-EDIT-04"],
 "base":{"${REL_A}":"$(filehash "${REL_A}")"},
 "ops":[{"op":"append_card","card":"${CARD_A}",
         "sections":{"知识内容":"Agent 自作主张改写的结论。\n"}}]}
JSON
)"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "append_card 写「知识内容」应退 2，实退 ${C}"; }
want_code E6 "矩阵 #12 的 P-A 🔴 子情形"
[ "$(rawhash "${REL_A}")" = "${HASH_A_BEFORE}" ] || die "被拒的 op 改动了目标文件字节"
[ -z "$(gitv status --porcelain)" ] || die "校验失败必须零写入、无 commit"
ok "① 负例成立：退 2 + E6 + 目标文件逐字节不变 + 零 commit"

step "① 矩阵 #12 正例：create_card 新建时写「知识内容」→ 退 0"
C="$(apply_agent <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"新建一张卡",
 "requirement_ids":["EG-EDIT-04"],"base":{},
 "ops":[{"op":"create_card","card_id":"${CARD_NEW}","title":"缩放点积注意力",
         "sources":[{"source":"${SRC_ID}","note":"${NOTE_ID}","rel":"support",
                     "reason":"原文第 3 节给出该机制的定义"}],
         "sections":{"知识内容":"缩放是为了稳定 softmax 的梯度。\n"}}]}
JSON
)"
[ "${C}" = "0" ] || { cat "${WORK}/out.txt"; die "create_card 新建写「知识内容」应退 0，实退 ${C}"; }
[ -f "${VAULT}/${REL_NEW}" ] || die "新卡未落盘"
grep -Fq '缩放是为了稳定 softmax 的梯度。' "${VAULT}/${REL_NEW}" || die "新卡的「知识内容」未写入"
ok "① 正例成立：同一格的 ✅ 子情形（create_card 新建）照常退 0 并落盘"

# ---------------------------------------------------------------- 3. ② 反伪造 N-1
step "② 反伪造：plan 内 initiator: user 但命令行缺 --user-request → 退 2"
HASH_A_BEFORE="$(rawhash "${REL_A}")"
FORGED_PLAN='{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"用户要求失效这张卡",
 "requirement_ids":["EG-EDIT-04"],"base":{},
 "ops":[{"op":"deprecate","target":"'"${CARD_A}"'","reason":"已被新卡取代","initiator":"user"}]}'
C="$(printf '%s' "${FORGED_PLAN}" | apply_agent)"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "缺 --user-request 应退 2，实退 ${C}"; }
want_code W7 "N-1 反伪造（沿用 W7，不新增编号）"
grep -Fq 'N-1' "${WORK}/out.txt" || { cat "${WORK}/out.txt"; die "W7 必须逐字说明反伪造理由（N-1）"; }
[ "$(rawhash "${REL_A}")" = "${HASH_A_BEFORE}" ] || die "被拒的 op 改动了目标文件字节"
ok "② 负例成立：plan 内容不能自证授权，退 2 + W7 点名 N-1 + 字节不变"

step "② 同一份 plan 带 --user-request → 不再因授权失败"
C="$(printf '%s' "${FORGED_PLAN}" | apply_user)"
[ "${C}" != "2" ] || { cat "${WORK}/out.txt"; die "带 --user-request 后不得再因授权失败（实退 2）"; }
if grep -Fq '"code":"W7","level":"error"' "${WORK}/out.txt"; then
  cat "${WORK}/out.txt"; die "带佐证后不得再出 W7 error"
fi
if grep -Fq '"code":"E6"' "${WORK}/out.txt"; then
  cat "${WORK}/out.txt"; die "矩阵 #3 在 P-U 下是 ✅，不得再出 E6"
fi
ok "② 正例成立：initiator: user + --user-request 同时成立才进 P-U"

# ---------------------------------------------------------------- 4. ③ 用户补充
step "③ 「用户补充」两条路径均退 2（矩阵 #15，授权也解不开）"
HASH_A_BEFORE="$(rawhash "${REL_A}")"
user_section_plan() {
  printf '%s' '{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"越界写用户补充",
 "requirement_ids":["EG-EDIT-04"],"base":{},
 "ops":[{"op":"append_card","card":"'"${CARD_A}"'"'"$1"',
         "sections":{"用户补充":"CLI 不该写的话。\n"}}]}'
}
C="$(user_section_plan '' | apply_agent)"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "P-A 写「用户补充」应退 2，实退 ${C}"; }
want_code E6 "矩阵 #15 的 P-A 🔴"
C="$(user_section_plan ',"initiator":"user"' | apply_user)"
[ "${C}" = "2" ] || { cat "${WORK}/out.txt"; die "P-U 写「用户补充」同样应退 2，实退 ${C}"; }
want_code E6 "矩阵 #15 的 P-U 🔴（授权也解不开）"
[ "$(rawhash "${REL_A}")" = "${HASH_A_BEFORE}" ] || die "用户分区所在文件的字节被改动了（B2）"
grep -Fq "${USER_LINE}" "${VAULT}/${REL_A}" || die "用户分区原文必须逐字保留"
ok "③ 两条路径均退 2 + E6，用户分区逐字保留（B2 / U-03）"

# ---------------------------------------------------------------- 5. ④ B3 不豁免
step "④ B3 不因授权豁免：--user-request + 文件被外部改动 → 退 3、报告里有 skipped"
BASE_STALE="$(filehash "${REL_A}")"
printf '\n外部编辑器加的一行（凭据自此过期）。\n' >>"${VAULT}/${REL_A}"
gitv add -A >/dev/null
gitv -c user.name=eg -c user.email=eg@example.com commit -q -m "外部改动：制造 content_hash 失配"
HASH_A_STALE="$(rawhash "${REL_A}")"
C="$(apply_user <<JSON
{"plan_version":1,"verb":"process","domain":"ai-infra","reason":"用户显式要求补一条依据",
 "requirement_ids":["EG-EDIT-04"],
 "base":{"${REL_A}":"${BASE_STALE}"},
 "ops":[{"op":"append_card","card":"${CARD_A}","initiator":"user",
         "sections":{"条件与边界":"用户要求补的依据。\n"}}]}
JSON
)"
[ "${C}" = "3" ] || { cat "${WORK}/out.txt"; die "用户显式路径遇到外部改动应退 3，实退 ${C}"; }
grep -Fq '"kind":"file_changed"' "${WORK}/out.txt" ||
  { cat "${WORK}/out.txt"; die "跳过必须进报告 skipped[]，kind=file_changed"; }
[ "$(rawhash "${REL_A}")" = "${HASH_A_STALE}" ] ||
  die "被跳过的文件必须逐字节不变（外部改动原样保留，不做任何还原）"
ok "④ 授权不是豁免：--user-request 照样先读盘比对 content_hash，失配即跳过 + 退 3 + 进报告"

# ---------------------------------------------------------------- 6. ⑤ 禁止措辞
step "⑤ 三条禁止措辞在 internal/ 非测试文件零命中"
# pattern **拼接**构造：写成完整字面量的话，本脚本自己就会被全库反证 grep 命中。
P1="跳过 hash"' '"比对"
P2="自动回滚到"'执行前'
P3="Agent 自动路径"'亦可改'
banned_hits() { (cd "${REPO_ROOT}" && grep -rn "${P1}\|${P2}\|${P3}" internal/ || true) |
  (grep -v '_test\.go' || true); }
N="$(banned_hits | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || { banned_hits; die "internal/ 出现禁止措辞（${N} 处）"; }
# 顺带锁住「授权不是豁免」的另外两条静态事实（合同 §5 / U-02）。
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
# 「在 M6 语境下永久禁用」。破坏性动作本体一格不放宽——把「可执行行」与「注释说明」分离：破坏性**可执行行**
# （剔除行首注释后）减索引白名单后恒 0（原判据原样复算，且比原来更严：build/rebuild 旧的整目录删除已消失，
# 只剩 scratch.go 删本包自建临时目录），并新增正面现态锁：build.go 零可执行 RemoveAll。
DESTRUCT_CODE() { DESTRUCT_ALL | { grep -vE ':[0-9]+:[[:space:]]*//' || true; }; }
N="$( DESTRUCT_CODE | { grep -vE "${IDX_WHITELIST}" || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] ||
  { DESTRUCT_CODE | { grep -vE "${IDX_WHITELIST}" || true; }
    die "internal/ 出现破坏性回滚动作（U-02）（可执行行摘掉 M5 派生索引重建白名单后仍命中 ${N} 处）"; }
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
N="$( { cd "${REPO_ROOT}" && grep -rn 'os.Exit(5)' internal/ || true; } | wc -l | tr -d ' ')"
[ "${N}" = "0" ] || die "M3 不实现退出码 5/6（${N} 处）"
ok "⑤ 三条禁止措辞 0 命中；破坏性回滚 0 命中；退出码 5 0 命中"

# ---------------------------------------------------------------- 7. 收口
step "收口：两条路径恰两条、工作区干净"
[ "$(eg_code --help)" = "0" ] || die "eg --help 应退 0"
grep -Fq -- '--user-request' "${WORK}/out.txt" || die "--help 的全局 flag 块必须说明 --user-request"
[ -z "$(gitv status --porcelain)" ] || { gitv status --porcelain; die "收口时工作区必须干净"; }
ok "收口：--help 登记了全局 flag --user-request，工作区干净"

printf '\n=== 全部 %d 步、%d 条断言通过：M3 授权路径判据成立（T-…-038）===\n' "${STEP}" "${PASS}"
exit 0
