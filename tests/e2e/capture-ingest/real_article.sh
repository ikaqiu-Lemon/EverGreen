#!/usr/bin/env bash
# M1 端到端验收脚本（T-evergreen.s1_main_flow-158614-018）。
#
# 一篇真实文章零介入跑通主链路：
#   eg init → eg config set → eg capture → eg context → eg apply（含一次 content_hash
#   不匹配的跳过路径）→ eg report --last
#
# 约束：离线、可重复执行、无外部依赖（只用 bash / coreutils / git / go）；
# 任何一条断言不成立立刻非零退出。全程零交互：所有命令的 stdin 都接 /dev/null。
#
# 用法：cd evergreen && bash tests/e2e/capture-ingest/real_article.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
DATA_DIR="${EG_FIXTURES}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m1-e2e.XXXXXX")"
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

# eg 包装：零交互（stdin 接 /dev/null），返回码交给调用方判断。
eg() { "${EG}" --vault "${VAULT}" "$@" </dev/null; }
eg_code() { local c=0; eg "$@" >"${WORK}/out.json" 2>"${WORK}/err.txt" || c=$?; echo "${c}"; }

# jget KEY FILE：从 --json 信封里取一个标量（只认 "key": "value" / "key": value 形态，
# 不引入 jq / python 依赖）。
jget() {
  tr ',' '\n' <"$2" | tr '{' '\n' | grep -o "\"$1\": *\"[^\"]*\"" | head -1 |
    sed 's/.*: *"//; s/"$//'
}

contains() { grep -Fq -- "$2" "$1"; }

# jhash REL FILE：从 --json 信封的 base{} 里取某个文件的 content_hash（取不到返回空串）。
jhash() {
  tr ',' '\n' <"$2" | tr '{' '\n' |
    grep -o "\"$1\": *\"sha256:[a-f0-9]*\"" | head -1 |
    sed 's/.*"\(sha256:[a-f0-9]*\)"$/\1/' || true
}

# ---------------------------------------------------------------- 0. 构建
step "构建 eg（CGO_ENABLED=0，与发布口径一致）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "编译失败"
ok "二进制就绪：$("${EG}" --version </dev/null | head -1)"

for f in bitter-lesson.txt verification-key-to-ai.txt; do
  [ -s "${DATA_DIR}/${f}" ] || die "语料缺失：tests/fixtures/e2e/${f}"
done
ok "真实文章语料齐备（The Bitter Lesson / Verification, The Key to AI）"

# ---------------------------------------------------------------- 1. init
step "eg init：初始化 vault 骨架"
[ "$(eg_code init --domain ai-infra)" = "0" ] || die "eg init 退出码非 0"
for p in unprocessed.md SKILL.md evergreen.yml sources domains/ai-infra/notes domains/ai-infra/knowledge; do
  [ -e "${VAULT}/${p}" ] || die "init 未生成 ${p}"
done
ok "骨架 + SKILL.md 落盘，Git 仓已建"

# ---------------------------------------------------------------- 2. config
step "eg config set：默认领域与领域列表"
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set default_domain 失败"
[ "$(eg_code config set domains ai-infra,ops)" = "0" ] || die "config set domains 失败"
eg config get default_domain >"${WORK}/cfg.txt" </dev/null
contains "${WORK}/cfg.txt" "ai-infra" || die "config get 读不回 default_domain"
ok "default_domain=ai-infra；配置变更产生 reconcile commit"

# ---------------------------------------------------------------- 3. capture
step "eg capture：收录第一篇真实文章"
code=$(eg_code capture --url http://www.incompleteideas.net/IncIdeas/BitterLesson.html \
  --title "The Bitter Lesson" --body-file "${DATA_DIR}/bitter-lesson.txt" \
  --reason "M1 端到端验收语料" --domain ai-infra --captured-at 2026-09-17T09:00:00+08:00 --json)
[ "${code}" = "0" ] || { cat "${WORK}/err.txt" >&2; die "capture 退出码 ${code}"; }
SRC="$(jget source_id "${WORK}/out.json")"
[ -n "${SRC}" ] || die "capture 未返回 source_id"
[ -f "${VAULT}/sources/${SRC}.md" ] || die "原文未落盘"
contains "${VAULT}/unprocessed.md" "${SRC}" || die "收件区未登记该原文"
ok "原文 ${SRC} 落盘并登记收件区"

