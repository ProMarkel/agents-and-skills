---
name: course-guide-builder
description: Build interactive HTML study guides for video courses (e.g. Bilibili course series). The skill generates a complete HTML page with module tabs, expandable episode cards, embedded video players, knowledge point lists, and practice exercises (quizzes + hands-on tasks). Trigger when user shares a video course URL and asks to create a study guide, learning notes, or course summary page.
---

# Course Guide Builder

Build interactive HTML study guides for video course series. Given a course video URL (Bilibili or similar), generate a self-contained HTML file with expandable episode cards, embedded video players, knowledge points, and practice exercises.

## Overview

This skill produces a single HTML file (`course-study-guide.html`) containing:
- **Header**: Course title, subtitle, video source link
- **Stats row**: Episode count, module count, knowledge point count, exercise count
- **Sticky module tabs**: Clickable tabs that remain fixed at top when scrolling, switch between course modules
- **Episode cards**: Expandable cards with video embed, knowledge points, and exercises
- **Bilibili video embed**: Official player API with lazy loading (iframe only loads on expand)
- **Interactive quizzes**: Multiple-choice questions with instant feedback
- **Practice tasks**: Coded/hands-on/thought exercises with tags
- **Embed mode**: When loaded with `?embed=1`, header and stats are hidden — designed for iframe display in knowledge base layouts
- **Scrollbar-gutter stable**: Prevents content shift when scrollbar appears/disappears in embed mode

## Workflow

### Step 1: Understand the Course

When the user provides a video course URL:

