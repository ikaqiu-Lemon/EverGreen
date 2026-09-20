#!/usr/bin/env bash
# 笔记复盘合同端到端（T-evergreen.knowledge_opinion_split-158614-012 · T12-5）。
#
# 判据来源（声明面）：
#   Schema v2 契约 §4.2（来源块 / omissions / annotation 词表 / 机器锚点）、
#   §4.2.3（extraction_coverage 语义与覆盖矩阵渲染）、§4.4（Knowledge / Opinion 双入口）、
#   §5.1（「提取结果」按 Knowledge → Opinion → 覆盖矩阵成清单）、§3（--json 信封 / 退出码）。
#
# 为什么需要这支 suite：既有 focused 用例（mdfile / plan / store）在**包内**证语义，
# 但没有任何一支端到端判据用**真实二进制**从 init → capture → apply 驱动一遍完整加工，
# 再回读盘上字节确认 v2 正向链路、三类失败链路、v1 兼容三面都成立。本 suite 补齐这层：
# 一切事实只回读文件字节 / git 自己 / eg 自己的 `--json` 信封，不看实现自报。
#
# 本 suite 锁死的判据（全部在真实临时 vault 上驱动真实二进制、离线、零交互、可重复）：
#   A v2 正向：一份可执行 plan 覆盖 source 块 + 一个真实 omission、七类内置 annotation
#     （guide/supplement/emphasis/summary/distinction/verification/reflection）+ 一个合法扩展
#     label、Knowledge 与 Opinion 双入口产物、extraction_coverage 的 outputs + note_only 且缺漏=0；
#     退 0、真实 note 落盘；**去掉 frontmatter 后的确定性正文，与固定 golden 逐字节相等**——
#     golden 是静态夹具（只存到最后一个内容行的单换行，不在运行时用产品 renderer 生成）；
#     note 模板恒在正文末尾多留一个空行，故比较不是「原始全文逐字节」，而是由测试侧把这一
#     模板固定的最终空行显式补到 golden 尾部后再 cmp；并逐条复核：七标签不退化、
#     source/agent 顺序、eg:nr / eg:nc 机器锚点在盘、Knowledge / Opinion 双入口产物真实落盘
#     （opinion frontmatter validation: pending）、提取结果按 Knowledge → Opinion → 覆盖矩阵
#     排列、覆盖行 / 输出 ID / Opinion `[pending]` 可见。
#   B v2 失败三类（各用独立 fresh vault，避免前案串扰；均走真实 `eg apply --json`）：
#     ① disposition=missing；② output_cards ↔ coverage 悬空；③ 非法 source_ref（越界）。
#     逐项解析 data.errors[] 断言 code=E2 + 精确 path；进程退出码 / 信封 exit_code 均为 2、
#     status=failed；请求前后权威 Markdown sha、HEAD、commit 数、git status 逐字不变，目标 note 不出现。
#   C v1 兼容：真实 plan_version:1 + sections{} apply 退 0，正文与固定 v1 golden 逐字节相等，
#     不出现 eg:nr / eg:nc（旧口径不引入新锚点）。
#   D 洁净性：REPO_ROOT 工作树 git status 在本 suite 前后逐字不变（一切写操作只在 mktemp 内）。
#
# 约束：离线、零交互、可重复执行、无外部依赖（bash / coreutils / git / go / python3）；
# 一切写操作只发生在 mktemp -d 目录内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/note-review/note_review_contract.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
. "${REPO_ROOT}/tests/lib/common.sh"
eg_require_disk 512

command -v python3 >/dev/null || { echo "[FAIL] 本脚本用 python3 做 JSON 键级判定" >&2; exit 1; }

FIX="${REPO_ROOT}/tests/fixtures/e2e/note-review"
GOLDEN_V2="${FIX}/v2_body.golden.md"
GOLDEN_V1="${FIX}/v1_body.golden.md"
[ -f "${GOLDEN_V2}" ] || { echo "[FAIL] 缺 v2 golden：${GOLDEN_V2}" >&2; exit 1; }
[ -f "${GOLDEN_V1}" ] || { echo "[FAIL] 缺 v1 golden：${GOLDEN_V1}" >&2; exit 1; }

WORK="$(eg_scratch eg-note-review-contract)"
EG="${WORK}/eg"
trap 'rm -rf "${WORK}"' EXIT
trap 'echo "[FAIL] 第 ${STEP} 步失败（行 ${LINENO}）" >&2' ERR