# ---------------------------------------------------------------- 4. context
step "eg context：取 base 的 content_hash"
[ "$(eg_code context --source "${SRC}" --json)" = "0" ] || die "context 退出码非 0"
UNPROC_HASH="$(jget "unprocessed.md" "${WORK}/out.json")"
case "${UNPROC_HASH}" in sha256:*) ;; *) die "context 未给出 unprocessed.md 的 content_hash" ;; esac
ok "base[unprocessed.md]=${UNPROC_HASH:0:24}…"

NOTE="n-20260917-the-bitter-lesson"
CARD="k-20260917-bitter-lesson"
CARD_REL="domains/ai-infra/knowledge/${CARD}.md"

# ---------------------------------------------------------------- 5. apply ①
step "eg apply：第一篇 → 材料笔记 + 知识卡 + 材料关系 + 未决问题"
cat >"${WORK}/plan1.json" <<JSON
{
  "plan_version": 1,
  "verb": "process",
  "domain": "ai-infra",
  "reason": "把《The Bitter Lesson》沉淀成一张可独立复用的知识卡",
  "requirement_ids": ["EG-KNW-04", "EG-CVG-01", "EG-EXT-02"],
  "base": { "unprocessed.md": "${UNPROC_HASH}" },
  "ops": [
    {
      "op": "write_note",
      "source": "${SRC}",
      "note_id": "${NOTE}",
      "title": "The Bitter Lesson（Rich Sutton, 2019）",
      "sections": {
        "材料提炼": "- 原文主张：能利用算力的通用方法长期最有效。\n",
        "Agent 分析": "- 约束的是长期技术路线选择，不是单次交付取舍。\n"
      },
      "output_cards": [{ "card": "${CARD}", "mode": "新建" }],
      "coverage_gaps": ["counterexample"]
    },
    {
      "op": "create_card",
      "card_id": "${CARD}",
      "title": "能利用算力的通用方法长期胜过人工注入知识",
      "tags": ["ai", "method"],
      "sources": [
        { "source": "${SRC}", "note": "${NOTE}", "rel": "support",
          "reason": "原文用四个领域的历史给出该结论的直接依据" }
      ],
      "sections": {
        "知识内容": "在算力成本持续下降的前提下，可随算力扩展的通用方法长期优于人工注入知识。\n",
        "解释与依据": "- 原文四个历史案例。\n",
        "条件与边界": "- 前提是算力可持续增长。\n",
        "理解自检": "- 算力停止增长时结论还成立吗？\n"
      }
    },
    {
      "op": "add_open_question",
      "note": "${NOTE}",
      "question": "在数据或评估信号受限的任务上，这个结论是否仍然成立？"
    }
  ]
}
JSON
code=$(eg_code apply --plan "${WORK}/plan1.json" --json)
[ "${code}" = "0" ] || { cat "${WORK}/out.json" >&2; die "apply ① 退出码 ${code}，期望 0"; }
cp "${WORK}/out.json" "${WORK}/apply1.json"
[ -f "${VAULT}/domains/ai-infra/notes/${NOTE}.md" ] || die "材料笔记未落盘"
[ -f "${VAULT}/${CARD_REL}" ] || die "知识卡未落盘"
contains "${VAULT}/domains/ai-infra/notes/${NOTE}.md" "${CARD}" || die "笔记「产出知识卡」未列出卡 ID"
for four in "source: '${SRC}'" "note: '${NOTE}'" "rel: 'support'" "reason: '"; do
  contains "${VAULT}/${CARD_REL}" "${four}" || die "卡 sources[] 四要素不全，缺 ${four}"
done
contains "${WORK}/apply1.json" "counterexample" || die "报告未标注 coverage_gaps 缺失项"
# 【M4 · T-…-057 按实测重钉，只加严不放宽】原式钉的是 `"reconcile":{"ran":false}` 一键一值。
# M4 依对账合同 §11 把该字段扩为**恰三键**（`ran` / `commit` / `findings`，合法新增能力，
# 见 M-004 完成判据 8），非对账命令路径下的占位形态因此是三键三值。本行改钉**完整占位串**：
# M1 的结论（这条链路没跑对账 → ran 恒 false）一格未放宽，另外把「commit 为 null、findings
# 是 [] 而非 null」两件事也一并钉住 —— 信息量由 1 值增到 3 值。
contains "${WORK}/apply1.json" '"reconcile":{"ran":false,"commit":null,"findings":[]}' \
  || die "报告 reconcile 必须是三键占位形态（ran=false / commit=null / findings=[]）"
