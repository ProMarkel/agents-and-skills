# Runtime reference

Read only the section needed: configuration; parameters and aliases; availability; retries; recovery; video; development.

## Configuration and diagnosis

- Required: `CODEX_API_KEY`; API base: `CODEX_API_URL`.
- Process environment takes precedence. On macOS, absent named settings may be resolved from the GUI login-session environment, in memory, once per invocation. Only the four API/default-model settings are recognized; no session dump is logged or saved. `CODEX_IMAGE_LOGIN_SESSION=0` disables this integration.
- `doctor` uses no network. It reports executable/platform, URL and key sources, key visibility, defaults, state location, and output-directory writability. A visible key is not proof that it is valid or funded.
- A missing URL produces `configuration_required` and two choices. Configure it locally, or explicitly choose `--use-default-url` for the preset `https://apinebula.com`. Do not persist that choice or change unrelated Codex/shell configuration unless requested.
- API roots are normalized with `/v1`. Prefer HTTPS. An explicitly configured HTTP API remains supported, with a transport warning in `doctor`, so an upgrade does not silently disable an existing service. HTTPS API result downloads/redirects cannot downgrade to HTTP. API URLs cannot embed credentials or query parameters.
- `CODEX_IMAGE_DEFAULT_MODEL` and `CODEX_VIDEO_DEFAULT_MODEL` are optional. An explicit user model overrides defaults. Automatic image selection uses the documented priority; video defaults to `grok-imagine-video`.
- `CODEX_IMAGE_STATE_DIR` optionally selects a shared writable recovery/cache directory. Default: `output/codex-image-gen/.state/`, relative to the project. Model catalogs are separated by API root and credential fingerprint; generation availability also distinguishes image operation. The PM scope identifier is separate from the original skill even if a state directory is shared. The main key is never stored. Changing credentials/API naturally creates a separate scope.
- `CODEX_API_IDEMPOTENCY_SUPPORTED=1` is an advanced declaration that the configured provider guarantees deduplication using `Idempotency-Key`. Set it only on documented provider support, not because arbitrary headers are accepted. Without it, uncertain generation submissions are not automatically replayed.

Never print full configuration dumps, Authorization headers, or keys. Error output redacts known credentials and sensitive authentication fields, while preserving useful status/reason/request identifiers. Result downloads do not receive the primary API key. An authenticated API content endpoint receives it only on the API origin; cross-origin redirects lose it. Generation redirects are not followed.

## Image parameters and aliases

`generate`, `edit`, and `generate-batch` share:

| Flag | Meaning/default |
|---|---|
| `--model` | Omit for automatic selection; explicit ID or spoken alias; `auto` explicitly selects automatic mode |
| `--size` | `1K`, `2K`, `4K`, `auto`, or `WIDTHxHEIGHT`; default `1K` |
| `--quality` | `low`, `medium`, `high`, `auto`; default `auto` |
| `--n` | 1–10 variants of one prompt |
| `--out-dir` | Default `output/codex-image-gen` |
| `--out` | `generate`/`edit`: absolute path, or a path relative to `--out-dir` |
| `--force` | Explicit overwrite authorization; never infer it |
| `--timeout` | Per HTTP request timeout, seconds; default 150 |
| `--max-retries` | 0–3 additional safe attempts, default 3 |
| `--max-attempts` | Legacy alternative, 1–4 total attempts; cannot combine with `--max-retries` |
| `--dry-run` | No network or availability writes; displays the plan without requiring a key |

Automatic names contain a prompt hash and random task suffix. Relative nested paths resolve under `--out-dir`. Batch destinations are checked for collisions before any submission. Exclusive locks and atomic publication also protect concurrent processes. A lock left after a crashed process requires checking that process/task before removing it; do not bypass a live lock with `--force`.

Supported validated image formats are PNG and JPEG. Automatic filenames adopt the actual format. Explicit file extensions must match returned content (`.png`, `.jpg`, `.jpeg`). If the API returns another supported format, retain the recovery manifest and `resume --out <correct-extension>`; do not regenerate merely to fix the filename. Unsupported or corrupt bytes are a delivery error, never success. Each image is bounded to 64 MiB and 50 million decoded pixels; HTTP responses are bounded to 128 MiB.

Model aliases preserve existing behavior:

- `Image 2`, `2`, `2.0` → `gpt-image-2`.
- Versions `2.5`, `2.6`, `2.7`, later IDs, Chinese/full-width digits and `G2.6`/`以麦 G2.6` resolve to the corresponding GPT model.
- `flare/闪焰`, `sunburst/日耀` append the variant; no explicit version means the 2.5 family unless an explicit GPT default is configured.
- `latest/最新` → `chatgpt-image-latest`, only when explicitly requested; it does not establish which model is newest.
- `Grok` → `grok-imagine-image-2.0`; `Grok 高质量` → `grok-imagine-image-quality`; explicit `grok-imagine` stays unchanged.
- Generic `高质量/高清` changes `--quality`, not model or resolution. The existing Grok 2.0 adapter maps `high` to `auto`; report sent versus requested quality separately. Its 4K choice requests the adapter's highest slot and does not promise native 4K.

For edits, repeat `--image` for references and use `--mask` for a compatible mask. Preserve originals and invariants in the prompt. Prompt structure is optional: asset/use; subject/scene; style; composition; lighting; verbatim text; constraints; avoid. Do not invent content to fill a template.

## Availability: list versus test

```text
<cli> models
<cli> models --all
<cli> models --check-availability
<cli> models --check-availability --model gpt-image-2.5 --model gpt-image-2
```