1. Use `WebFetch` to fetch the Bilibili video page and extract:
   - Course title (from page title or og:title)
   - Total episode count (check if it's a multi-part video)
   - Brief description of course content

2. If the URL is Bilibili format `https://www.bilibili.com/video/BVxxxxxx`, note the BVID for embedding.

3. Ask the user to confirm the course structure (number of episodes, module划分) if not clearly available from the page.

4. Note: Bilibili's pure player embed (`player.bilibili.com/player.html`) does NOT show the subtitle button. Use the full video page embed (`www.bilibili.com/video/BVxxx`) instead — subtitles are then accessible via the player's subtitle button.

### Step 2: Design the Guide Structure

Plan the guide's module and episode structure:

- **Modules**: Group episodes into 3-6 logical modules (e.g., "Basics", "Core Practice", "Advanced", "Summary")
- **Episodes**: For each episode, plan:
  - `num`: Episode number (e.g., "01", "02")
  - `title`: Chinese title
  - `en`: English title or description
  - `dur`: Duration (e.g., "12:34")
  - `points`: 3-5 core knowledge points (bullet points)
  - `exercises`: 1-3 exercises (mix of quiz and hands-on)

**Important**: The Bilibili embed uses `page=N` parameter where N starts from 1. Episode index `idx` in the array maps to `page=idx+1`.

### Step 3: Generate the HTML

Copy the template from `assets/course-guide-template.html` as the starting point. This template contains:

- Complete CSS with CSS variables (`:root` with `--bg`, `--surface`, `--text`, `--accent`, `--accent-light`, etc.)
- Module tabs navigation
- Episode card structure with expand/collapse interaction
- Video wrapper with 16:9 aspect ratio
- Knowledge points list (`.points-list`)
- Exercise cards (`.q-card` for quizzes, `.task-item` for hands-on)
- JavaScript for tab switching, episode toggle, and quiz answer checking

**Fill in the template:**
1. Update `<title>` and header `<h1>` with course name
2. Update stats row numbers
3. Update `modules` JS array with module definitions
4. Update `episodes` JS array with all episode data
5. Add the Bilibili embed URL in `renderEpisodes()`: `src="https://www.bilibili.com/video/BVxxxxxx?p=${idx + 1}"` with `scrolling="no"` attribute.
   - Full page embed is required for the subtitle button to appear; the pure player embed does not show it.

### Step 4: Preview and Iterate

1. Use `preview_url` to show the generated HTML file
2. Verify:
   - Module tabs switch correctly and remain sticky on scroll
   - Episode cards expand/collapse smoothly
   - Video embeds load on expand (check browser console for iframe errors)
   - Quizzes show correct/wrong feedback
   - Responsive on mobile (narrow viewport)
3. Verify embed mode: add `?embed=1` to the URL and confirm:
   - Header and stats row are hidden
   - Container expands to full width
   - Module tabs stick at top with no gap
   - Scrollbar-gutter prevents content shift
4. Ask user for feedback and iterate

## HTML Template Reference

The template file `assets/course-guide-template.html` is the canonical reference. Key sections:

### CSS Variables (`:root`)

```css
:root {
  --bg: #fafafa;
  --surface: #fff;
  --text: #1a1a2e;
  --text-secondary: #555;
  --text-muted: #888;
  --border: #e8e8ef;
  --accent: #534AB7;          /* Purple - primary color */
  --accent-light: #EEEDFE;    /* Light purple - hover/expanded state */
  --success: #0F6E56;
  --success-light: #E1F5EE;
  --warning: #854F0B;
  --warning-light: #FAEEDA;
  --radius: 12px;
  --radius-sm: 8px;
  --transition: 0.2s cubic-bezier(0.16, 1, 0.3, 1);
}
```

### Module Tabs Sticky Positioning

Module tabs use `position: sticky` to remain visible when scrolling down the page:

```css
.module-tabs {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 32px;
  padding: 4px;
  background: var(--surface);
  border: 0.5px solid var(--border);
  border-radius: var(--radius);
  position: sticky;
  top: 16px;
  z-index: 10;
}
```

- `top: 16px` provides breathing room from the top edge
- `z-index: 10` ensures tabs stay above episode cards
- `background: var(--surface)` prevents content from showing through when fixed
- In embed mode, `margin-top` is reset to `0` (see Embed Mode section below)

### Expand/Collapse Card Styling

**Collapsed state** (default): White background, purple circle number, black title, gray subtitle, gray duration pill.

**Expanded state** (`.episode-card.open`):
- Header background: `var(--accent-light)` (light purple `#EEEDFE`)
- Number circle: white background (`#fff`), purple text (`var(--accent)`)
- Title: purple text (`var(--accent)`)
- Subtitle and duration: unchanged (gray)
- Arrow: rotated 90deg

```css
.episode-card.open .episode-header {
  background: var(--accent-light);
  border-radius: var(--radius) var(--radius) 0 0;
}
.episode-card.open .episode-header .episode-num {
  background: #fff;
  color: var(--accent);
}
.episode-card.open .episode-header .episode-title {
  color: var(--accent);
}
```

### Video Embed

**Important**: Bilibili's pure player embed (`player.bilibili.com/player.html`) does NOT show the subtitle button. Use the full video page embed with CSS cropping instead.

```html
<div class="video-wrapper">
  <iframe src="https://www.bilibili.com/video/BVxxxxxx?p=${idx + 1}"
    allowfullscreen loading="lazy" scrolling="no">
  </iframe>
</div>
```

```css
.video-wrapper {
  width: calc(100% + 44px);
  margin-left: -22px;
  margin-right: -22px;
  height: 0;
  padding-bottom: 58.5%;
  margin-top: 0;
  margin-bottom: 16px;
  overflow: hidden;
  position: relative;
  background: #000;
}
.video-wrapper iframe {
  position: absolute;
  top: 0; left: 0;
  width: 100%;
  height: 100%;
  border: none;
  display: block;
}
```

**Design rationale**:
- Full Bilibili page (`www.bilibili.com/video/BVxxx`) is used instead of the pure player because subtitles are only available on the full page
- Container uses breakout width (`calc(100% + 44px)` with `-22px` margins) to fill the full content area, bypassing parent padding
- `padding-bottom: 58.5%` is the empirically-tested perfect ratio — crops the Bilibili page to show only the video player area with controls, hiding navigation/header/recommendations
- `scrolling="no"` prevents the iframe from being scrollable, hiding the rest of the Bilibili page
- `overflow: hidden` on the wrapper crops any overflow from the iframe
- The page content width should be `max-width: 1080px` for this cropping to work correctly
- The Bilibili page's own "web fullscreen" button can be manually clicked by the user for a perfect fit

**Critical**: `loading="lazy"` ensures iframes only load when the card is expanded, not on page load. With 40+ episodes, eager loading causes severe performance issues.

### Quiz Interaction

Quiz cards (`.q-card`) have clickable options. When clicked:
1. Disable all options (`pointer-events: none`)
2. If correct: add `.correct` class (green highlight)
3. If wrong: add `.wrong` class (red highlight), also add `.correct` to the right answer
4. Show feedback div (`.q-feedback.show`)

```js
function checkAnswer(el, feedbackId, chosen, correct) {
  const fb = document.getElementById(feedbackId + '_fb');
  const allOpts = el.parentElement.querySelectorAll('.q-opt');
  allOpts.forEach(o => { o.classList.remove('correct', 'wrong'); o.style.pointerEvents = 'none'; });
  if (chosen === correct) {
    el.classList.add('correct');
    fb.className = 'q-feedback correct show';
  } else {
    el.classList.add('wrong');
    allOpts[correct].classList.add('correct');
    fb.className = 'q-feedback wrong show';
  }
}
```

## Embed Mode Support

Every generated study guide must include embed mode support. This allows the HTML file to function both as a standalone page and as embedded content inside an iframe (e.g., in a knowledge base layout like `notes.html`).

### Embed Mode CSS

```css
/* Embed mode: hide header, expand container for iframe display */
.embed header { display: none !important; }
.embed .container { max-width: 100% !important; padding-top: 0 !important; }
.embed .stats-row { display: none !important; }
.embed .module-tabs { margin-top: 0 !important; }
html.embed, body.embed { scrollbar-gutter: stable; }
```

Key points:
- `.embed header` and `.embed .stats-row` are hidden — they're redundant when inside an iframe
- `.embed .container` expands to full width and removes top padding
- `.embed .module-tabs` resets `margin-top` to `0` so the sticky tabs sit flush at the top
- `scrollbar-gutter: stable` on both `html.embed` and `body.embed` — prevents content from shifting left when the scrollbar appears/disappears as the iframe height changes (e.g., expanding/collapsing episode cards)

### Embed Mode JavaScript

Place this script **immediately after `<body>`**, before `<div class="container">`:

```html
<script>
if (window.location.search.includes('embed=1')) {
  document.addEventListener('DOMContentLoaded', function() {
    document.body.classList.add('embed');
    document.documentElement.classList.add('embed');
  });
}
</script>
```

- Detects `?embed=1` in the URL query string
- Waits for `DOMContentLoaded` to ensure both `body` and `documentElement` exist
- Adds `.embed` class to both — CSS rules target `.embed` on parent for `html.embed` / `body.embed` selectors

### Activation

To load a study guide in embed mode, use: `course-study-guide.html?embed=1`

## Optional Components

### `.key-concept` Card

For courses that need concept explanation cards (as seen in the Claude Code study guide), use the `.key-concept` component:

```css
.key-concept {
  background: var(--surface);
  border: 0.5px solid var(--accent-light);
  border-radius: var(--radius-sm);
  padding: 12px 16px;
  margin-bottom: 8px;
  font-size: 13px;
  line-height: 1.6;
}
.key-concept .kc-tag {
  font-size: 11px;
  font-weight: 500;
  color: var(--accent);
  margin-bottom: 2px;
}
```

```html
<div class="key-concept">
  <div class="kc-tag">核心概念</div>
  <div>概念说明文字……</div>
</div>
```

### `.empty-state` Placeholder

For modules with no episodes or loading states:

```css
.empty-state {
  text-align: center;
  padding: 60px 20px;
  color: var(--text-muted);
}
```

## Bilibili Embed API Reference

- **Player URL**: `https://player.bilibili.com/player.html?bvid={BVID}&page={PAGE_NUM}`
- `bvid`: The Bilibili video ID (e.g., `BV1JZ4y1i7az`)
- `page`: Part number (1-based, matches video playlist order)
- The player API auto-matches the video's aspect ratio
- For non-Bilibili platforms, use their standard embed URL format

## Common Pitfalls

| Pitfall | Solution |
|---------|----------|
| 41 iframes loaded eagerly = page hang | Always use `loading="lazy"` on video iframes |
| Expand state color too dark | Use `#EEEDFE` (light purple), NOT dark purple. Matches module tab active state |
| Quiz feedback not showing | Check `feedbackId` matches `id` attribute: `ep${idx}_q${ei}_fb` |
| Bilibili page param wrong | `page=1` is first episode, so array index `idx` maps to `page=idx+1` |
| Mobile layout broken | Check `.module-tab` has `min-width: 110px` (can be 120px for wider tabs) and `flex-wrap: wrap` |
| Module tabs not sticking on scroll | Ensure `position: sticky; top: 16px; z-index: 10;` and `background: var(--surface)` are set |
| Embed mode header still visible | Verify `.embed` class is added to both `body` and `documentElement` with `DOMContentLoaded` |
| iframe content shifts when scrollbar appears | Set `html.embed, body.embed { scrollbar-gutter: stable; }` |
| Embed mode sticky tabs have top gap | Reset `.embed .module-tabs { margin-top: 0 !important; }` |
| Bilibili subtitles not showing | Use full video page embed (`www.bilibili.com/video/BVxxx`) with CSS cropping — the pure player embed does not show the subtitle button. User can manually click subtitle button on the embedded video. |

## Resources

- **Template**: `assets/course-guide-template.html` — Copy this as starting point for every new guide
- **Reference**: This SKILL.md contains all CSS patterns, JS logic, and Bilibili embed details needed to build the guide