ok "原文 / 笔记 / 卡 / 材料关系 / 未决问题齐全，覆盖缺失已标注"

# ---------------------------------------------------------------- 6. git 链路
step "git log：init → reconcile → capture → process"
mapfile -t VERBS < <(git -C "${VAULT}" log --reverse --format=%s | sed 's/(.*//')
EXPECT=(init reconcile capture process)
for i in 0 1 2 3; do
  [ "${VERBS[$i]:-}" = "${EXPECT[$i]}" ] ||
    die "第 $((i + 1)) 条 commit verb = ${VERBS[$i]:-<空>}，期望 ${EXPECT[$i]}"
done
if git -C "${VAULT}" log --format=%s | grep -qvE '^[a-z_]+\(ai-infra\): .+'; then
  die "存在不符合 <verb>(<domain>): <subject> 的 commit 主题"
fi
[ -z "$(git -C "${VAULT}" status --porcelain)" ] || die "apply 后工作区应干净（已全部提交）"
ok "四条 commit 链路完整、主题规范、工作区干净"

# ---------------------------------------------------------------- 7.5 第二篇收录
step "eg capture + context：收录相似的第二篇，取已有卡的 content_hash"
CORE_BEFORE="$(sed -n '/^## 知识内容$/,/^## /p' "${VAULT}/${CARD_REL}" | md5sum)"
code=$(eg_code capture --url http://www.incompleteideas.net/IncIdeas/KeytoAI.html \
  --title "Verification, The Key to AI" --body-file "${DATA_DIR}/verification-key-to-ai.txt" \
  --reason "与已有卡高度相似的第二篇" --domain ai-infra --captured-at 2026-09-17T09:30:00+08:00 --json)
[ "${code}" = "0" ] || die "第二篇 capture 退出码 ${code}"
SRC2="$(jget source_id "${WORK}/out.json")"
NOTE2="n-20260917-verification-the-key-to-ai"
eg context --source "${SRC2}" --json >"${WORK}/ctx2.json" </dev/null
UNPROC2="$(jhash "unprocessed.md" "${WORK}/ctx2.json")"
CARD_HASH="$(jhash "${CARD_REL}" "${WORK}/ctx2.json")"
[ -n "${UNPROC2}" ] && [ -n "${CARD_HASH}" ] || die "第二篇 context 未给全 base（SRC2=${SRC2}）"
ok "第二篇 ${SRC2} 已收录；候选相似卡 ${CARD} 的 content_hash 已取到"
# ---------------------------------------------------------------- 8. B3 跳过
step "eg apply：content_hash 不匹配 → 跳过并上报（B3，退 3）"
# 写前 hash 只经 eg context 取得（不自己算），随后模拟「用户在 Obsidian 里改了这张卡」。
[ -n "${CARD_HASH}" ] || die "eg context 未给出知识卡的 content_hash，无法构造 B3 场景"
printf '\n<!-- 外部编辑：模拟用户在 Obsidian 里改了这张卡 -->\n' >>"${VAULT}/${CARD_REL}"
cat >"${WORK}/plan_b3.json" <<JSON
{
  "plan_version": 1, "verb": "process", "domain": "ai-infra",
  "reason": "B3 回归：base 用改动前的 content_hash",
  "base": { "${CARD_REL}": "${CARD_HASH}" },
  "ops": [
    { "op": "append_card", "card": "${CARD}",
      "sections": { "条件与边界": "- 这条不应落盘\n" } }
  ]
}
JSON
code=$(eg_code apply --plan "${WORK}/plan_b3.json" --json)
[ "${code}" = "3" ] || { cat "${WORK}/out.json" >&2; die "B3 场景退出码 ${code}，期望 3"; }
contains "${WORK}/out.json" '"kind":"file_changed"' || die "skipped[].kind 应为 file_changed"
contains "${WORK}/out.json" '"cause":"content_hash_mismatch"' || die "skipped[].cause 应为 content_hash_mismatch"
if grep -Fq "这条不应落盘" "${VAULT}/${CARD_REL}"; then die "被跳过的内容不得落盘"; fi
git -C "${VAULT}" checkout -- "${CARD_REL}"
ok "外部改动被拦下：kind=file_changed / cause=content_hash_mismatch，内容零落盘"

