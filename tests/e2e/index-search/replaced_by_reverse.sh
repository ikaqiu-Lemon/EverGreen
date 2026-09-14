#!/usr/bin/env bash
# `replaced_by` 正反双向查询 端到端脚本（M5 · T-evergreen.s1_main_flow-158614-068 · 阶段 A）。
#
# 判据来源：`docs/specs/2026-12-19-m5-index-architecture-contract.md`
#   §8.4（数据来源 = `cards.replaced_by`，**不新增表**；正反双向一致；与可见性正交）
#   §8.3（data 键集合不扩张：条目仍恰 from / type / target / reason / path 五键）
#   §6.1（索引问题不阻断读、不改退出码）
# 以及 milestone `M-005` 判据 13（`replaced_by` 反向查询，且与 deleted / deprecated 四象限正交）。
#
# 本脚本守五件事：
#   ① 正反双向读的是**同一条记录**：`A.replaced_by = B` ⇒ `eg rel A --replaced-by` 的正向
#      与 `eg rel B --replaced-by` 的反向逐字一致（记录只有一份、写在失效卡身上，
#      反向不是第二条记录，也不回写目标卡一个字节）；
#   ② 与可见性**正交**：B 的反向对端是 deprecated 的 A ⇒ 默认隐藏并产 Q4，
#      `--include-deprecated` 才展示；**已删除**对端在任何 flag 下都隐藏且不触发 Q4；
#   ③ 链式 A → B → C：中间卡 B 两个方向各恰一条，且链**不传递**（一次查询只看一跳）；
#   ④ 形态边界：条目 `type` 逐字 `replaced_by`、每条恰五键、`eg rel` 的 data 仍恰五键，
#      默认（论证关系）视图**不含**替代指针条目 —— 两个视图互不污染；
#   ⑤ 只读边界：全程零写权威 Markdown、零 commit、工作区不脏；`--replaced-by` 挂到
#      `rel add` / `rel remove` 一律退 1；索引在位与索引删除两条后端结果逐字相等。
#
# 约束：离线、零交互（stdin 接 /dev/null）、可重复执行；只用 bash / coreutils / awk / git / go，
#   无 jq / sqlite3 依赖；一切写只发生在 mktemp -d 沙箱内；任一断言不成立立刻非零退出。
#
# 用法：cd evergreen && bash tests/e2e/index-search/replaced_by_reverse.sh

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# 测试体系公共 helper（EG_FIXTURES / EG_CONTRACTS / 磁盘前置检查）。
. "${REPO_ROOT}/tests/lib/common.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/eg-m5-replaced-by.XXXXXX")"
VAULT="${WORK}/vault"
EG="${WORK}/eg"

OLD='k-20261201-old'   # deprecated，replaced_by → NEW
MID='k-20261201-mid'   # deprecated，replaced_by → NEW（链的中间：被 OLD 取代关系无关，见下）
NEW='k-20261201-new'   # active，链尾
GONE='k-20261201-gone' # deprecated + 已逻辑删除，replaced_by → NEW（删除维度的观察点）

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
gitv() { git -C "${VAULT}" -c user.email=eg@example.com -c user.name=eg "$@"; }
commits() { gitv log --oneline | wc -l | tr -d ' '; }
porcelain() { gitv status --porcelain | sort; }
authority_sha() {
  ( cd "${VAULT}" && { find domains sources proposals -type f 2>/dev/null || true; } | sort |
    xargs -r sha256sum )
}

# —— JSON 取值（键序由合同固定，切片确定；计数 grep 一律 `|| true`：零命中是合法结果）——
out_slice() { sed 's/.*"relations_out":\[//; s/\],"relations_in".*//' "$1"; }
in_slice()  { sed 's/.*"relations_in":\[//; s/\],"scanned_files".*//' "$1"; }
slice_count() { printf '%s' "$1" | { grep -o '"from":"' || true; } | wc -l | tr -d ' '; }
code_count() { { grep -o "\"code\":\"$2\"" "$1" || true; } | wc -l | tr -d ' '; }
has_code() { grep -Fq "\"code\":\"$2\"" "$1"; }

seed_card() { # <id> <title> <status> <replaced_by-target|""> <reason> <deleted:yes|no>
  local id="$1" title="$2" status="$3" target="$4" reason="$5" deleted="$6"
  local dir="${VAULT}/domains/ai-infra/knowledge"
  mkdir -p "${dir}"
  {
    printf '%s\n' '---' "id: ${id}" "status: ${status}" "created_at: '2026-12-01'" \
      "updated_at: '2026-12-01T10:00:00+08:00'" "title: ${title}" 'sources: []'
    if [ -n "${target}" ]; then
      printf '%s\n' 'replaced_by:' "  target: ${target}" "  reason: ${reason}"
    fi
    if [ "${deleted}" = "yes" ]; then
      printf '%s\n' "deleted_at: '2026-12-02T10:00:00+08:00'" 'deleted_reason: 语料造的逻辑删除'
    fi
    printf '%s\n' '---' '' '## 知识内容' '' "正文占位：${title}。" ''
  } >"${dir}/${id}.md"
}

