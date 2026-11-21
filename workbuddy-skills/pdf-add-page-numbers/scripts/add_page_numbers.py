#!/usr/bin/env python3
"""Add 'X / Y' page numbers to a PDF file.

Usage: python add_page_numbers.py <input.pdf>

Saves result as <input>_有页码.pdf in the same directory.
Requires: pypdf, reportlab (auto-installed if missing).
"""

import os, sys, subprocess

# ── Ensure dependencies ──
for lib in ("reportlab", "pypdf"):
    try:
        __import__(lib)
    except ImportError:
        subprocess.check_call([sys.executable, "-m", "pip", "install", lib, "-q"])

from reportlab.pdfgen import canvas
from pypdf import PdfReader, PdfWriter


def add_page_numbers(input_path: str) -> str:
    """Add 'X / Y' page numbers to PDF, return output path."""
    base, ext = os.path.splitext(os.path.basename(input_path))
    output_path = os.path.join(os.path.dirname(input_path), f"{base}_有页码{ext}")

    reader = PdfReader(input_path)
    total = len(reader.pages)

    if total == 0:
        print("Error: PDF has no pages")
        sys.exit(1)

    first_page = reader.pages[0]
    pw = float(first_page.mediabox.width)
    ph = float(first_page.mediabox.height)

    # ── Create watermark PDF with page numbers ──
    watermark_path = os.path.join(os.path.dirname(input_path), "_watermark_temp.pdf")
    c = canvas.Canvas(watermark_path, pagesize=(pw, ph))

    for i in range(total):
        text = f"{i + 1} / {total}"
        c.setFont("Helvetica", 9)
        c.setFillColorRGB(0.4, 0.4, 0.4)
        tw = c.stringWidth(text, "Helvetica", 9)
        c.drawString((pw - tw) / 2, 35, text)
        c.showPage()
    c.save()

    # ── Merge watermark onto original pages ──
    wm_reader = PdfReader(watermark_path)
    writer = PdfWriter()

    reader2 = PdfReader(input_path)
    for i, page in enumerate(reader2.pages):
        if i < len(wm_reader.pages):
            page.merge_page(wm_reader.pages[i])
        writer.add_page(page)

    writer.write(output_path)
    os.remove(watermark_path)

    size_kb = os.path.getsize(output_path) // 1024
    print(f"Done: {output_path}  ({total} pages, {size_kb} KB)")
    return output_path


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python add_page_numbers.py <input.pdf>")
        sys.exit(1)
    add_page_numbers(sys.argv[1])