# ---------------------------------------------------------------- 9. 第二篇 apply
step "eg apply：相似第二篇走 append_card（不动「知识内容」）"
# B3 那步留下的外部编辑已随上一次 apply 一并提交，这里按规程重新取一次 base（Agent 收到
# 退出码 3 后的标准动作：刷新 eg context 再重试）。
eg context --source "${SRC2}" --json >"${WORK}/ctx2.json" </dev/null
UNPROC2="$(jhash "unprocessed.md" "${WORK}/ctx2.json")"
CARD_HASH="$(jhash "${CARD_REL}" "${WORK}/ctx2.json")"
[ -n "${CARD_HASH}" ] || die "重试前的 eg context 未给出卡的 content_hash"

cat >"${WORK}/plan2.json" <<JSON
{
  "plan_version": 1, "verb": "process", "domain": "ai-infra",
  "reason": "第二篇按非核心补充追加到已有卡",
  "requirement_ids": ["EG-CVG-01"],
  "convergence": [
    { "card": "${CARD}", "relation": "non_core_supplement",
      "core_knowledge": "same", "conditions": "different", "reuse_purpose": "same",
      "note": "核心知识相同，第二篇补了一条成立条件（知识须能被系统自验证）" }
  ],
  "base": { "unprocessed.md": "${UNPROC2}", "${CARD_REL}": "${CARD_HASH}" },
  "ops": [
    { "op": "write_note", "source": "${SRC2}", "note_id": "${NOTE2}",
      "title": "Verification, The Key to AI（Rich Sutton, 2001）",
      "sections": { "材料提炼": "- 原文主张：系统必须能自己验证自己是否工作正常。\n" },
      "output_cards": [{ "card": "${CARD}", "mode": "补充" }] },
    { "op": "append_card", "card": "${CARD}",
      "sections": {
        "条件与边界": "- 补充边界：纯人工维护的知识库不满足自验证条件。\n- 补充依据：可扩展的前提之一是知识能被系统自验证。\n" } },
    { "op": "add_material_rel", "card": "${CARD}", "source": "${SRC2}", "note": "${NOTE2}",
      "rel": "support", "reason": "第二篇从自验证角度为该卡结论提供支持性材料依据" }
  ]
}
JSON
code=$(eg_code apply --plan "${WORK}/plan2.json" --json)
[ "${code}" = "0" ] || { cat "${WORK}/out.json" >&2; die "第二篇 apply 退出码 ${code}，期望 0"; }
CORE_AFTER="$(sed -n '/^## 知识内容$/,/^## /p' "${VAULT}/${CARD_REL}" | md5sum)"
[ "${CORE_BEFORE}" = "${CORE_AFTER}" ] || die "append_card 改动了「知识内容」分区"
contains "${VAULT}/${CARD_REL}" "补充依据" || die "「条件与边界」未收到补充依据行"
contains "${VAULT}/${CARD_REL}" "补充边界" || die "「条件与边界」未收到补充边界行"
ok "条件与边界追加成功（v2 知识卡自动路径仅此一格可写），「知识内容」字节不变"

# ---------------------------------------------------------------- 10. report
step "eg report --last：与磁盘逐项交叉核对"
eg report --last --json >"${WORK}/last.json" </dev/null || die "report --last 退出码非 0"
eg report --last >"${WORK}/last.txt" </dev/null || die "report --last（文本）退出码非 0"
contains "${WORK}/last.json" "${CARD}" || die "报告未提及本次更新的卡"
contains "${WORK}/last.txt" "${CARD}" || die "人类可读报告未提及本次更新的卡"
SHA="$(jget commit "${WORK}/last.json")"
[ -n "${SHA}" ] && git -C "${VAULT}" cat-file -e "${SHA}" 2>/dev/null ||
  die "报告 commit ${SHA} 在 Git 里不存在"
[ -z "$(git -C "${VAULT}" status --porcelain)" ] || die "report 是只读命令，工作区不应变化"
# 封闭命名表之外的同义词一律不得出现（关键词运行期拼接，保证本目录 grep 零匹配）。
BANNED="sta""le"
if grep -q "${BANNED}" "${WORK}/last.json"; then die "报告出现封闭命名表之外的措辞 ${BANNED}"; fi
ok "报告与磁盘 / Git 一致（commit ${SHA:0:8}），且只读"