BEFORE_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"

# ---------------------------------------------------------------- 1. 构建 + seed
step "CGO_ENABLED=0 构建 + seed 替代指针语料（OLD→MID→NEW 链 + 一张已删除的 GONE→NEW）"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 go build -o "${EG}" ./cmd/eg) || die "CGO_ENABLED=0 编译失败"
[ "$(eg_code init --domain ai-infra)" = "0" ] || { cat "${WORK}/err.txt"; die "eg init 失败"; }
[ "$(eg_code config set default_domain ai-infra)" = "0" ] || die "config set 失败"
# 链：OLD 被 MID 取代、MID 又被 NEW 取代（指针一律写在**失效**的那张卡上，单向存储）。
seed_card "${OLD}"  "旧结论"   deprecated "${MID}" "旧结论已被中间版本取代" no
seed_card "${MID}"  "中间结论" deprecated "${NEW}" "中间版本又被新证据取代" no
seed_card "${NEW}"  "新结论"   active     ""       ""                       no
seed_card "${GONE}" "废弃结论" deprecated "${NEW}" "废弃后已逻辑删除"       yes
gitv add -A >/dev/null && gitv commit -q -m "seed: m5 替代指针语料"
[ "$(porcelain)" = "" ] || die "前置条件失败：工作区应干净"
BASE_COMMITS="$(commits)"
BASE_STATUS="$(porcelain)"
authority_sha >"${WORK}/authority.before.txt"
[ -s "${WORK}/authority.before.txt" ] || die "权威快照为空：语料没建起来"
ok "四张卡入库（deprecated 链 + 已删除卡），工作区干净"

