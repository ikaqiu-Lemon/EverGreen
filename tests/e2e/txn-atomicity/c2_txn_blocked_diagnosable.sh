#!/usr/bin/env bash
# C2 · I-…-025 先红用例：事务扫描阻断态**必须可诊断**（system_assurance · T-…-014）。
#
# 判据来源（声明面，逐字）：
#   `2027-01-24-m6-atomicity-and-strict-check-contract.md`
#     §18.4 / §4.1.2：「报告须逐条列出冲突 / 违规路径：…**损坏事务给 `txn_id` 与不可解析原因**，…
#     **扫描全集异常给全部问题 `txn_id` 列表，便于人工处置**」；§4.2 损坏事务原样保留 + 权威零写入；
#     §12：M6 新增诊断码**恰 5 条**（E15/E16/W26/W27/W28）——本用例**不允许**实现新增第 6 个码；
#     §13.4：命令数恒 22——**不允许**新增任何清理 / 强制放弃命令。
#   `2026-09-01-eg-cli-contract.md` §5：诊断条目的 `path` 语义是**文件路径**
#     （因此 `txn_id` 不得占用 `path`，应落在 `target` 这一既有字段上）。
#   `skill/SKILL.md`（E15 处置指引）：「先跑 `eg check --strict` 定位」——该指引要成立，
#     `eg check` / `check --strict` 就必须在阻断态里说出「谁把它堵住了、为什么、怎么解开」。
#
# 本用例锁死的事实（全部在 mktemp -d 的真实 vault 上驱动真实二进制，只回读 JSON 信封 / 文件字节 /
# git 自己 / txnctl scan，不看实现自报）：
#   A 多 `OpenTxn` 阻断（原因⑥）：A 类写命令退 **5** + `E15`；且诊断必须
#     A2 逐个未闭合事务各有一条诊断，`txn_id` 落在 `target` 字段；
#     A3 总述条的 `message` 里含**全部**问题 `txn_id`（「全部问题 txn_id 列表」的落点）；
#     A4 至少一条诊断给出 **CLI 出路**（指向 `eg check` 这条可用的定位命令）；
#     A5 **没有任何**诊断把 `txn_id` 塞进 `path`（`path` 只能是空串或 `.index/txn/…` 这类真实路径）。
#   B 损坏事务（原因③）：B1 退 5 + `E15`；B2 诊断必须给出**不可解析原因**（不是一句「损坏事务」）；
#     B3 `txn_id` 在 `target`、不在 `path`；B4 事务目录与 `intent.json` 逐字节原样保留。
#   C `eg check` / `eg check --strict` 在阻断态**可诊断**：
#     C1 两条命令都仍退 **0**（只读体检不改退出码，合同 §13 三档不变）；
#     C2 每个问题 `txn_id` 都出现在诊断的 `target` 上（未闭合 + 损坏全覆盖）；
#     C3 输出含**不可解析原因**与**人工出路**（`.index/txn/` 这一具体处置对象）；
#     C4 `--strict` 至少与默认面**同样**可诊断（不许 strict 反而更少）；
#     C5 只读到底：零 commit、`git status --porcelain` 逐字不变、`.index/txn/` 树快照逐字节不变；
#     C6 `data.check` 键面仍恰 `excluded,findings,scope`（可诊断信息**不靠新增 data 键**换来）。
#   D 三面文档覆盖：`README.md` / `skill/SKILL.md` / `eg check --help` 都写明阻断态的诊断读法与
#     人工出路（`.index/txn/<txn_id>`）——「便于人工处置」这句承诺必须有落点。
#   E 健康库对照（防噪音）：干净 vault 上 `eg check` / `--strict` 退 0 且**不出现**任何事务态诊断。
#   F 码面守恒：阻断态的错误码恒 `E15`，check 的事务态诊断**不引入新编号码**（§12 恰 5 条不被破）。
#
# 约束：离线、零交互、可重复执行；一切写操作只发生在 mktemp -d 目录内；
#      一律显式传 `--vault` 绝对路径（不依赖 cwd 向上查找）；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/txn-atomicity/c2_txn_blocked_diagnosable.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 256

WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-c2-txn-blocked.XXXXXX")"
BASEV="${WORK}/base"
EG="${WORK}/eg"
TXN="${WORK}/txnctl"

STEP=0
PASS=0
FAILED=0
cleanup() { rm -rf "${WORK}"; }
trap cleanup EXIT