# ---------------------------------------------------------------- 11. 幂等
step "幂等：同一篇文章再收录一次"
BEFORE_SRC_COUNT="$(find "${VAULT}/sources" -name '*.md' | wc -l | tr -d ' ')"
code=$(eg_code capture --url http://www.incompleteideas.net/IncIdeas/BitterLesson.html \
  --title "The Bitter Lesson" --body-file "${DATA_DIR}/bitter-lesson.txt" \
  --reason "重复收录（幂等回归）" --domain ai-infra --captured-at 2026-09-17T09:00:00+08:00 --json)
[ "${code}" = "0" ] || die "重复收录退出码 ${code}"
contains "${WORK}/out.json" '"deduped":true' || die "重复收录未判重"
AFTER_SRC_COUNT="$(find "${VAULT}/sources" -name '*.md' | wc -l | tr -d ' ')"
[ "${BEFORE_SRC_COUNT}" = "${AFTER_SRC_COUNT}" ] || die "重复收录产生了第二份原文"
[ "$(grep -c "source_id: '${SRC}'" "${VAULT}/unprocessed.md" || true)" = "0" ] ||
  die "收件区出现重复条目"
[ "$(grep -c "  - source: '${SRC}'" "${VAULT}/${CARD_REL}")" = "1" ] || die "卡 sources[] 出现重复条目"
ok "原文一份、收件区无重复、材料关系无重复"

# ---------------------------------------------------------------- 12. error 反例
step "error 反例：E6 写「用户补充」被拒 → 退 2 且零写入"
CARD_MD5_BEFORE="$(md5sum <"${VAULT}/${CARD_REL}")"
cat >"${WORK}/plan_e6.json" <<JSON
{
  "plan_version": 1, "verb": "process", "domain": "ai-infra",
  "reason": "E6 反例：自动路径不得写用户分区", "base": {},
  "ops": [ { "op": "append_card", "card": "${CARD}",
             "sections": { "用户补充": "- 越界写入\n" } } ]
}
JSON
code=$(eg_code apply --plan "${WORK}/plan_e6.json" --json)
[ "${code}" = "2" ] || die "E6 反例退出码 ${code}，期望 2"
contains "${WORK}/out.json" '"code":"E6"' || die "未给出 E6 诊断"
[ -z "$(git -C "${VAULT}" status --porcelain)" ] || die "error 路径必须零写入"
[ "${CARD_MD5_BEFORE}" = "$(md5sum <"${VAULT}/${CARD_REL}")" ] || die "error 路径改动了目标文件"
ok "E6 拦截、零写入、目标文件字节不变"

# ---------------------------------------------------------------- 13. 产物可解析
step "产物可被 Obsidian 打开（机器替代判据）"
# Schema v2 固定分区数按实体类型区分（真源 internal/mdfile：CardSections()=3、NoteSections()=4）：
#   知识卡（domains/**/knowledge/）= 3（知识内容 / 条件与边界 / 用户补充）；
#   材料笔记（domains/**/notes/）   = 4（整理正文 / 提取结果 / 存疑与待验证 / 用户补充）。
# 旧的「一律 5」是 v1 五分区口径（卡与笔记都 5）；v2 起两类分区面各自收敛，故按类型逐一钉死。
while IFS= read -r f; do
  head -1 "${f}" | grep -q '^---$' || die "${f} 缺 frontmatter"
  if grep -q '^# ' "${f}"; then die "${f} 出现 H1（分区一律 H2）"; fi
  n="$(grep -c '^## ' "${f}")"
  case "${f}" in
    */knowledge/*) want=3 ;;
    */notes/*)     want=4 ;;
    *)             die "未预期的产物路径（既非 knowledge 卡也非 notes 笔记）：${f}" ;;
  esac
  [ "${n}" = "${want}" ] || die "${f} 的 H2 分区数 = ${n}，期望 ${want}（Schema v2 按类型）"
done < <(find "${VAULT}/domains" -name '*.md')
ok "全部知识卡（3 分区）/ 材料笔记（4 分区）：frontmatter 齐全、无 H1、H2 分区数按 Schema v2 类型恰好"

printf '\n===================================================\n'
printf 'M1 端到端验收脚本通过：%d 步 / %d 条断言\n' "${STEP}" "${PASS}"
printf '===================================================\n'