STEP=0
PASS=0
step() { STEP=$((STEP + 1)); printf '\n=== [%02d] %s ===\n' "${STEP}" "$1"; }
ok()   { PASS=$((PASS + 1)); printf '  [ok] %s\n' "$1"; }
die()  { printf '  [FAIL] %s\n' "$1" >&2; exit 1; }

# 洁净性基线：本 suite 结束时 REPO_ROOT 工作树必须逐字回到此刻状态。
BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# 正文源固定六个非空物理行（capture 落盘后前置一空行，故非空行为 L2..L7）。
# L7 是原文页脚里重复出现的导航条——契约允许删除的 omission 只有导航 / 广告 / 评论区 /
# 重复页眉页脚这类非正文噪声，故正向链路以它作合法 omission（“主题无关旁注”不属可删理由）。
ARTICLE=$'甲 定义：缩放因子是 1/sqrt(d_k)。\n乙 条件：仅在点积注意力下成立。\n丙 反例：短序列下不显著。\n丁 观点：长序列不经济。\n戊 争议：是否可扩展存疑。\n首页 · 订阅 · 关于我们 · 版权所有 —— 重复页脚导航。\n'

# new_vault <标记>：起一份全新 vault，走真实主链路 init → config → capture，回显 "VAULT SRC BASE"。
new_vault() {
  local tag="$1"
  local v="${WORK}/v_${tag}"
  local art="${WORK}/art_${tag}.md"
  rm -rf "${v}"
  "${EG}" --vault "${v}" init --domain tech </dev/null >/dev/null || die "[${tag}] init 失败"
  "${EG}" --vault "${v}" config set default_domain tech </dev/null >/dev/null || die "[${tag}] config 失败"
  printf '%s' "${ARTICLE}" >"${art}"
  "${EG}" --vault "${v}" capture --url "https://example.com/${tag}" --title "夹具${tag}" \
    --reason 'T12-5 契约夹具' --body-file "${art}" </dev/null >/dev/null 2>&1 || die "[${tag}] capture 失败"
  local src base
  src="$(ls "${v}/sources" | head -1 | sed 's/\.md$//')"
  [ -n "${src}" ] || die "[${tag}] capture 后 sources/ 为空"
  base="$("${EG}" --vault "${v}" context --source "${src}" --json </dev/null \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["base"]["unprocessed.md"])')"
  [ -n "${base}" ] || die "[${tag}] context 未回显 unprocessed.md base"
  printf '%s %s %s\n' "${v}" "${src}" "${base}"
}

# strip_fm <note文件>：丢弃 YAML frontmatter（首个 --- 到第二个 --- 含），留确定性正文。
strip_fm() { awk 'BEGIN{n=0} /^---$/{n++; if(n<=2) next} n>=2{print}' "$1"; }