step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
bad()  { FAILED=$((FAILED + 1)); printf '  [FAIL] %s\n' "$1" >&2; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

command -v python3 >/dev/null || die "本脚本用 python3 做键级判定"

egv()   { "${EG}" --vault "$1" "${@:2}" </dev/null; }
codev() { local v="$1"; shift; local c=0; egv "${v}" "$@" --json >"${WORK}/out.txt" 2>&1 || c=$?; echo "${c}"; }
gitv()  { git -C "$1" "${@:2}"; }

# jget <json文件> <python表达式>：o=信封根，d=data，
#   diags = 所有诊断桶（信封 warnings[] + data.errors[] + data.report.warnings[]）的条目对象全集。
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
diags = []
for bucket in (o.get("warnings"), d.get("errors"),
               (d.get("report") or {}).get("warnings")):
    for it in (bucket or []):
        if isinstance(it, dict):
            diags.append(it)
print(eval(sys.argv[2]))
PY
}
# targets_of <json文件>：全部诊断的 target 值（去重升序，空值剔除）
targets_of() { jget "$1" 'sorted({ (x.get("target") or "") for x in diags } - {""})'; }
# msgs_of <json文件>：全部诊断 message 拼成一段（供关键词判定）
msgs_of() { jget "$1" '" || ".join([ (x.get("message") or "") for x in diags ])'; }
# 目录树快照（相对路径 + 类型 + 内容哈希，逐字节可比）
snap() {
  ( cd "$1" 2>/dev/null && find . | LC_ALL=C sort | while read -r p; do
      if [ -f "${p}" ]; then printf '%s f %s\n' "${p}" "$(sha256sum "${p}" | awk '{print $1}')";
      else printf '%s d\n' "${p}"; fi
    done ) || true
}
fresh() { local v="${WORK}/$1"; rm -rf "${v}"; cp -a "${BASEV}" "${v}"; echo "${v}"; }
# 造 N 个未闭合事务（intent 已发布、commit/abort 均缺席）
mk_open() {
  local v="$1" n="$2" i id
  for i in $(seq 1 "${n}"); do
    id="$("${TXN}" alloc-id --vault "${v}" | sed -n 's/^txn_id=//p')"
    "${TXN}" write-intent --vault "${v}" --txn "${id}" --path "o-open-${i}.md" >/dev/null
    printf '%s\n' "${id}"
  done
}
# 造一个损坏事务（intent.json 在盘但不可解析）
mk_corrupt() {
  local v="$1" id="$2"
  mkdir -p "${v}/.index/txn/${id}"
  printf 'not-json-at-all' > "${v}/.index/txn/${id}/intent.json"
}
scanfield() {
  "${TXN}" scan --vault "$1" 2>/dev/null | tail -1 |
    tr ' ' '\n' | awk -F= -v k="$2" '$1==k {print $2}'
}

# ---------------------------------------------------------------- 0. 构建 + seed
step "构建 eg 与 txnctl（CGO_ENABLED=0）并 seed 一个干净基准 vault"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) >/dev/null || die "编译 eg 失败"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${TXN}" ./tests/lib/txnctl) >/dev/null || die "编译 txnctl 失败"
mkdir -p "${BASEV}"
[ "$(codev "${BASEV}" init --domain tech)" = "0" ] || { cat "${WORK}/out.txt"; die "eg init 失败"; }
[ "$(codev "${BASEV}" config set default_domain tech)" = "0" ] || die "config set 失败"
printf 'I-025 证据语料：阻断态必须可诊断，诊断必须给 txn_id、原因与出路。%.0s' 1 2 3 4 5 6 7 8 \
  > "${WORK}/art.md"
[ "$(codev "${BASEV}" capture --url https://example.com/i025 --title 'I-025 证据原文' \
    --reason 'I-025 取证' --body-file "${WORK}/art.md")" = "0" ] || { cat "${WORK}/out.txt"; die "capture 失败"; }