The first two are list queries, with separate historical availability records. They do not mark an advertised ID usable and do not generate test images. The latter commands are explicit paid probes: one 1K image per selected image model, one attempt each, and a per-model report. A full probe checks image IDs returned by the gateway, not video generation. Do not use full probe mode without the user's request to check availability. An uncertain submission or authentication error stops the sweep.

Ordinary image generation obtains `/models` once, or reuses a scoped catalog younger than 5 minutes. `models` always refreshes the catalog. No image is generated by a catalog query. Selection intersects the four preferred IDs (`gpt-image-2.5`, `gpt-image-2.5-sunburst`, `gpt-image-2.5-flare`, `gpt-image-2`) with that list, then appends other image models in API return order, deduplicated. Other model types are excluded; image names and optional `output_modalities` metadata identify automatic image candidates. Unrecognized but advertised image-capable IDs can be selected explicitly. Missing preferred IDs are returned with status `unsupported` and a catalog observation timestamp; any absent explicit choice also stops without submission. Listing an ID gives support information, not proof it works.

Within the eligible order, only historical working models are selected when such records exist. With no working record, the actual requested generation tries eligible candidates in order until the first valid result. Catalog refresh failures and malformed catalogs stop before generation and leave prior support observations intact. Empty valid lists yield no candidates. Historical successes remain in storage but cannot override catalog absence; relisting restores the historical record. Dry runs never query the API or write marks and label their selection provisional. It may switch after definite endpoint/model rejection (404/405); authentication, malformed requests, accepted-but-invalid output, uncertain network/5xx failures, and exhausted shared rate limits stop for a user decision. This preserves the no-duplicate-submission rule.

Successful normal generation updates only its model record. Temporary failures retain earlier successes and failure timestamps. A 404/405 records rejection by this service at that time, not permanent/global model unavailability. Records for generation and editing are separate. Old observations never trigger a paid background sweep.

## Retry boundaries

A job has one shared budget, including a needed catalog query: three retries after the first request, with waits 5s, 10s, 15s across its phases. Waiting totals at most 30s; HTTP request durations and normal queued-video polling are additional. `--timeout` does not mean total job duration. If a server asks for a longer `Retry-After` than the next permitted delay, return control instead of retrying too soon or silently exceeding the budget.

- GET/query/download: network failures and retryable HTTP responses can retry within budget.
- Generation POST: a definite 429 rejection without a task may retry. 500/502/503/504/timeouts may already have created a job and stop unless idempotency is confirmed. A received task ID is retained even if a response is an error.
- Confirmed idempotency: all attempts/resume use the same request key. Sending a header alone is not proof of support.
- 401/403, parameter/policy failures, and ordinary 4xx errors do not blindly retry. Endpoint or model rotation cannot be used to evade these failures.
- Every new explicit user choice to continue permits a new bounded retry round of the original recoverable task. A newly generated job requires the user's decision when the previous job may have been accepted.

## Recovery

Failures after preparing a job return `job_id`, `job_file`, state, any task/request IDs, applicable `next_actions`, and no claimed successful output paths. Exit status is nonzero. Save these details in the conversation without exposing private request/response files.

```text
<cli> resume --job "<absolute-path>/job.json"
<cli> resume --job "<absolute-path>/job.json" --out "<absolute-path>/recovered.jpg"
<cli> resume --job "<absolute-path>/job.json" --max-attempts 4 --timeout 150s
```

`resume` uses a duration-valued timeout (`150s`), whereas generation commands use numeric seconds. It validates the current API/credential scope. It reuses a saved image response/validated bytes, continues the known video task, or safely resubmits a confirmed idempotent request with the same key. A definitively rejected 429 submission can also be continued after the user selects retry. It does not create a fresh task to hide a failed query/download.

If the API returns no task ID and offers neither task lookup nor idempotency, an uncertain submission cannot be automatically recovered: explain that limit and offer provider-side checking, a confirmed new generation, or stopping. Invalid returned bytes can be inspected/recovered from the private response; repeatedly decoding them cannot repair them.

Recovery manifests and files use private permissions. Request bodies (prompts and edit inputs) are retained for unfinished jobs and removed after completion; saved response/validated output bytes remain for recovery until the user no longer needs the job. Signed result URLs are private recovery data, not log/public-report material. Do not delete recovery files the user may still need.

## Video

```text
<cli> video --prompt "A red balloon drifting over a calm lake" --model grok-imagine-video --size 1K --out "<absolute-path>/balloon.mp4"
```

Default: `grok-imagine-video`, `1K`, 8 seconds. `Grok 视频 1.5` maps to `grok-imagine-video-1.5`; `sora/sora 2` to `sora-2`; explicit IDs pass through. Video aliases are selections, not availability guarantees. Video is requested explicitly; no automatic all-model video probe.

Sizes request 1K = 1280×720, 2K = 1920×1080; 4K requests the adapter's 1080p slot with a note. Never report these requested sizes as verified output dimensions. Current container validation recognizes MP4/WebM, but does not decode video frames to measure their dimensions.

`--timeout` is the status wait timeout in seconds (default 600); individual HTTP requests have their own timeout. `--max-attempts` defaults to 4, up to 3 safe retries. Default creation endpoint is `/v1/videos`. For a service explicitly documented to use the legacy adapter, select `CODEX_VIDEO_ENDPOINT=video/generations`; errors never trigger an automatic second creation endpoint.

## Development

Runtime uses only native executables. Source builds/tests need Go 1.24 or later, with no third-party modules:

```text
cd src
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o ../bin/codex-image-gen-darwin-arm64 .
```

The regression tests use dummy credentials and loopback servers; no paid generation. The platform binaries should all be rebuilt after source changes. Changes to installed system `imagegen` are outside this skill's scope.