# jenv <信封文件> <python表达式>：o=信封根对象，errs=data.errors 的 (code,path) 列表。
jenv() {
  python3 - "$1" "$2" <<'PY'
import json, sys
o = json.load(open(sys.argv[1], encoding="utf-8"))
d = o.get("data") or {}
errs = [(e.get("code"), e.get("path")) for e in (d.get("errors") or [])]
print(eval(sys.argv[2]))
PY
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

# ---------------------------------------------------------------- 1. v2 正向
step "A v2 正向：一份可执行 plan 覆盖来源块 / omission / 七标签 / 扩展 label / 双入口 / 覆盖矩阵"
read -r V SRC BASE < <(new_vault pos)
cat >"${WORK}/plan_pos.json" <<PLAN
{ "plan_version": 2, "verb": "process", "domain": "tech", "reason": "T12-5 正向契约",
  "requirement_ids": ["EG-NR-01"],
  "base": { "unprocessed.md": "${BASE}" },
  "ops": [
    { "op": "write_note", "source": "${SRC}", "note_id": "n-20270101-contract",
      "blocks": [
        {"role":"source","source_ref":"L2-L2","heading":"定义","body":"缩放因子是 1/sqrt(d_k)。"},
        {"role":"agent","annotation":"guide","body":"先读定义，再看条件。"},
        {"role":"source","source_ref":"L3-L3","heading":"条件","body":"仅在点积注意力下成立。"},
        {"role":"agent","annotation":"supplement","body":"补充：sqrt 用于抑制方差。"},
        {"role":"source","source_ref":"L4-L4","heading":"反例","body":"短序列下不显著。"},
        {"role":"agent","annotation":"emphasis","body":"务必区分显著与成立。"},
        {"role":"source","source_ref":"L5-L5","heading":"观点","body":"长序列不经济。"},
        {"role":"agent","annotation":"summary","body":"总结：定义-条件-反例-观点。"},
        {"role":"source","source_ref":"L6-L6","heading":"争议","body":"是否可扩展存疑。"},
        {"role":"agent","annotation":"distinction","body":"辨析：可扩展性与经济性不同。"},
        {"role":"agent","annotation":"verification","body":"待验证：平方复杂度断言。"},
        {"role":"agent","annotation":"reflection","body":"反思：证据链是否闭合。"},
        {"role":"agent","annotation":"caveat","label":"边界提示","body":"扩展标签：仅限本例。"}
      ],
      "omissions": [
        {"source_ref":"L7-L7","reason":"重复页脚导航，非正文内容"}
      ],
      "extraction_coverage": [
        {"module":"知识与观点提炼","source_refs":["L2-L2","L3-L3","L5-L5"],"summary":"定义、条件与观点凝练为复用产物。","disposition":"outputs","outputs":["k-20270101-scaling","o-20270101-longseq"]},
        {"module":"整理留存","source_refs":["L4-L4","L6-L6"],"summary":"反例与争议仅在笔记内留存。","disposition":"note_only","reason":"证据不足以独立成卡，暂留笔记。"}
      ],
      "output_cards": [
        {"card":"k-20270101-scaling","mode":"新建"},
        {"card":"o-20270101-longseq","mode":"新建"}
      ] },
    { "op": "create_knowledge", "card_id": "k-20270101-scaling", "title": "缩放点积注意力",
      "sources": [{"source":"${SRC}","note":"n-20270101-contract","rel":"support","reason":"原文定义与条件"}],
      "sections": {"知识内容":"缩放因子是 1/sqrt(d_k)。\n","条件与边界":"仅在点积注意力下成立。\n"} },
    { "op": "create_opinion", "opinion_id": "o-20270101-longseq", "title": "长序列下不经济",
      "sources": [{"source":"${SRC}","note":"n-20270101-contract","rel":"support","reason":"原文观点行"}],
      "sections": {"观点":"长序列下该机制不经济。\n"} }
  ] }
PLAN

RC=0; "${EG}" --vault "${V}" apply --plan "${WORK}/plan_pos.json" --json </dev/null >"${WORK}/env_pos.json" 2>&1 || RC=$?
[ "${RC}" = "0" ] || { cat "${WORK}/env_pos.json"; die "v2 正向 apply 应退 0，实际 ${RC}"; }
[ "$(jenv "${WORK}/env_pos.json" 'o.get("status")')" = "completed" ] || die "v2 正向 status 应为 completed"
ok "apply 退 0、信封 status=completed"

NOTE="${V}/domains/tech/notes/n-20270101-contract.md"
[ -f "${NOTE}" ] || die "v2 正向未落盘 note"
ok "真实 note 落盘：${NOTE##*/}"

strip_fm "${NOTE}" >"${WORK}/pos.body"
# golden 只存到最后一个内容行的单换行；note 模板恒在正文末尾多留一个空行（最后一段 `## 用户补充`
# 后固定跟一空行）。比较不是「原始全文逐字节」，而是把这一模板固定的最终空行显式补到 golden
# 尾部得到期望正文，再与实得正文逐字节 cmp——即除模板固定末空行外，正文其余字节需逐字相等。
{ cat "${GOLDEN_V2}"; printf '\n'; } >"${WORK}/pos.expected"
if ! cmp -s "${WORK}/pos.body" "${WORK}/pos.expected"; then
  echo "----- diff (实得 <  vs  期望 >) -----"; diff "${WORK}/pos.body" "${WORK}/pos.expected" || true
  die "v2 正向正文与 golden（末补模板固定空行后）不逐字节相等"
fi
ok "去 frontmatter 正文与 v2 golden（末补模板固定空行）逐字节相等（cmp）"

# 七类内置 annotation 固定中文标签一个不缺、不退化（渲染进 > **[Agent <标签>]**）。
for lab in 导读 补充 强调 总结 辨析 待验证 反思; do
  grep -qF "> **[Agent ${lab}]** " "${NOTE}" || die "缺内置标签渲染：${lab}"
done
ok "七类内置 annotation 标签齐全且不退化：导读/补充/强调/总结/辨析/待验证/反思"
# 合法扩展 label 按原文渲染。
grep -qF "> **[Agent 边界提示]** " "${NOTE}" || die "缺合法扩展 label 渲染：边界提示"
ok "合法扩展 label 按原 label 渲染：边界提示"

# 机器锚点在盘：eg:nr（每块一条：5 source + 8 agent，加 1 omission，共 14）、eg:nc（每个覆盖模块一条，共 2）。
NR="$(grep -c 'eg:nr:1' "${NOTE}")"; NC="$(grep -c 'eg:nc:1' "${NOTE}")"
[ "${NR}" = "14" ] || die "eg:nr 锚点应有 14 条（13 块 + 1 omission），实得 ${NR}"
[ "${NC}" = "2" ]  || die "eg:nc 锚点应有 2 条（两个覆盖模块），实得 ${NC}"
ok "机器锚点在盘：eg:nr=14、eg:nc=2"

# 提取结果严格按 Knowledge → Opinion → 覆盖矩阵排列。
K_AT="$(grep -n '^### Knowledge$' "${NOTE}" | head -1 | cut -d: -f1)"
O_AT="$(grep -n '^### Opinion$'   "${NOTE}" | head -1 | cut -d: -f1)"
C_AT="$(grep -n '^### 覆盖矩阵$'  "${NOTE}" | head -1 | cut -d: -f1)"
[ -n "${K_AT}" ] && [ -n "${O_AT}" ] && [ -n "${C_AT}" ] || die "提取结果缺 Knowledge/Opinion/覆盖矩阵 之一"
[ "${K_AT}" -lt "${O_AT}" ] && [ "${O_AT}" -lt "${C_AT}" ] || die "提取结果顺序不是 Knowledge → Opinion → 覆盖矩阵"
ok "提取结果按 Knowledge(${K_AT}) → Opinion(${O_AT}) → 覆盖矩阵(${C_AT}) 排列"

grep -qF -- '- k-20270101-scaling（新建）' "${NOTE}" || die "Knowledge 行缺 k-20270101-scaling"
grep -qF -- '- o-20270101-longseq（新建） `[pending]`' "${NOTE}" || die "Opinion 行缺 [pending] 标记"
ok "输出 ID 可见、Opinion 行带 [pending]"
grep -qF '| `知识与观点提炼` |' "${NOTE}" || die "覆盖矩阵缺 outputs 行"
grep -qF '| `整理留存` |' "${NOTE}" || die "覆盖矩阵缺 note_only 行"
grep -qF 'Note-only：' "${NOTE}" || die "覆盖矩阵 note_only 处置未渲染"
ok "覆盖矩阵 outputs 行与 note_only 行俱在（缺漏=0）"

# 双入口产物：不止清单文本提到，Knowledge 卡与 Opinion 卡都要在各自分区真实落盘。
K_CARD="${V}/domains/tech/knowledge/k-20270101-scaling.md"
O_CARD="${V}/domains/tech/opinions/o-20270101-longseq.md"
[ -f "${K_CARD}" ] || die "双入口缺 Knowledge 卡落盘：domains/tech/knowledge/${K_CARD##*/}"
[ -f "${O_CARD}" ] || die "双入口缺 Opinion 卡落盘：domains/tech/opinions/${O_CARD##*/}"
# Opinion 分区独有的正交维度：新建观点 frontmatter 的 validation 恒为 pending（§4.5 / §6.3）。
grep -qE "^validation:[[:space:]]*'?pending'?[[:space:]]*$" "${O_CARD}" \
  || die "Opinion frontmatter validation 应为 pending"
# 知识卡分区不引入 validation 键（两分区 frontmatter 形态不同）。
grep -qE "^validation:" "${K_CARD}" && die "Knowledge 卡不应带 validation 键" || true
ok "双入口产物真实落盘：knowledge/k-20270101-scaling.md + opinions/o-20270101-longseq.md（opinion validation: pending）"

# ---------------------------------------------------------------- 2. v2 失败三类
# authority_fp <vault>：权威事实指纹（全量 *.md sha256 + HEAD + commit 数 + git status）。
authority_fp() {
  local v="$1"
  ( cd "${v}" && find . -name '*.md' -type f | sort | xargs -r sha256sum
    echo "--HEAD $(git -C "${v}" rev-parse HEAD)"
    echo "--commits $(git -C "${v}" rev-list --count HEAD)"
    git -C "${v}" status --porcelain | sort )
}

# 三类失败 plan 均为**带占位符的模板**（__SRC__ / __BASE__），由 assert_fail 用各自 fresh
# vault 的真实 SRC/BASE 替换后驱动——每类一份独立 vault，杜绝前案串扰。
write_fail_templates() {
  python3 - "${WORK}" <<'PY'
import json, sys, os
work = sys.argv[1]
def blocks(bad=False):
    refs = ["L2-L2","L3-L3","L4-L4","L5-L5","L6-L6","L7-L7"]
    heads = ["定义","条件","反例","观点","争议","页脚"]
    bodies = ["缩放因子是 1/sqrt(d_k)。","仅在点积注意力下成立。","短序列下不显著。",
              "长序列不经济。","是否可扩展存疑。","首页 · 订阅 · 关于我们 · 版权所有 —— 重复页脚导航。"]
    if bad:
        refs[4] = "L20-L20"  # 越界：源正文仅到 L7
    return [{"role":"source","source_ref":r,"heading":h,"body":b}
            for r,h,b in zip(refs,heads,bodies)]
def base(ops, reason, rid):
    return {"plan_version":2,"verb":"process","domain":"tech","reason":reason,
            "requirement_ids":[rid],"base":{"unprocessed.md":"__BASE__"},"ops":ops}
# ① disposition=missing
miss = base([{"op":"write_note","source":"__SRC__","note_id":"n-20270101-contract",
    "blocks":blocks(),"omissions":[],"extraction_coverage":[
      {"module":"整理留存","source_refs":["L2-L2","L3-L3","L4-L4","L5-L5","L6-L6"],
       "summary":"整理留存。","disposition":"note_only","reason":"暂留笔记。"},
      {"module":"缺口","source_refs":["L7-L7"],"summary":"该段未处理。","disposition":"missing"}]}],
    "T12-5 失败·missing","EG-NR-F1")
# ② output_cards ↔ coverage 悬空（k-scaling 未被任何覆盖模块 outputs 引用）
dangle = base([{"op":"write_note","source":"__SRC__","note_id":"n-20270101-contract",
    "blocks":blocks(),"omissions":[],"extraction_coverage":[
      {"module":"整理留存","source_refs":["L2-L2","L3-L3","L4-L4","L5-L5","L6-L6","L7-L7"],
       "summary":"整理留存。","disposition":"note_only","reason":"暂留笔记。"}],
    "output_cards":[{"card":"k-20270101-scaling","mode":"新建"}]}],
    "T12-5 失败·dangle","EG-NR-F2")
# ③ 非法 source_ref（越界）
badref = base([{"op":"write_note","source":"__SRC__","note_id":"n-20270101-contract",
    "blocks":blocks(bad=True),"omissions":[],"extraction_coverage":[
      {"module":"整理留存","source_refs":["L2-L2","L3-L3","L4-L4","L5-L5","L20-L20","L7-L7"],
       "summary":"整理留存。","disposition":"note_only","reason":"暂留笔记。"}]}],
    "T12-5 失败·badref","EG-NR-F3")
for name, obj in (("miss",miss),("dangle",dangle),("badref",badref)):
    json.dump(obj, open(os.path.join(work, f"tpl_{name}.json"), "w"), ensure_ascii=False)
PY
}

# assert_fail <tag> <模板文件> <期望code> <期望path>
assert_fail() {
  local tag="$1" tpl="$2" code="$3" path="$4" v src base note before after rc hit plan
  read -r v src base < <(new_vault "${tag}")
  note="${v}/domains/tech/notes/n-20270101-contract.md"
  plan="${WORK}/plan_${tag}.json"
  python3 -c "import sys;t=open(sys.argv[1]).read().replace('__SRC__',sys.argv[2]).replace('__BASE__',sys.argv[3]);open(sys.argv[4],'w').write(t)" \
    "${tpl}" "${src}" "${base}" "${plan}"
  before="$(authority_fp "${v}")"
  rc=0
  "${EG}" --vault "${v}" apply --plan "${plan}" --json </dev/null >"${WORK}/env_${tag}.json" 2>&1 || rc=$?
  [ "${rc}" = "2" ] || { cat "${WORK}/env_${tag}.json"; die "[${tag}] 进程退出码应为 2，实际 ${rc}"; }
  [ "$(jenv "${WORK}/env_${tag}.json" 'o.get("exit_code")')" = "2" ] || die "[${tag}] 信封 exit_code 应为 2"
  [ "$(jenv "${WORK}/env_${tag}.json" 'o.get("status")')" = "failed" ] || die "[${tag}] 信封 status 应为 failed"
  hit="$(jenv "${WORK}/env_${tag}.json" "(\"${code}\", \"${path}\") in errs")"
  [ "${hit}" = "True" ] || { echo "  实得 errors: $(jenv "${WORK}/env_${tag}.json" 'errs')"; die "[${tag}] data.errors[] 未含 (${code}, ${path})"; }
  [ ! -e "${note}" ] || die "[${tag}] 失败后目标 note 不应出现"
  after="$(authority_fp "${v}")"
  [ "${before}" = "${after}" ] || { diff <(printf '%s' "${before}") <(printf '%s' "${after}") || true; die "[${tag}] 权威面（md sha / HEAD / commit 数 / status）被改动"; }
  ok "[${tag}] E2@${path}、exit=2/status=failed、note 不出现、权威面逐字不变"
}

write_fail_templates
step "B① 失败：disposition=missing → E2 @ extraction_coverage[].disposition"
assert_fail miss "${WORK}/tpl_miss.json" E2 "ops[0].extraction_coverage[1].disposition"
step "B② 失败：output_cards ↔ coverage 悬空 → E2 @ output_cards[].card"
assert_fail dangle "${WORK}/tpl_dangle.json" E2 "ops[0].output_cards[0].card"
step "B③ 失败：非法 source_ref（越界 L20-L20）→ E2 @ blocks[].source_ref"
assert_fail badref "${WORK}/tpl_badref.json" E2 "ops[0].blocks[4].source_ref"

# ---------------------------------------------------------------- 3. v1 兼容
step "C v1 兼容：plan_version:1 + sections{} apply 退 0、正文对 v1 golden、无 eg:nr/eg:nc"
read -r V SRC BASE < <(new_vault v1)
cat >"${WORK}/plan_v1.json" <<PLAN
{ "plan_version": 1, "verb": "process", "domain": "tech", "reason": "T12-5 v1 兼容",
  "requirement_ids": ["EG-NR-V1"],
  "base": { "unprocessed.md": "${BASE}" },
  "ops": [
    { "op": "write_note", "source": "${SRC}", "note_id": "n-20270101-v1compat",
      "sections": {"材料提炼":"- 缩放因子是 1/sqrt(d_k)。\n","Agent 分析":"- 仅在点积注意力下成立。\n"},
      "output_cards": [{"card":"k-20270101-v1","mode":"新建"}] },
    { "op": "create_knowledge", "card_id": "k-20270101-v1", "title": "v1 缩放",
      "sources": [{"source":"${SRC}","note":"n-20270101-v1compat","rel":"support","reason":"原文"}],
      "sections": {"知识内容":"缩放因子是 1/sqrt(d_k)。\n","条件与边界":"仅在点积注意力下成立。\n"} }
  ] }
PLAN
RC=0; "${EG}" --vault "${V}" apply --plan "${WORK}/plan_v1.json" --json </dev/null >"${WORK}/env_v1.json" 2>&1 || RC=$?
[ "${RC}" = "0" ] || { cat "${WORK}/env_v1.json"; die "v1 兼容 apply 应退 0，实际 ${RC}"; }
NOTE_V1="${V}/domains/tech/notes/n-20270101-v1compat.md"
[ -f "${NOTE_V1}" ] || die "v1 兼容未落盘 note"
strip_fm "${NOTE_V1}" >"${WORK}/v1.body"
# 同 v2：golden 只存到最后内容行的单换行，比较前把模板固定的最终空行补到 golden 尾再 cmp。
{ cat "${GOLDEN_V1}"; printf '\n'; } >"${WORK}/v1.expected"
cmp -s "${WORK}/v1.body" "${WORK}/v1.expected" || { echo "----- diff -----"; diff "${WORK}/v1.body" "${WORK}/v1.expected" || true; die "v1 正文与 golden（末补模板固定空行后）不逐字节相等"; }
ok "v1 apply 退 0、正文与 v1 golden（末补模板固定空行）逐字节相等"
if grep -q 'eg:nr:1\|eg:nc:1' "${NOTE_V1}"; then die "v1 兼容不得出现 eg:nr/eg:nc 锚点"; fi
ok "v1 兼容正文不含 eg:nr / eg:nc（旧口径未引入新锚点）"

# ---------------------------------------------------------------- 4. 洁净性
step "D 洁净性：REPO_ROOT 工作树 git status 逐字不变"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] || die "本 suite 改动了 REPO_ROOT 工作树（应只写 mktemp 内）"
ok "REPO_ROOT 工作树未被本 suite 改动"

printf '\n[PASS] note_review_contract：%d 项断言全部通过\n' "${PASS}"