ok "基准 vault 就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- A. 多 OpenTxn 阻断态的诊断
step "A 多 OpenTxn 阻断：写命令退 5 + E15，诊断必须给出全部 txn_id（在 target 位）、以及 CLI 出路"
VA="$(fresh va)"
OPEN_IDS="$(mk_open "${VA}" 2)"
[ "$(scanfield "${VA}" open)" = "2" ] || die "夹具未造出 2 个未闭合事务"
[ "$(scanfield "${VA}" blocked)" = "1" ] || die "夹具未进入阻断态"
RC="$(codev "${VA}" capture --url https://example.com/i025-a --title 'A 段写命令' --reason 'A 段取证' --body-file "${WORK}/art.md")"
cp "${WORK}/out.txt" "${WORK}/a.json"
[ "${RC}" = "5" ] || bad "A1 阻断态写命令应退 5，实得 ${RC}"
[ "${RC}" = "5" ] && ok "A1 阻断态 A 类写命令退 5"
grep -Fq '"code":"E15"' "${WORK}/a.json" || bad "A1 阻断态应携 E15"
grep -Fq '"code":"E15"' "${WORK}/a.json" && ok "A1 诊断码 E15 在场"

A_TARGETS="$(targets_of "${WORK}/a.json")"
A_MSGS="$(msgs_of "${WORK}/a.json")"
MISS=0
for id in ${OPEN_IDS}; do
  printf '%s' "${A_TARGETS}" | grep -Fq "${id}" || { MISS=1; printf '    缺 target：%s\n' "${id}" >&2; }
done
[ "${MISS}" = "0" ] || bad "A2 每个未闭合事务都应有一条诊断把 txn_id 放进 target，实得 target 集合=${A_TARGETS}"
[ "${MISS}" = "0" ] && ok "A2 未闭合事务的 txn_id 逐个落在 target 位"

MISS=0
for id in ${OPEN_IDS}; do
  printf '%s' "${A_MSGS}" | grep -Fq "${id}" || MISS=1
done
[ "${MISS}" = "0" ] || bad "A3 总述必须给出**全部问题 txn_id 列表**（合同 §18.4），实得诊断文本=${A_MSGS}"
[ "${MISS}" = "0" ] && ok "A3 诊断文本含全部问题 txn_id"

printf '%s' "${A_MSGS}" | grep -Fq 'eg check' ||
  bad "A4 诊断必须给出 CLI 出路（至少指向 eg check 这条可用的定位命令）"
printf '%s' "${A_MSGS}" | grep -Fq 'eg check' && ok "A4 诊断给出 CLI 出路"

BADPATH="$(jget "${WORK}/a.json" \
  'sorted({ (x.get("path") or "") for x in diags if (x.get("path") or "").startswith("t0000") })')"
[ "${BADPATH}" = "[]" ] ||
  bad "A5 path 语义是文件路径（CLI 合同 §5），不得塞 txn_id，实得 ${BADPATH}"
[ "${BADPATH}" = "[]" ] && ok "A5 没有任何诊断把 txn_id 塞进 path"

# ---------------------------------------------------------------- B. 损坏事务的诊断
step "B 损坏事务：退 5 + E15，诊断必须给出不可解析原因，txn_id 在 target 而非 path，目录逐字节原样"
VB="$(fresh vb)"
CID="t0000000000007777"
mk_corrupt "${VB}" "${CID}"
CSNAP_BEFORE="$(snap "${VB}/.index/txn")"
RC="$(codev "${VB}" capture --url https://example.com/i025-b --title 'B 段写命令' --reason 'B 段取证' --body-file "${WORK}/art.md")"
cp "${WORK}/out.txt" "${WORK}/b.json"
[ "${RC}" = "5" ] || bad "B1 损坏事务应退 5，实得 ${RC}"
[ "${RC}" = "5" ] && ok "B1 损坏事务退 5"
B_MSGS="$(msgs_of "${WORK}/b.json")"
B_TARGETS="$(targets_of "${WORK}/b.json")"
printf '%s' "${B_MSGS}" | grep -Eq '不可解析|无法解析|结构非法|解析失败' ||
  bad "B2 损坏事务必须给出**不可解析原因**（合同 §18.4），实得=${B_MSGS}"
printf '%s' "${B_MSGS}" | grep -Eq '不可解析|无法解析|结构非法|解析失败' && ok "B2 给出不可解析原因"
printf '%s' "${B_TARGETS}" | grep -Fq "${CID}" ||
  bad "B3 损坏事务的 txn_id 应落在 target，实得 target 集合=${B_TARGETS}"
printf '%s' "${B_TARGETS}" | grep -Fq "${CID}" && ok "B3 损坏事务 txn_id 落在 target"
jget "${WORK}/b.json" 'sorted({ (x.get("path") or "") for x in diags }) == sorted({ p for p in { (x.get("path") or "") for x in diags } if not p.startswith("t0000") })' \
  | grep -Fq True || bad "B3 path 不得等于 txn_id"
