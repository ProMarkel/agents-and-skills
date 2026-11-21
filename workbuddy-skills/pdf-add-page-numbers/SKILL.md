---
name: pdf-add-page-numbers
description: Add "X / Y" format page numbers to PDF documents. This skill should be used when the user asks to add page numbers to a PDF file, add pagination, or label pages with "X / Y" style numbering. It generates a copy of the PDF with "_有页码" suffix in the same directory.
---

# PDF Add Page Numbers

## Overview

Add page numbers in "X / Y" format to any PDF document. The skill overlays page numbers at the bottom center of each page (Helvetica 9pt, gray) and saves the result as `<original_name>_有页码.pdf` in the same directory.

## When to Use

Triggers on requests like:
- "给这个 PDF 添加页码"
- "add page numbers to this PDF"
- "给 PDF 加 X / Y 格式的页码"
- Any mention of adding pagination or page numbering to a `.pdf` file

## How to Use

### Single PDF

Execute the script directly with the input PDF path:

```bash
python scripts/add_page_numbers.py "<path/to/file.pdf>"
```

The script:
1. Auto-installs `pypdf` and `reportlab` if missing
2. Reads the PDF to determine page count and dimensions
3. Creates a watermark layer with "X / Y" page numbers (bottom center, Helvetica 9pt, #666 gray)
4. Merges the watermark onto the original pages
5. Saves as `<original_name>_有页码.pdf` in the same directory
6. Cleans up the temporary watermark file

### Multiple PDFs

To process multiple PDFs, run the script for each file:

```bash
python scripts/add_page_numbers.py "file1.pdf"
python scripts/add_page_numbers.py "file2.pdf"
```

### Dependencies

- `pypdf` (PDF reading/writing)
- `reportlab` (watermark PDF generation)

Both are auto-installed on first run if missing.

## Output

| Detail | Value |
|--------|-------|
| File name | `<original>_有页码.pdf` |
| Location | Same directory as input |
| Format | "1 / N", "2 / N", ... |
| Position | Bottom center, ~35pt from edge |
| Font | Helvetica, 9pt |
| Color | Gray (#666666) |
