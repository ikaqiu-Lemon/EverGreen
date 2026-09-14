#!/usr/bin/env python3
"""D7「任何等待 / sleep <= 120 秒」的唯一扫描实现（被 D7 门禁调用的共享库）。

放在 tests/lib/ 而不是 tests/tools/ 的理由：本文件**不是独立判据**（自己不判红、不返回判定
退出码），而是 tests/contract/resource-limits/limits.sh 的扫描后端，语义上与 tests/lib/common.sh
同类。它的正确性由那个门禁的反例自证（A8-1 / A8-5 / A8-6 / A8-7）负责。

为什么要抽出来：门禁的 A7（真实盘面判据）与 A8（反例自证）此前各自内联了一份 mini 扫描器，
两份实现会漂移 —— 反例可能在一份实现上变红、真实判据那份却看不见。现在两边调用同一个入口，
反例证明的就是真实判据本身。

分类（每一处命中都必须落到某一类，不存在"扫到但不处理"）：

  EXEC 类（**会被执行**的等待，D7 硬上限的真正对象）
    判定：把整行的注释与字符串字面量（含 .py 三引号块、.go 反引号裸串，跨行跟踪）剔除后，
          剩下的"代码面"里仍出现 `sleep <字面量>`（shell）或
          `time.Sleep(<字面量> * time.Second|Minute)`（Go）。
          注意 shell 反引号是命令替换、heredoc 体是待执行内容，二者都算代码面（从严）。
    处置：
      OVER    超过上限且无标记 -> 违规，调用方必须判红。
      EXEMPT  超过上限但带 `eg:sleep-exempt <理由>` -> 登记豁免。豁免只用于"停在某点把进程
              挂住、由父测试 kill 终结"的崩溃留态 harness：墙钟由父测试决定，数字本身不构成
              等待。豁免点数量由调用方封顶，且理由必须自述 kill 语义。

  TEXT 类（只出现在注释 / 字符串里，本行**不会**产生等待）
    判定：原始行里有超限字面量，但代码面里没有。
    存在的必要：门禁自己要生成并打印超限反例字面量来自证判据非空；若不区分，门禁会把自己的
                反例夹具判成违规（自指假红）。
    处置：不是无条件放过 —— 只有在文件头 `eg:sleep-text-fixture-file <理由>` 显式登记过的
          文件里才允许，其余一律 OVER。因此"把超限字面量藏进字符串再 bash -c 执行"这种绕过
          依旧会红：那个文件没有登记，TEXT 命中直接变 OVER。
          登记必须写在**注释行**（.py/.sh 的 `#` 行、.go 的 `//` 行）的前 DECL_SCAN_LINES 行内：
          否则任何"在字符串里出现过标记名"的文件（包括本文件的常量定义与这段说明）都会被
          误认成已登记，登记就形同虚设。

  INFO 类（不可执行的文件：文档 / 清单 / 数据）
    .md/.yaml/.yml/.tsv/.json/.txt 等不会被当作程序执行，其中的字面量不可能造成等待，只做
    计数输出，不参与判红。可执行扩展名（.sh/.bash/.py/.go）一律按上面两类严判。

输出：每行一条 TAB 分隔记录 `<KIND>\t<rel>:<lineno>\t<what>\t<reason>`，
      KIND in {OVER, EXEMPT, TEXTFIXTURE, INFO}；调用方只依据 KIND 判红。
      `--print-decl` 额外输出 `DECL\t<rel>\t<理由>`，供调用方封顶登记文件数。

用法：
  python3 tests/lib/sleep_scan.py --repo <repo> --git-subtree tests --cap 120
  python3 tests/lib/sleep_scan.py --root <dir> --cap 120        # 扫任意（含未跟踪）目录
"""

from __future__ import annotations

import argparse
import os
import re
import subprocess
import sys

EXECUTABLE_EXT = {".sh", ".bash", ".py", ".go"}
DECL_MARKER = "eg:sleep-text-fixture-file"
EXEMPT_MARKER = "eg:sleep-exempt"
DECL_SCAN_LINES = 60

PAT_SH = re.compile(r"\bsleep\s+([0-9]+(?:\.[0-9]+)?)\b")
PAT_GO = re.compile(
    r"time\.Sleep\(\s*([0-9]+(?:\.[0-9]+)?)\s*\*\s*time\.(Second|Minute)\b"
)


def code_faces(lines: list[str], ext: str) -> list[str]:
    """逐行返回"代码面"：剔除注释与字符串字面量后剩下的部分。

    只做到"足以判定 sleep 是否位于可执行位置"的程度，且跨行跟踪未闭合的多行字面量
    （.py 的三引号块、.go 的反引号裸串）—— 否则文档字符串里的示例会被误判成可执行等待
    （自指假红），或反过来被人利用来藏东西。被剔除的区间用空格填充，保证列位不漂移、
    也不会把两侧标识符粘连出假命中。
    """
    faces: list[str] = []
    multi: str | None = None  # 跨行未闭合的多行字面量定界符
    for line in lines:
        buf: list[str] = []
        quote: str | None = None
        i, n = 0, len(line)
        while i < n:
            if multi is not None:
                if line.startswith(multi, i):
                    buf.append(" " * len(multi))
                    i += len(multi)
                    multi = None
                else:
                    buf.append(" ")
                    i += 1
                continue
            ch = line[i]
            if quote is None:
                # 多行字面量起始：.py 三引号 / .go 反引号裸串
                if ext == ".py" and (line.startswith('"""', i) or line.startswith("'''", i)):
                    multi = line[i : i + 3]
                    buf.append("   ")
                    i += 3
                    continue
                if ext == ".go" and ch == "`":
                    multi = "`"
                    buf.append(" ")
                    i += 1
                    continue
                # 行注释
                if ch == "#" and ext != ".go" and (i == 0 or line[i - 1].isspace()):
                    break
                if ext == ".go" and line.startswith("//", i):
                    break
                # 单行引号（shell 的反引号是命令替换，按代码面处理，不在此列）
                if ch in ("'", '"'):
                    quote = ch
                    buf.append(" ")
                    i += 1
                    continue
                buf.append(ch)
                i += 1
                continue
            # 引号内：整体抹成空格
            if ch == "\\" and quote != "'" and i + 1 < n:
                buf.append("  ")
                i += 2
                continue
            if ch == quote:
                quote = None
            buf.append(" ")
            i += 1
        faces.append("".join(buf))
    return faces


