#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
从 A4 版式 HTML 导出 PDF（中文简历专用）。

核心陷阱：Edge headless 的 --print-to-pdf 在**非 ASCII 路径**（含中文/空格）下
会将 PDF 错误输出到用户 Desktop，或因编码问题静默失败。
本脚本先将输入 HTML 复制为 ASCII 临时副本，再导出，最后把结果移回目标路径。

用法:
    python export_pdf.py <input.html> <output.pdf> [--edge "C:\\path\\msedge.exe"]

依赖: 系统已安装 Microsoft Edge（桌面版）。
"""
import subprocess
import shutil
import os
import sys
import tempfile


def find_edge():
    candidates = [
        r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
        r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    ]
    for c in candidates:
        if os.path.exists(c):
            return c
    # 回退到 PATH
    return "msedge"


def export(input_html, output_pdf, edge=None):
    if not os.path.exists(input_html):
        raise FileNotFoundError(f"输入 HTML 不存在: {input_html}")

    edge = edge or find_edge()
    tmp_dir = tempfile.gettempdir()
    # ASCII 临时副本，避免中文路径陷阱
    tmp_html = os.path.join(tmp_dir, "resume_export_tmp.html")
    tmp_pdf = os.path.join(tmp_dir, "resume_export_tmp.pdf")

    shutil.copy2(input_html, tmp_html)

    uri = "file:///" + tmp_html.replace(os.sep, "/").replace(" ", "%20")
    # --no-sandbox + 独立 --user-data-dir 是必需的：否则在无显示环境 / 默认 profile
    # 被占用时静默失败（exit 0 但不产出 PDF）。
    ud = tempfile.mkdtemp(prefix="edge_ud_")
    cmd = [
        edge,
        "--headless=new",
        "--no-sandbox",
        "--disable-gpu",
        "--user-data-dir=" + ud,
        "--no-pdf-header-footer",
        "--print-to-pdf=" + tmp_pdf,
        uri,
    ]

    r = subprocess.run(cmd, capture_output=True, text=True, timeout=90, errors="ignore")
    if r.returncode != 0:
        # 仅打印警告，继续尝试查找产物
        sys.stderr.write(f"[warn] edge exit={r.returncode}\n")

    # Edge 可能在 Desktop 落盘，做兜底查找
    desktop = os.path.join(os.path.expanduser("~"), "Desktop", "resume_export_tmp.pdf")
    src = None
    for cand in (tmp_pdf, desktop):
        if os.path.exists(cand):
            src = cand
            break

    if not src:
        raise RuntimeError("PDF 导出失败：未在临时目录或 Desktop 找到产物。请确认 Edge 已安装。")

    os.makedirs(os.path.dirname(os.path.abspath(output_pdf)), exist_ok=True)
    shutil.move(src, output_pdf)
    try:
        os.remove(tmp_html)
    except OSError:
        pass
    # 清理临时 user-data-dir
    try:
        shutil.rmtree(ud, ignore_errors=True)
    except Exception:
        pass
    return output_pdf


if __name__ == "__main__":
    if len(sys.argv) < 3:
        print("用法: python export_pdf.py <input.html> <output.pdf> [--edge PATH]")
        sys.exit(1)
    edge_arg = None
    args = sys.argv[1:]
    if "--edge" in args:
        i = args.index("--edge")
        edge_arg = args[i + 1]
        args = args[:i] + args[i + 2:]
    out = export(args[0], args[1], edge=edge_arg)
    print(f"PDF 已生成: {out}  ({os.path.getsize(out)//1024} KB)")