[ "$(snap "${VB}/.index/txn")" = "${CSNAP_BEFORE}" ] || bad "B4 损坏事务目录必须逐字节原样保留"
[ "$(snap "${VB}/.index/txn")" = "${CSNAP_BEFORE}" ] && ok "B4 损坏事务目录逐字节原样保留"

# ---------------------------------------------------------------- C. eg check / --strict 可诊断
step "C eg check / check --strict 在阻断态必须可诊断（退 0 不变、给 txn_id + 原因 + 出路、只读到底）"
VC="$(fresh vc)"
C_OPEN="$(mk_open "${VC}" 2)"
mk_corrupt "${VC}" "${CID}"
GIT_BEFORE="$(gitv "${VC}" status --porcelain | LC_ALL=C sort)"
LOG_BEFORE="$(gitv "${VC}" rev-list --count HEAD)"
TXNSNAP_BEFORE="$(snap "${VC}/.index/txn")"

RC="$(codev "${VC}" check)"; cp "${WORK}/out.txt" "${WORK}/c.json"
[ "${RC}" = "0" ] || bad "C1 eg check 在阻断态仍应退 0（只读体检不改退出码），实得 ${RC}"
[ "${RC}" = "0" ] && ok "C1 eg check 退 0 不变"
RC="$(codev "${VC}" check --strict)"; cp "${WORK}/out.txt" "${WORK}/cs.json"
[ "${RC}" = "0" ] || bad "C1 eg check --strict 在阻断态仍应退 0，实得 ${RC}"
[ "${RC}" = "0" ] && ok "C1 eg check --strict 退 0 不变"

for f in c cs; do
  T="$(targets_of "${WORK}/${f}.json")"
  M="$(msgs_of "${WORK}/${f}.json")"
  MISS=0
  for id in ${C_OPEN} ${CID}; do
    printf '%s' "${T}" | grep -Fq "${id}" || { MISS=1; printf '    %s 缺 target：%s\n' "${f}" "${id}" >&2; }
  done
  [ "${MISS}" = "0" ] || bad "C2/${f} 阻断态的全部问题 txn_id 都必须出现在诊断 target 上，实得=${T}"
  [ "${MISS}" = "0" ] && ok "C2/${f} 全部问题 txn_id 可诊断（target 位）"
  printf '%s' "${M}" | grep -Eq '不可解析|无法解析|结构非法|解析失败' ||
    bad "C3/${f} 必须给出损坏事务的不可解析原因"
  printf '%s' "${M}" | grep -Fq '.index/txn/' ||
    bad "C3/${f} 必须给出人工出路的具体处置对象（.index/txn/<txn_id>）"
  { printf '%s' "${M}" | grep -Eq '不可解析|无法解析|结构非法|解析失败'; } &&
    { printf '%s' "${M}" | grep -Fq '.index/txn/'; } && ok "C3/${f} 给出原因 + 人工出路"
done

C_N="$(jget "${WORK}/c.json"  'len([ x for x in diags if (x.get("target") or "").startswith("t0000") ])')"
CS_N="$(jget "${WORK}/cs.json" 'len([ x for x in diags if (x.get("target") or "").startswith("t0000") ])')"
[ "${CS_N}" -ge "${C_N}" ] 2>/dev/null ||
  bad "C4 --strict 的可诊断信息不得少于默认面（默认 ${C_N} 条 / strict ${CS_N} 条）"
[ "${CS_N}" -ge "${C_N}" ] 2>/dev/null && ok "C4 --strict 至少与默认面同样可诊断"

[ "$(gitv "${VC}" rev-list --count HEAD)" = "${LOG_BEFORE}" ] || bad "C5 eg check 必须恒 0 次提交"
[ "$(gitv "${VC}" status --porcelain | LC_ALL=C sort)" = "${GIT_BEFORE}" ] || bad "C5 eg check 必须零文件变化"
[ "$(snap "${VC}/.index/txn")" = "${TXNSNAP_BEFORE}" ] || bad "C5 eg check 不得动 .index/txn/ 任何字节"
{ [ "$(gitv "${VC}" rev-list --count HEAD)" = "${LOG_BEFORE}" ] &&
  [ "$(gitv "${VC}" status --porcelain | LC_ALL=C sort)" = "${GIT_BEFORE}" ] &&
  [ "$(snap "${VC}/.index/txn")" = "${TXNSNAP_BEFORE}" ]; } && ok "C5 只读到底（零 commit、零文件变化、事务目录零触碰）"