# ---------------------------------------------------------------- 2. 正向：谁取代了我
step "正向（谁取代了本卡）：至多一条，type 逐字 replaced_by，reason 取权威原值"
# 正向也走同一套可见性策略：OLD 的对端 MID 是 deprecated ⇒ 默认隐藏并产 Q4。
[ "$(eg_code rel "${OLD}" --replaced-by --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "rel --replaced-by 必须退 0"; }
[ "$(slice_count "$(out_slice "${WORK}/out.txt")")" = "0" ] ||
  die "默认视图应隐藏 deprecated 对端（MID），正向应为 0 条"
[ "$(code_count "${WORK}/out.txt" Q4)" = "1" ] || die "默认隐藏 deprecated 对端必须产恰一条 Q4"
[ "$(eg_code rel "${OLD}" --replaced-by --include-deprecated --json)" = "0" ] ||
  { cat "${WORK}/err.txt"; die "rel --replaced-by --include-deprecated 必须退 0"; }
cp "${WORK}/out.txt" "${WORK}/old.fwd.json"
[ "$(code_count "${WORK}/old.fwd.json" Q4)" = "0" ] || die "显式放开后不应再产 Q4"
[ "$(slice_count "$(out_slice "${WORK}/old.fwd.json")")" = "1" ] ||
  { cat "${WORK}/old.fwd.json"; die "OLD 的正向应恰一条"; }
grep -Fq "\"type\":\"replaced_by\"" "${WORK}/old.fwd.json" || die "条目 type 必须逐字 replaced_by"
grep -Fq "\"target\":\"${MID}\"" "${WORK}/old.fwd.json" || die "正向的 target 应为 MID"
grep -Fq '"reason":"旧结论已被中间版本取代"' "${WORK}/old.fwd.json" ||
  die "reason 必须是权威 Markdown 里的逐字原值"
# 新卡没有正向（没人取代它）。
[ "$(eg_code rel "${NEW}" --replaced-by --json)" = "0" ] || die "rel NEW --replaced-by 应退 0"
[ "$(slice_count "$(out_slice "${WORK}/out.txt")")" = "0" ] || die "NEW 不应有正向替代指针"
# 条目恰五键：从条目原文里数键名（多一格 / 少一格都当场红）。
FIRST_EDGE="$(out_slice "${WORK}/old.fwd.json" | sed 's/^{//; s/}.*//')"
for k in from type target reason path; do
  printf '%s' "${FIRST_EDGE}" | grep -Fq "\"${k}\":" || die "条目缺键 ${k}"
done
KEYS_N="$(printf '%s' "${FIRST_EDGE}" | { grep -o '"[a-z_]*":' || true; } | wc -l | tr -d ' ')"
[ "${KEYS_N}" = "5" ] || die "条目键数 = ${KEYS_N}，合同要求恰 5 键"
ok "OLD 正向恰一条（→ MID）、type/reason 逐字正确、条目恰五键；NEW 无正向"

# ---------------------------------------------------------------- 3. 反向：我取代了谁（+ 可见性正交）
step "反向（本卡取代了谁）：默认隐藏 deprecated 对端并产 Q4，--include-deprecated 才展示"
[ "$(eg_code rel "${MID}" --replaced-by --json)" = "0" ] || die "rel MID --replaced-by 应退 0"
cp "${WORK}/out.txt" "${WORK}/mid.default.json"
[ "$(slice_count "$(in_slice "${WORK}/mid.default.json")")" = "0" ] ||
  die "默认视图应隐藏 deprecated 对端（OLD）"
has_code "${WORK}/mid.default.json" Q4 || die "默认隐藏 deprecated 对端必须产 Q4"
[ "$(code_count "${WORK}/mid.default.json" Q4)" = "1" ] || die "Q4 应恰一条"

[ "$(eg_code rel "${MID}" --replaced-by --include-deprecated --json)" = "0" ] ||
  die "rel MID --replaced-by --include-deprecated 应退 0"
cp "${WORK}/out.txt" "${WORK}/mid.open.json"
[ "$(slice_count "$(in_slice "${WORK}/mid.open.json")")" = "1" ] ||
  { cat "${WORK}/mid.open.json"; die "显式放开后 MID 的反向应恰一条（OLD）"; }
grep -Fq "\"from\":\"${OLD}\"" "${WORK}/mid.open.json" || die "反向条目的 from 应为 OLD"
[ "$(code_count "${WORK}/mid.open.json" Q4)" = "0" ] || die "显式放开后不应再产 Q4"

# ① 正反双向读的是同一条记录：OLD 的正向条目原文 == MID 的反向条目原文（逐字）。
FWD_EDGE="$(out_slice "${WORK}/old.fwd.json")"
REV_EDGE="$(in_slice "${WORK}/mid.open.json")"
[ "${FWD_EDGE}" = "${REV_EDGE}" ] ||
  die "正反两个方向不是同一条记录：\n 正向 ${FWD_EDGE}\n 反向 ${REV_EDGE}"
ok "MID 反向默认隐藏 + 恰一条 Q4；放开后恰一条，且与 OLD 的正向条目逐字相同"

# ---------------------------------------------------------------- 4. 与逻辑删除维度正交
step "已删除对端（GONE）在任何 flag 下都隐藏，且不触发 Q4；被查询卡自身状态不参与筛选"
for FLAG in "" "--include-deprecated"; do
  # shellcheck disable=SC2086
  [ "$(eg_code rel "${NEW}" --replaced-by ${FLAG} --json)" = "0" ] || die "rel NEW ${FLAG} 应退 0"
  cp "${WORK}/out.txt" "${WORK}/new.rev.json"
  IN_N="$(slice_count "$(in_slice "${WORK}/new.rev.json")")"
  if [ -z "${FLAG}" ]; then
    # 默认：MID（deprecated）被隐藏 + GONE（已删除）被隐藏 ⇒ 0 条，且 Q4 只为 MID 记一次。
    [ "${IN_N}" = "0" ] || die "默认视图 NEW 的反向应为 0 条，实际 ${IN_N}"
    [ "$(code_count "${WORK}/new.rev.json" Q4)" = "1" ] ||
      die "已删除对端不计入 Q4：Q4 应恰一条（仅 MID）"
  else
    # 放开 deprecated：MID 出现，GONE **仍然**不出现（删除是独立维度，任何 flag 下都隐藏）。
    [ "${IN_N}" = "1" ] || die "放开 deprecated 后 NEW 的反向应恰一条（MID），实际 ${IN_N}"
    grep -Fq "\"from\":\"${MID}\"" "${WORK}/new.rev.json" || die "反向应含 MID"
    printf '%s' "$(in_slice "${WORK}/new.rev.json")" | grep -Fq "${GONE}" &&
      die "已删除对端 ${GONE} 不得出现在任何视图里"
    [ "$(code_count "${WORK}/new.rev.json" Q4)" = "0" ] || die "放开后不应再产 Q4"
  fi
done
# 被查询卡自身已删除 + deprecated，但对端 NEW 是 active ⇒ 它的正向照常可见（筛的是对端）。
[ "$(eg_code rel "${GONE}" --replaced-by --json)" = "0" ] || die "rel GONE --replaced-by 应退 0"
[ "$(slice_count "$(out_slice "${WORK}/out.txt")")" = "1" ] ||
  die "已删除卡的正向（对端 active）应照常可见"
ok "GONE 在任何 flag 下都不出现且不计 Q4；GONE 自己的正向照常可见（四象限正交）"

# ---------------------------------------------------------------- 5. 链式与不传递
step "链式 OLD → MID → NEW：中间卡两个方向各恰一条，且链不传递（只看一跳）"
[ "$(slice_count "$(out_slice "${WORK}/mid.open.json")")" = "1" ] ||
  die "MID 的正向应恰一条（→ NEW）"
grep -Fq "\"target\":\"${NEW}\"" "${WORK}/mid.open.json" || die "MID 的正向 target 应为 NEW"
# NEW 的反向只有 MID / GONE 这一跳，绝不含 OLD（不传递）。
printf '%s' "$(in_slice "${WORK}/new.rev.json")" | grep -Fq "${OLD}" &&
  die "反向查询不得跨跳传递（NEW 的反向不该出现 OLD）"
ok "MID 正反各恰一条；NEW 的反向不含 OLD（一次查询只看一跳）"

# ---------------------------------------------------------------- 6. 两个视图互不污染 + data 键
step "默认（论证关系）视图不含替代指针条目；eg rel 的 data 仍恰五键"
[ "$(eg_code rel "${OLD}" --json)" = "0" ] || die "rel OLD（默认视图）应退 0"
printf '%s' "$(out_slice "${WORK}/out.txt")" | grep -Fq 'replaced_by' &&
  die "默认视图（relations[]）不得出现替代指针条目：两个视图必须互不污染"
[ "$(slice_count "$(out_slice "${WORK}/out.txt")")" = "0" ] ||
  die "OLD 没有 relations[]，默认视图正向应为 0 条"
DATA_KEYS="$(sed 's/.*"data":{//; s/"relations_out".*//' "${WORK}/out.txt")"
printf '%s' "${DATA_KEYS}" | grep -Fq '"id":' || die "data 首键应为 id"
for k in id relations_out relations_in scanned_files skipped_files; do
  grep -Fq "\"${k}\":" "${WORK}/out.txt" || die "data 缺键 ${k}"
done
ok "默认视图零替代指针条目；data 五键齐全"

# ---------------------------------------------------------------- 7. 索引后端 == 扫描后端
step "索引在位与索引删除：替代指针视图逐字相等（Markdown 恒为权威）"
[ "$(eg_code index build --json)" = "0" ] || { cat "${WORK}/err.txt"; die "eg index build 应退 0"; }
eg rel "${NEW}" --replaced-by --include-deprecated --json >"${WORK}/idx.json" </dev/null ||
  die "索引态查询应退 0"
rm -rf "${VAULT}/.index"
eg rel "${NEW}" --replaced-by --include-deprecated --json >"${WORK}/scan.json" </dev/null ||
  die "扫描态查询应退 0"
[ "$(in_slice "${WORK}/idx.json")" = "$(in_slice "${WORK}/scan.json")" ] ||
  die "两条后端的反向替代指针结果不逐字相等"
[ "$(out_slice "${WORK}/idx.json")" = "$(out_slice "${WORK}/scan.json")" ] ||
  die "两条后端的正向替代指针结果不逐字相等"
ok "索引态与扫描态的替代指针视图逐字相等"

# ---------------------------------------------------------------- 8. 只读边界
step "--replaced-by 不作用于写路径；全程零写权威、零 commit、工作区不脏"
[ "$(eg_code rel add "${OLD}" supports "${NEW}" --reason x --replaced-by)" = "1" ] ||
  die "rel add --replaced-by 必须退 1"
[ "$(eg_code rel remove "${OLD}" supports "${NEW}" --reason x --replaced-by)" = "1" ] ||
  die "rel remove --replaced-by 必须退 1"
authority_sha >"${WORK}/authority.after.txt"
diff "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" >/dev/null ||
  { diff "${WORK}/authority.before.txt" "${WORK}/authority.after.txt" || true
    die "权威 Markdown 被改写（替代指针查询恒只读）"; }
[ "$(commits)" = "${BASE_COMMITS}" ] || die "commit 数变了：读路径恒零 commit"
[ "$(porcelain)" = "${BASE_STATUS}" ] || die "工作区被污染"
ok "写子命令拒收 --replaced-by（退 1）；权威字节清单不变、commit +0、工作区干净"

# ---------------------------------------------------------------- 9. 仓库零污染
step "真实仓库工作区零污染自查"
AFTER_REPO_STATUS="$(git -C "${REPO_ROOT}" status --porcelain | sort)"
[ "${BEFORE_REPO_STATUS}" = "${AFTER_REPO_STATUS}" ] ||
  { diff <(printf '%s\n' "${BEFORE_REPO_STATUS}") <(printf '%s\n' "${AFTER_REPO_STATUS}") || true
    die "脚本污染了真实仓库工作区"; }
ok "真实仓库 git status 逐字不变"

printf '\n[PASS] replaced_by_reverse.sh 全部 %d 组断言通过（共 %d 步）\n' "${PASS}" "${STEP}"
