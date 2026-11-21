---
name: codex-image-gen
description: Generate or edit bitmap images through a user-configured compatible API, with cached model availability and recoverable jobs. Also handles explicitly requested video generation. Use this gateway skill when selected; respect an explicit request for imagegen or another tool.
---

# Codex ImageGen

Use the bundled native CLI. This is the portable Codex ImageGen skill, configured through environment variables. Selecting this skill does not force a particular model and does not override an explicit request to use `imagegen` or another tool.

## Executable and quick calls

Select `bin/codex-image-gen-<platform>` once: `darwin-arm64`, `darwin-amd64`, `windows-amd64.exe`, or `windows-arm64.exe`. Normal use requires no Python, Node, Go, or compilation. On macOS, restore execute permission if needed.

Examples below use `<cli>` for that absolute executable path. Prefer absolute output paths; otherwise `--out` is relative to `--out-dir` (default `output/codex-image-gen`).

```text
<cli> generate --prompt "A blue glass bottle containing a golden star" --out "<absolute-path>/bottle.png"
<cli> generate --prompt "A quiet beach" --model gpt-image-2.5-sunburst --size 2K
<cli> edit --image "<absolute-path>/input.png" --prompt "Replace only the background; preserve the subject" --out "<absolute-path>/edited.png"
<cli> models
<cli> doctor
<cli> resume --job "<absolute-path>/job.json"
```

Use `--prompt-file` for long prompts, `--n` for variants of the same prompt, and `generate-batch` for distinct assets. Read [batch-format.md](references/batch-format.md) for batches; [runtime.md](references/runtime.md) for advanced flags, video, configuration diagnosis, and recovery.

## Configuration

Read `CODEX_API_URL` and `CODEX_API_KEY`. On macOS, the CLI also checks the login session in memory when a named value is absent from the current process; it reports the source, never the key. Do not confuse a process visibility problem with the user never having configured a value. Use the offline `doctor` when diagnosis is needed, not before every normal generation.

If the URL is missing, stop before sending a request and offer exactly these meaningful choices: configure the URL, or continue with the preset `https://apinebula.com`. Add `--use-default-url` only after the user selects that option; this flag is consent for that invocation, not a silent fallback or a change to system configuration. Never ask for a key in chat or put one in a command, prompt, report, or file.

## Model selection

Selection order: the user's current explicit model → their most recent explicit model choice in this conversation → `CODEX_IMAGE_DEFAULT_MODEL` → automatic selection. Pass `--model` only for an explicit/conversation/profile choice, or use `--model auto` to select automatically. Naming `$codex-image-gen` selects the skill, not model 2.5.

Automatic image priority is:

1. `gpt-image-2.5`
2. `gpt-image-2.5-sunburst`
3. `gpt-image-2.5-flare`
4. `gpt-image-2`

First intersect this priority with the API model list. Missing IDs are marked `unsupported` and never submitted, even if previously successful. Append other advertised image models after the four preferred IDs, preserving their API return order and removing duplicates; exclude chat/video models. Image models are recognized by their image-model names or advertised `output_modalities: ["image"]`; an unfamiliar advertised image-capable ID can be selected explicitly.

Within that eligible order, use models with cached successful records for this API, credential, and operation. If none has a working record, try the eligible order as the actual requested job until the first valid result or all safely rejected candidates are exhausted. The model catalog is cached for 5 minutes; `models` always refreshes it. Catalog failure stops execution without changing support marks or submitting a generation. Explicit model choices must also be advertised; do not silently replace an unsupported explicit choice. Do not run a separate probe before the real generation. A generation that might already have been accepted stops automatic model switching. A timeout or limit does not permanently invalidate a working model. State includes observation times, not a promise of future availability.

`models` queries what the gateway advertises, updates catalog support marks, and shows separate historical generation availability records. Listing an ID does not mark it working. Only an explicit user request to check availability authorizes `<cli> models --check-availability` (optionally repeat `--model <id>` to limit scope). That operation generates one image per selected image model, may incur charges, and stops on uncertain acceptance or authentication failure. Do not test every model for an ordinary image request.

Map `2`, `Image 2`, or `2.0` to `gpt-image-2`; `2.5`, `2.6`, later versions, Chinese digits, and speech forms such as `以麦 G2.6` to the corresponding `gpt-image-{version}`. `日耀/sunburst` and `闪焰/flare` select that suffix; without a version they refer to the 2.5 family. Explicit gateway IDs pass through. See runtime reference for Grok/video aliases. A model name in quoted image text or an image subject is not a model choice.

## Generate, inspect, and deliver

Preserve the user's actual prompt, exact text, constraints, and requested edits. Add only details that improve the intended image. Inspect reference images before editing and state what must remain unchanged. Keep originals and write edits to new paths.

Model, size, and rendering quality are separate. Retain the user's latest explicit size/quality choices; otherwise use `1K` and `auto`. `高清一点` alone does not change the model or imply 4K. Presets request 1K = 1024×1024, 2K = 2048×2048, 4K = 3840×2160; the upstream may return different dimensions.

Inspect each returned image for subject, composition, exact text, and visible defects. The CLI validates image bytes and reports actual dimensions per file. Report discrepancies plainly; never label a lower-resolution result 4K. Requested quality is not measured output quality. Report the requested model and any provider-reported model separately; successful use of a gateway ID does not verify its backend identity.

Show the resulting image and its absolute path. Keep the response proportional to the task; include actual dimensions and relevant warnings. Do not overwrite an existing file without explicit authorization (`--force`).

## Failure and recovery

The default is up to **3 retries**, waiting **5, 10, 15 seconds**: at most 30 seconds of retry waits per job, excluding request time and normal video-status polling. Authentication/parameter errors do not retry. A longer server `Retry-After` stops this automatic round instead of violating the wait budget.

Queries/downloads may retry. Generation POSTs may retry only after a definite rejection (such as 429 without a task) or with provider-confirmed idempotency support; never enable that support just to obtain retries. Uncertain submissions retain structured recovery information instead of blindly creating another image/video or changing models/endpoints.

On failure, retain the `job_file` and any task/request IDs. Offer the applicable choices: continue the original job/retry round, generate a new job with the user's confirmation, or stop. Explain when a synchronous API supplies no way to query an uncertain submission. `resume` reuses saved results, queries the known video task, or resends the same idempotent request when supported; it does not silently create a new paid job. Read runtime recovery guidance before using it.

Private recovery files under `output/codex-image-gen/.state/` can contain prompts, reference-image bytes, or signed result URLs, but never the main API key. Do not expose these files as public deliverables. A shared writable cache can be selected with `CODEX_IMAGE_STATE_DIR`.