CK_KEYS="$(jget "${WORK}/c.json" '",".join(sorted((d.get("check") or {}).keys()))')"
[ "${CK_KEYS}" = "excluded,findings,scope" ] ||
  bad "C6 data.check 键面必须守恒（excluded,findings,scope），实得 ${CK_KEYS}"
[ "${CK_KEYS}" = "excluded,findings,scope" ] && ok "C6 data.check 键面守恒（可诊断信息不靠新增 data 键）"

# ---------------------------------------------------------------- D. 三面文档
step "D 三面覆盖：README / SKILL / eg check --help 都写明阻断态诊断读法与人工出路"
egv "${BASEV}" check --help >"${WORK}/help.txt" 2>&1 || true
for pair in "README.md" "skill/SKILL.md"; do
  F="${REPO_ROOT}/${pair}"
  grep -Fq '.index/txn/' "${F}" ||
    bad "D ${pair} 必须写明事务阻断态的人工处置对象（.index/txn/<txn_id>）"
  grep -Eq '阻断.*(处置|诊断|出路)|事务阻断态' "${F}" ||
    bad "D ${pair} 必须写明阻断态的诊断 / 处置步骤"
  { grep -Fq '.index/txn/' "${F}" && grep -Eq '阻断.*(处置|诊断|出路)|事务阻断态' "${F}"; } &&
    ok "D ${pair} 覆盖阻断态诊断与人工出路"
done
grep -Eq '事务|txn' "${WORK}/help.txt" ||
  bad "D eg check --help 必须交代它会披露事务阻断态（SKILL 的「先跑 eg check --strict 定位」才有落点）"
grep -Eq '事务|txn' "${WORK}/help.txt" && ok "D eg check --help 覆盖事务态披露"

# ---------------------------------------------------------------- E. 健康库对照 + F 码面守恒
step "E 健康库对照：干净 vault 上 check / --strict 退 0 且零事务态诊断；F 阻断码恒 E15、不引入新码"
VE="$(fresh ve)"
[ "$(codev "${VE}" check)" = "0" ] || die "健康库 eg check 应退 0"
E_T="$(targets_of "${WORK}/out.txt")"
printf '%s' "${E_T}" | grep -Fq 't0000' &&
  bad "E 健康库不得出现任何事务态诊断（噪音），实得 target=${E_T}" || ok "E 健康库零事务态诊断、退 0"

NEWCODE="$(jget "${WORK}/c.json" \
  'sorted({ (x.get("code") or "") for x in diags if (x.get("target") or "").startswith("t0000") })')"
printf '%s' "${NEWCODE}" | grep -Eq 'W2[9]|W3[0-9]|E1[7-9]|E2[0-9]' &&
  bad "F check 的事务态诊断不得引入 M6 未分配的新编号码（§12 恰 5 条），实得 ${NEWCODE}" ||
  ok "F check 的事务态诊断未引入 M6 未分配的新码（实得 ${NEWCODE}）"
BLOCK_CODES="$(jget "${WORK}/a.json" 'sorted({ (x.get("code") or "") for x in diags })')"
[ "${BLOCK_CODES}" = "['E15']" ] ||
  bad "F 阻断态错误码应恒 E15（不新增码），实得 ${BLOCK_CODES}"
[ "${BLOCK_CODES}" = "['E15']" ] && ok "F 阻断态错误码恒 E15"

printf '\n===== 结果：%d 条判据通过，%d 条失败 =====\n' "${PASS}" "${FAILED}"
# 收尾标记（D6.2 卫生）：统一 runner 用它证明「脚本真的跑到了最后」——退出码 0 不足以排除
# 中途 return / set -e 早退。因此标记只在**判据零失败**时打印，红的时候必须先退非零，
# 免得一行标记把一次真失败洗成绿。
if [ "${FAILED}" != "0" ]; then
  printf '收尾：本次有 %d 条判据未满足，未达全绿（不打收尾标记）\n' "${FAILED}"
  exit 1
fi
printf '===== [PASS] C2 · I-…-025 判据全部通过（%d 项断言，0 失败）=====\n' "${PASS}"