def is_comment_line(line: str, ext: str) -> bool:
    """整行注释判定（文件级登记只在注释行上生效）。

    不这样限制的话，"字符串里出现过标记名"就等于登记 —— 本文件的 DECL_MARKER 常量定义
    和头部说明都会把自己登记掉，登记也就不再是一个需要显式书写的治理动作。
    """
    s = line.lstrip()
    return s.startswith("//") if ext == ".go" else s.startswith("#")


def hits(text: str, ext: str) -> list[tuple[float, str]]:
    found: list[tuple[float, str]] = []
    for m in PAT_SH.finditer(text):
        found.append((float(m.group(1)), f"sleep {m.group(1)}"))
    if ext == ".go":
        for m in PAT_GO.finditer(text):
            secs = float(m.group(1)) * (60 if m.group(2) == "Minute" else 1)
            found.append((secs, f"time.Sleep {m.group(1)}*{m.group(2)}"))
    return found


def iter_files(args) -> list[tuple[str, str]]:
    """返回 (显示用相对路径, 绝对路径) 列表。"""
    items: list[tuple[str, str]] = []
    if args.git_subtree is not None:
        rels = subprocess.run(
            ["git", "ls-files", args.git_subtree],
            cwd=args.repo,
            capture_output=True,
            text=True,
            check=True,
        ).stdout.split()
        for rel in rels:
            items.append((rel, os.path.join(args.repo, rel)))
    else:
        for dirpath, dirs, files in os.walk(args.root):
            dirs[:] = [d for d in dirs if d != ".git"]
            for f in sorted(files):
                p = os.path.join(dirpath, f)
                items.append((os.path.relpath(p, args.root), p))
    keep = []
    for rel, p in items:
        norm = "/" + rel.replace(os.sep, "/") + "/"
        # 归档区按 D9 原文封存，不参与当前判据面。
        if "/archive/" in norm:
            continue
        if os.path.isfile(p):
            keep.append((rel, p))
    return keep


def scan(args) -> list[str]:
    records: list[str] = []
    for rel, path in iter_files(args):
        ext = os.path.splitext(path)[1]
        try:
            lines = open(path, encoding="utf-8", errors="replace").read().splitlines()
        except OSError:
            continue
        executable = ext in EXECUTABLE_EXT
        declared = ""
        if executable:
            for line in lines[:DECL_SCAN_LINES]:
                if DECL_MARKER in line and is_comment_line(line, ext):
                    declared = line.split(DECL_MARKER, 1)[1].strip()
                    break
            if declared and args.print_decl:
                records.append(f"DECL\t{rel}\t{declared}")
        faces = code_faces(lines, ext) if executable else []
        for i, line in enumerate(lines, 1):
            over_raw = [(s, w) for s, w in hits(line, ext) if s > args.cap]
            if not over_raw:
                continue
            if not executable:
                for _secs, what in over_raw:
                    records.append(f"INFO\t{rel}:{i}\t{what}\t不可执行文件（仅计数）")
                continue
            exec_over = {w for s, w in hits(faces[i - 1], ext) if s > args.cap}
            for _secs, what in over_raw:
                if what in exec_over:
                    if EXEMPT_MARKER in line:
                        reason = line.split(EXEMPT_MARKER, 1)[1].strip()
                        records.append(f"EXEMPT\t{rel}:{i}\t{what}\t{reason}")
                    else:
                        records.append(f"OVER\t{rel}:{i}\t{what}\t可执行位置的超限等待")
                    continue
                # TEXT：只在注释 / 字符串里。仅登记过的文件允许。
                if declared:
                    records.append(f"TEXTFIXTURE\t{rel}:{i}\t{what}\t{declared}")
                else:
                    records.append(
                        f"OVER\t{rel}:{i}\t{what}\t"
                        f"注释/字符串内的超限字面量，且文件未用 {DECL_MARKER} 登记"
                    )
    return records


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo")
    ap.add_argument("--git-subtree")
    ap.add_argument("--root")
    ap.add_argument("--cap", type=float, required=True)
    ap.add_argument("--print-decl", action="store_true")
    args = ap.parse_args(argv)
    if args.git_subtree is not None and not args.repo:
        ap.error("--git-subtree 需要 --repo")
    if args.git_subtree is None and not args.root:
        ap.error("需要 --root 或 --repo/--git-subtree")
    records = scan(args)
    sys.stdout.write("\n".join(records) + ("\n" if records else ""))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
