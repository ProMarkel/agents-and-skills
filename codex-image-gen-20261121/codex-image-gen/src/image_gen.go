package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAPIURL      = "https://apinebula.com"
	defaultModel       = "gpt-image-2.5"
	defaultGrokModel   = "grok-imagine-image-2.0"
	defaultVideoModel  = "grok-imagine-video"
	defaultSize        = "1K"
	defaultQuality     = "auto"
	defaultOutDir      = "output/codex-image-gen"
	defaultVideoOutDir = "output/codex-image-gen-video"
	defaultVideoSecs   = 8
)

// sizePresets 把用户说的 1K/2K/4K 换成 Images API 真正认的像素。
// 4K 用 3840x2160：官方长边上限 3840，正方形 3840x3840 会超过像素总量上限。
var sizePresets = map[string]string{
	"1K": "1024x1024",
	"2K": "2048x2048",
	"4K": "3840x2160",
}

var retryable = map[int]bool{429: true, 500: true, 502: true, 503: true, 504: true, 524: true}

// Grok 别名仍用表。GPT Image 版本号（2.5、2.6、2.7…）由 resolveGPTImage 按规则生成，不必每次改表。
var modelAliases = []struct {
	alias string
	id    string
}{
	{"grok-imagine-image-quality", "grok-imagine-image-quality"},
	{"grok imagine image quality", "grok-imagine-image-quality"},
	{"grok imagine quality", "grok-imagine-image-quality"},
	{"grok quality", "grok-imagine-image-quality"},
	{"imagine quality", "grok-imagine-image-quality"},
	{"grok 高质量", "grok-imagine-image-quality"},
	{"grok 高质量版", "grok-imagine-image-quality"},
	{"grok 质量版", "grok-imagine-image-quality"},
	{"grok-imagine-image-2.0", "grok-imagine-image-2.0"},
	{"grok imagine image 2.0", "grok-imagine-image-2.0"},
	{"grok imagine 2.0", "grok-imagine-image-2.0"},
	{"grok imagine image 2", "grok-imagine-image-2.0"},
	{"grok 2.0", "grok-imagine-image-2.0"},
	{"grok 2", "grok-imagine-image-2.0"},
	{"imagine 2.0", "grok-imagine-image-2.0"},
	{"grok-imagine", "grok-imagine"},
	{"grok imagine", "grok-imagine"},
	{"grok", defaultGrokModel},
	{"chatgpt-image-latest", "chatgpt-image-latest"},
	{"chatgpt image latest", "chatgpt-image-latest"},
	{"chatgpt latest", "chatgpt-image-latest"},
	{"image latest", "chatgpt-image-latest"},
	{"latest", "chatgpt-image-latest"},
	{"最新", "chatgpt-image-latest"},
	{"最新模型", "chatgpt-image-latest"},
}

type commonArgs struct {
	model       string
	size        string
	quality     string
	n           int
	outDir      string
	force       bool
	dryRun      bool
	maxAttempts int
	timeout     time.Duration
	budget      *retryBudget
	autoOut     bool
}

type apiResponse struct {
	Data []struct {
		B64JSON string `json:"b64_json"`
		URL     string `json:"url"`
	} `json:"data"`
}

type batchJob struct {
	Prompt  string `json:"prompt"`
	Model   string `json:"model,omitempty"`
	Size    string `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
	N       int    `json:"n,omitempty"`
	Out     string `json:"out,omitempty"`
	AutoOut bool   `json:"-"`
}

func endpoint(base, operation string) string {
	return apiRoot(base) + "/images/" + operation
}

// resolveSize 接受 1K/2K/4K、auto，或 WIDTHxHEIGHT。发给上游的永远是后两种。
func resolveSize(size string) (string, error) {
	size = normalizeSize(size)
	if size == "" {
		size = defaultSize
	}
	if preset, ok := sizePresets[strings.ToUpper(size)]; ok {
		return preset, nil
	}
	if strings.EqualFold(size, "auto") {
		return "auto", nil
	}
	parts := strings.Split(strings.ToLower(size), "x")
	if len(parts) != 2 {
		return "", errors.New("size must be 1K, 2K, 4K, auto, or WIDTHxHEIGHT")
	}
	w, e1 := strconv.Atoi(parts[0])
	h, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || w < 1 || h < 1 {
		return "", errors.New("size must be 1K, 2K, 4K, auto, or WIDTHxHEIGHT")
	}
	return fmt.Sprintf("%dx%d", w, h), nil
}

func normalizeWidth(value string) string {
	return strings.Map(func(character rune) rune {
		if character >= '！' && character <= '～' {
			return character - 0xFEE0
		}
		return character
	}, value)
}

func normalizeKey(value string) string {
	value = strings.ToLower(normalizeWidth(value))
	value = strings.NewReplacer("-", "", "_", "").Replace(value)
	return strings.Join(strings.Fields(value), "")
}

func normalizeSize(size string) string {
	size = strings.ToUpper(strings.Join(strings.Fields(normalizeWidth(size)), ""))
	size = strings.ReplaceAll(size, "×", "X")
	switch size {
	case "一K":
		return "1K"
	case "二K", "两K":
		return "2K"
	case "四K":
		return "4K"
	}
	return size
}

func isVideoModel(model string) bool {
	key := normalizeKey(model)
	return strings.Contains(key, "video") || strings.Contains(key, "视频")
}

func isGrokImage(model string) bool {
	k := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(k, "grok-imagine") && !isVideoModel(k)
}

func currentDefaultModel() string {
	if value := strings.TrimSpace(settingValue("CODEX_IMAGE_DEFAULT_MODEL")); value != "" {
		return value
	}
	return defaultModel
}

func currentDefaultVideoModel() string {
	if value := strings.TrimSpace(settingValue("CODEX_VIDEO_DEFAULT_MODEL")); value != "" {
		return value
	}
	return defaultVideoModel
}

func defaultGPTBase() string {
	id := strings.ToLower(strings.TrimSpace(currentDefaultModel()))
	id = strings.TrimSuffix(id, "-flare")
	id = strings.TrimSuffix(id, "-sunburst")
	if strings.HasPrefix(id, "gpt-image-") {
		return id
	}
	return defaultModel
}

func rewriteSpokenDigits(value string) string {
	var built strings.Builder
	for _, character := range value {
		switch character {
		case '零', '〇', '○':
			built.WriteByte('0')
		case '一', '壹':
			built.WriteByte('1')
		case '二', '两', '兩', '贰':
			built.WriteByte('2')
		case '三', '叁':
			built.WriteByte('3')
		case '四', '肆':
			built.WriteByte('4')
		case '五', '伍':
			built.WriteByte('5')
		case '六', '陆', '陸':
			built.WriteByte('6')
		case '七', '柒':
			built.WriteByte('7')
		case '八', '捌':
			built.WriteByte('8')
		case '九', '玖':
			built.WriteByte('9')
		case '点', '點', '．':
			built.WriteByte('.')
		default:
			built.WriteRune(character)
		}
	}
	return built.String()
}

func splitGPTVariant(key string) (rest, variant string) {
	type suffix struct {
		token string
		id    string
	}
	for _, item := range []suffix{
		{normalizeKey("闪焰版"), "-flare"},
		{normalizeKey("日耀版"), "-sunburst"},
		{normalizeKey("flare"), "-flare"},
		{normalizeKey("sunburst"), "-sunburst"},
		{normalizeKey("闪焰"), "-flare"},
		{normalizeKey("日耀"), "-sunburst"},
	} {
		if item.token != "" && strings.HasSuffix(key, item.token) {
			return strings.TrimSuffix(key, item.token), item.id
		}
	}
	return key, ""
}

func stripGPTPrefix(key string) string {
	for _, prefix := range []string{"以麦", "gptimage", "gpt", "image", "img"} {
		if strings.HasPrefix(key, prefix) {
			key = strings.TrimPrefix(key, prefix)
			break
		}
	}
	if strings.HasPrefix(key, "g") && looksLikeVersion(key[1:]) {
		return key[1:]
	}
	return key
}

func gptImageID(version, variant string) string {
	if version == "" {
		return defaultGPTBase() + variant
	}
	major, minor, ok := strings.Cut(version, ".")
	if !ok || minor == "0" {
		return "gpt-image-" + major + variant
	}
	return "gpt-image-" + version + variant
}

func looksLikeVersion(value string) bool {
	if value == "" {
		return false
	}
	major, minor, ok := strings.Cut(value, ".")
	if !ok {
		_, err := strconv.Atoi(value)
		return err == nil
	}
	if major == "" || minor == "" {
		return false
	}
	_, e1 := strconv.Atoi(major)
	_, e2 := strconv.Atoi(minor)
	return e1 == nil && e2 == nil
}

func resolveGPTImage(model string) (string, bool) {
	key := normalizeKey(rewriteSpokenDigits(model))
	rest, variant := splitGPTVariant(key)
	rest = stripGPTPrefix(rest)
	if rest == "" {
		if variant != "" || key == "gpt" || key == "gptimage" {
			return gptImageID("", variant), true
		}
		return "", false
	}
	if !looksLikeVersion(rest) {
		return "", false
	}
	return gptImageID(rest, variant), true
}

func apiRoot(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base
}

// resolveModel 把对话里的叫法收成网关生图模型名。视频模型请用 video 命令；认不出的原样传。
func resolveModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return currentDefaultModel(), nil
	}
	if isVideoModel(model) {
		return "", errors.New("this is a video model; use the video command")
	}
	key := normalizeKey(rewriteSpokenDigits(model))
	if key == "高质量" || key == "高清" || key == "highquality" {
		return "", errors.New("quality alone does not select a model; keep the current model and use --quality high, or explicitly choose Grok 高质量")
	}
	for _, item := range modelAliases {
		if key == normalizeKey(item.alias) {
			return item.id, nil
		}
	}
	if id, ok := resolveGPTImage(model); ok {
		return id, nil
	}
	return model, nil
}

func resolveVideoModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return currentDefaultVideoModel(), nil
	}
	key := normalizeKey(rewriteSpokenDigits(model))
	if key == "高质量" || key == "高清" || key == "highquality" {
		return "", errors.New("quality alone does not select a video model")
	}
	for _, item := range []struct {
		alias string
		id    string
	}{
		{"grok-imagine-video-1.5-preview", "grok-imagine-video-1.5-preview"},
		{"grok-imagine-video-1.5", "grok-imagine-video-1.5"},
		{"grok imagine video 1.5", "grok-imagine-video-1.5"},
		{"grok video 1.5", "grok-imagine-video-1.5"},
		{"grok 视频 1.5", "grok-imagine-video-1.5"},
		{"grok-imagine-video", "grok-imagine-video"},
		{"grok imagine video", "grok-imagine-video"},
		{"grok video", "grok-imagine-video"},
		{"grok 视频", "grok-imagine-video"},
		{"sora-2", "sora-2"},
		{"sora2", "sora-2"},
		{"sora 2", "sora-2"},
		{"sora", "sora-2"},
		{"video", currentDefaultVideoModel()},
		{"视频", currentDefaultVideoModel()},
		{"生成视频", currentDefaultVideoModel()},
	} {
		if key == normalizeKey(item.alias) {
			return item.id, nil
		}
	}
	if isVideoModel(model) {
		return model, nil
	}
	if id, ok := resolveGPTImage(model); ok {
		return "", fmt.Errorf("%s is an image model; use generate, not video", id)
	}
	return model, nil
}

func resolveVideoSize(size string) (pixels, band, note string, err error) {
	size = normalizeSize(size)
	if size == "" {
		size = defaultSize
	}
	switch strings.ToUpper(size) {
	case "1K", "AUTO", "720P", "720":
		return "1280x720", "1K", "", nil
	case "2K", "1080P", "1080":
		return "1920x1080", "2K", "", nil
	case "4K", "2160P", "2160":
		return "1920x1080", "4K", "most video models top out at 1080p; requested the highest common slot", nil
	case "480P", "480":
		return "848x480", "480p", "", nil
	}
	if strings.Contains(strings.ToLower(size), "x") {
		parts := strings.Split(strings.ToLower(size), "x")
		if len(parts) == 2 {
			w, e1 := strconv.Atoi(parts[0])
			h, e2 := strconv.Atoi(parts[1])
			if e1 == nil && e2 == nil && w > 0 && h > 0 {
				return fmt.Sprintf("%dx%d", w, h), requestedBand(size), "", nil
			}
		}
	}
	return "", "", "", errors.New("video size must be 1K, 2K, 4K, 480p/720p/1080p, or WIDTHxHEIGHT")
}

func grokResolution(size string) (res, pixel, note string) {
	size = normalizeSize(size)
	if size == "" {
		size = defaultSize
	}
	switch strings.ToUpper(size) {
	case "1K", "AUTO":
		return "1k", "1024x1024", ""
	case "2K":
		return "2k", "2048x2048", ""
	case "4K":
		// Do not send resolution=4k: 6rr ignores it and returns 1024x1024.
		// size=3840x2160 is the highest slot (typically 2816x1584).
		return "2k", "3840x2160", "Grok Imagine has no 4K; requested the highest available slot"
	}
	resolved, err := resolveSize(size)
	if err != nil {
		return "1k", "1024x1024", ""
	}
	parts := strings.Split(strings.ToLower(resolved), "x")
	if len(parts) == 2 {
		w, e1 := strconv.Atoi(parts[0])
		h, e2 := strconv.Atoi(parts[1])
		if e1 == nil && e2 == nil && (w >= 3000 || h >= 3000) {
			return "2k", resolved, ""
		}
		if e1 == nil && e2 == nil && (w >= 1536 || h >= 1536) {
			return "2k", resolved, ""
		}
	}
	return "1k", "1024x1024", ""
}

type imageSpec struct {
	model   string
	payload map[string]any
	fields  map[string]string
	extra   map[string]any
}

func prepareImageRequest(prompt string, args commonArgs) (imageSpec, error) {
	model, err := resolveModel(args.model)
	if err != nil {
		return imageSpec{}, err
	}
	spec := imageSpec{
		model:   model,
		payload: map[string]any{"model": model, "prompt": prompt, "n": args.n},
		fields:  map[string]string{"model": model, "prompt": prompt, "n": strconv.Itoa(args.n)},
		extra:   map[string]any{"model": model, "size": args.size, "requested_quality": args.quality, "quality_sent": nil},
	}
	if isGrokImage(model) {
		res, pixel, note := grokResolution(args.size)
		// new-api drops unknown fields like resolution unless pass_through is on.
		// 6rr honors both resolution=2k and OpenAI size=2048x2048; send both.
		spec.payload["resolution"] = res
		spec.payload["size"] = pixel
		spec.fields["resolution"] = res
		spec.fields["size"] = pixel
		spec.extra["family"] = "grok"
		spec.extra["resolution"] = res
		spec.extra["resolved_size"] = pixel
		if note != "" {
			spec.extra["note"] = note
		}
		if model == "grok-imagine-image-2.0" {
			q := args.quality
			if q == "high" {
				q = "auto"
			}
			spec.payload["quality"] = q
			spec.fields["quality"] = q
			spec.extra["quality"] = q
			spec.extra["quality_sent"] = q
		}
		return spec, nil
	}
	resolved, err := resolveSize(args.size)
	if err != nil {
		return imageSpec{}, err
	}
	if model == "gpt-image-2" && resolved != "auto" {
		var w, h int
		_, _ = fmt.Sscanf(resolved, "%dx%d", &w, &h)
		long, short := w, h
		if h > w {
			long, short = h, w
		}
		if w%16 != 0 || h%16 != 0 || long > 3840 || int64(long) > 3*int64(short) || int64(w)*int64(h) < 655360 || int64(w)*int64(h) > 8294400 {
			return imageSpec{}, errors.New("gpt-image-2 size requires multiples of 16, maximum edge 3840, aspect ratio at most 3:1, and 655360-8294400 total pixels")
		}
	}
	spec.payload["size"] = resolved
	spec.payload["quality"] = args.quality
	spec.fields["size"] = resolved
	spec.fields["quality"] = args.quality
	spec.extra["resolved_size"] = resolved
	spec.extra["quality"] = args.quality
	spec.extra["quality_sent"] = args.quality
	if strings.HasPrefix(strings.ToLower(model), "gpt-image") {
		spec.extra["family"] = "gpt"
	} else {
		spec.extra["family"] = "other"
	}
	return spec, nil
}

func resultJSON(spec imageSpec, outputs any) map[string]any {
	out := map[string]any{}
	for k, v := range spec.extra {
		out[k] = v
	}
	out["outputs"] = outputs
	return out
}

func requestedBand(size string) string {
	size = normalizeSize(size)
	u := strings.ToUpper(size)
	if u == "1K" || u == "2K" || u == "4K" {
		return u
	}
	if strings.EqualFold(size, "auto") || size == "" {
		return "1K"
	}
	resolved, err := resolveSize(size)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.ToLower(resolved), "x")
	if len(parts) != 2 {
		return ""
	}
	w, e1 := strconv.Atoi(parts[0])
	h, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		return ""
	}
	return qualityBand(w, h)
}

func qualityBand(w, h int) string {
	long := w
	if h > w {
		long = h
	}
	switch {
	case long >= 3000:
		return "4K"
	case long >= 1800:
		return "2K"
	default:
		return "1K"
	}
}

func bandRank(band string) int {
	switch band {
	case "4K":
		return 3
	case "2K":
		return 2
	default:
		return 1
	}
}

func inspectImage(data []byte) (int, int, string) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, ""
	}
	return cfg.Width, cfg.Height, format
}

func attachActual(spec *imageSpec, images [][]byte, asked string) {
	if spec == nil {
		return
	}
	assets := []map[string]any{}
	warnings := []string{}
	expected, e := resolveSize(asked)
	for i, data := range images {
		w, h, f := inspectImage(data)
		a := map[string]any{"index": i + 1, "actual_size": fmt.Sprintf("%dx%d", w, h), "width": w, "height": h, "actual_format": f, "validated": w > 0 && h > 0}
		if e == nil && expected != "auto" && a["actual_size"] != expected {
			warning := fmt.Sprintf("Image %d: requested %s, actual %dx%d; returned dimensions do not match the request.", i+1, expected, w, h)
			a["warning"] = warning
			warnings = append(warnings, warning)
		}
		assets = append(assets, a)
	}
	spec.extra["assets"] = assets
	spec.extra["requested_size"] = expected
	// Backwards-compatible scalar only for a single image; no misleading 'actual_quality'.
	if len(assets) == 1 {
		spec.extra["actual_size"] = assets[0]["actual_size"]
		spec.extra["actual_format"] = assets[0]["actual_format"]
	}
	if len(warnings) > 0 {
		spec.extra["warnings"] = warnings
		spec.extra["warning"] = strings.Join(warnings, " ")
	}
}

func stringifyErrorValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case map[string]any:
		if msg, ok := t["message"]; ok {
			return stringifyErrorValue(msg)
		}
	}
	return ""
}

func extractJSONError(body []byte) string {
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		s := strings.TrimSpace(redact(string(body)))
		if len(s) > 240 {
			s = s[:240]
		}
		return s
	}
	if msg := stringifyErrorValue(top["error"]); msg != "" {
		return msg
	}
	return stringifyErrorValue(top["message"])
}

func formatAPIError(status int, body []byte) string {
	msg := redact(extractJSONError(body))
	if len(msg) > 2000 {
		msg = msg[:2000] + "…"
	}
	if msg == "" {
		return fmt.Sprintf("Upstream API returned HTTP %d", status)
	}
	return fmt.Sprintf("Upstream API returned HTTP %d: %s", status, msg)
}

func validateCommon(args commonArgs) error {
	if _, err := resolveModel(args.model); err != nil {
		return err
	}
	if args.n < 1 || args.n > 10 {
		return errors.New("n must be from 1 to 10")
	}
	if args.maxAttempts < 1 || args.maxAttempts > 4 {
		return errors.New("--max-attempts must be 1-4 (up to 3 retries)")
	}
	if args.timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	if args.quality != "low" && args.quality != "medium" && args.quality != "high" && args.quality != "auto" {
		return errors.New("quality must be low, medium, high, or auto")
	}
	if _, err := resolveSize(args.size); err != nil {
		return err
	}
	return nil
}

func promptValue(prompt, promptFile string) (string, error) {
	if (prompt == "") == (promptFile == "") {
		return "", errors.New("provide exactly one of --prompt or --prompt-file")
	}
	if promptFile != "" {
		data, err := os.ReadFile(promptFile)
		if err != nil {
			return "", fmt.Errorf("could not read prompt file: %w", err)
		}
		prompt = string(data)
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", errors.New("prompt must not be empty")
	}
	return prompt, nil
}

func outputPaths(out, outDir, prompt string, n int) []string {
	if out == "" {
		sum := sha256.Sum256([]byte(prompt))
		out = "image-" + hex.EncodeToString(sum[:6]) + "-" + randomID() + ".png"
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(outDir, out)
	}
	ext := filepath.Ext(out)
	if ext == "" {
		ext = ".png"
		out += ext
	}
	if n == 1 {
		return []string{out}
	}
	base := strings.TrimSuffix(out, ext)
	paths := make([]string, n)
	for i := range paths {
		paths[i] = fmt.Sprintf("%s-%d%s", base, i+1, ext)
	}
	return paths
}

func checkOutputs(paths []string, force bool) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err == nil {
			if info.IsDir() {
				return fmt.Errorf("output is a directory: %s", path)
			}
			if !force {
				return fmt.Errorf("refusing to overwrite existing file: %s", path)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func apiKey() (string, error) {
	key, _ := setting("CODEX_API_KEY")
	if key == "" {
		return "", errors.New("CODEX_API_KEY is not visible in this process or the macOS login session; configure it locally, never paste it into chat")
	}
	rememberSecret(key)
	return key, nil
}

func doRequest(client *http.Client, makeRequest func() (*http.Request, error), maxAttempts int) ([]byte, error) {
	return reliableRequest(client, makeRequest, maxAttempts)
}

func decodeResponse(raw []byte, client *http.Client) ([][]byte, error) {
	return decodeImages(raw, client, 4)
}

func saveImages(images [][]byte, paths []string) ([]string, error) {
	return saveImagesSafely(images, paths, false)
}

func generate(prompt, out string, args commonArgs) (map[string]any, error) {
	return runImageTask(prompt, nil, "", out, args)
}

func edit(prompt string, imagePaths []string, mask, out string, args commonArgs) (map[string]any, error) {
	return runImageTask(prompt, imagePaths, mask, out, args)
}

func printJSON(value any) {
	encoder := json.NewEncoder(redactedWriter{os.Stdout})
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func parseCommon(fs *flag.FlagSet, argv []string, args *commonArgs) error {
	var timeout float64
	retries := -1
	fs.SetOutput(redactedWriter{os.Stderr})
	fs.StringVar(&args.model, "model", "", "image model; omitted/auto selects verified models in priority order")
	fs.StringVar(&args.size, "size", defaultSize, "1K, 2K, 4K, auto, or WIDTHxHEIGHT")
	fs.StringVar(&args.quality, "quality", defaultQuality, "low, medium, high, or auto")
	fs.IntVar(&args.n, "n", 1, "number of variants (1-10)")
	fs.StringVar(&args.outDir, "out-dir", defaultOutDir, "output directory; relative --out is resolved here")
	fs.BoolVar(&args.force, "force", false, "explicitly overwrite existing outputs")
	fs.BoolVar(&args.dryRun, "dry-run", false, "validate without network access or credentials")
	fs.IntVar(&args.maxAttempts, "max-attempts", 4, "total attempts, including the first (1-4)")
	fs.IntVar(&retries, "max-retries", -1, "retries after first attempt (0-3); alternative to --max-attempts")
	fs.Float64Var(&timeout, "timeout", 150, "request timeout in seconds")
	if e := fs.Parse(argv); e != nil {
		return e
	}
	if retries != -1 {
		supplied := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "max-attempts" {
				supplied = true
			}
		})
		if supplied || retries < 0 || retries > 3 {
			return errors.New("choose either --max-retries 0-3 or --max-attempts 1-4")
		}
		args.maxAttempts = retries + 1
	}
	args.timeout = time.Duration(timeout * float64(time.Second))
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func runGenerate(argv []string) error {
	fs := newFlagSet("generate")
	var prompt, promptFile, out string
	fs.StringVar(&prompt, "prompt", "", "prompt text")
	fs.StringVar(&promptFile, "prompt-file", "", "UTF-8 prompt file")
	fs.StringVar(&out, "out", "", "output path")
	var args commonArgs
	if err := parseCommon(fs, argv, &args); err != nil {
		return err
	}
	finalPrompt, err := promptValue(prompt, promptFile)
	if err != nil {
		return err
	}
	result, err := generate(finalPrompt, out, args)
	if result != nil {
		printJSON(result)
	}
	return err
}

func runEdit(argv []string) error {
	fs := newFlagSet("edit")
	var images stringList
	var prompt, promptFile, mask, out string
	fs.Var(&images, "image", "input image; repeat for multiple images")
	fs.StringVar(&mask, "mask", "", "optional PNG mask")
	fs.StringVar(&prompt, "prompt", "", "prompt text")
	fs.StringVar(&promptFile, "prompt-file", "", "UTF-8 prompt file")
	fs.StringVar(&out, "out", "", "output path")
	var args commonArgs
	if err := parseCommon(fs, argv, &args); err != nil {
		return err
	}
	if len(images) == 0 {
		return errors.New("provide at least one --image")
	}
	finalPrompt, err := promptValue(prompt, promptFile)
	if err != nil {
		return err
	}
	result, err := edit(finalPrompt, images, mask, out, args)
	if result != nil {
		printJSON(result)
	}
	return err
}

func runBatch(argv []string) error {
	fs := newFlagSet("generate-batch")
	var input string
	var concurrency int
	var failFast bool
	fs.StringVar(&input, "input", "", "JSONL input path")
	fs.IntVar(&concurrency, "concurrency", 2, "parallel jobs")
	fs.BoolVar(&failFast, "fail-fast", false, "stop scheduling after a failure")
	var args commonArgs
	if err := parseCommon(fs, argv, &args); err != nil {
		return err
	}
	if input == "" || concurrency < 1 {
		return errors.New("--input is required and --concurrency must be at least 1")
	}
	file, err := os.Open(input)
	if err != nil {
		return fmt.Errorf("could not read batch input: %w", err)
	}
	defer file.Close()
	var jobs []batchJob
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var job batchJob
		if err := json.Unmarshal(scanner.Bytes(), &job); err != nil || strings.TrimSpace(job.Prompt) == "" {
			return errors.New("each JSONL line must contain a non-empty prompt")
		}
		jobs = append(jobs, job)
	}
	if err := scanner.Err(); err != nil || len(jobs) == 0 {
		return errors.New("batch input contains no valid jobs")
	}
	// Resolve and validate every destination before any paid job can be submitted.
	seen := map[string]bool{}
	for i := range jobs {
		jobs[i].AutoOut = jobs[i].Out == ""
		ja := args
		if jobs[i].Model != "" {
			ja.model = jobs[i].Model
		}
		if jobs[i].Size != "" {
			ja.size = jobs[i].Size
		}
		if jobs[i].Quality != "" {
			ja.quality = jobs[i].Quality
		}
		if jobs[i].N != 0 {
			ja.n = jobs[i].N
		}
		if e := validateCommon(ja); e != nil {
			return fmt.Errorf("batch job %d: %w", i+1, e)
		}
		base := outputPaths(jobs[i].Out, args.outDir, jobs[i].Prompt, 1)[0]
		base, e := filepath.Abs(base)
		if e != nil {
			return e
		}
		jobs[i].Out = base
		paths := outputPaths(base, "", jobs[i].Prompt, ja.n)
		for _, p := range paths {
			canonical := p
			if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
				canonical = strings.ToLower(p)
			}
			if seen[canonical] {
				return fmt.Errorf("batch output collision before submission: %s", p)
			}
			seen[canonical] = true
		}
		if !args.dryRun {
			if e := checkOutputs(paths, args.force); e != nil {
				return e
			}
		}
	}
	if !args.dryRun {
		if _, e := configuredRoot(); e != nil {
			return e
		}
		if _, e := apiKey(); e != nil {
			return e
		}
	}
	type result struct {
		Index int            `json:"index"`
		OK    bool           `json:"ok"`
		Data  map[string]any `json:"data,omitempty"`
		Error string         `json:"error,omitempty"`
	}
	results := make([]result, len(jobs))
	queue := make(chan int)
	var wg sync.WaitGroup
	var failed bool
	var mu sync.Mutex
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range queue {
				job := jobs[index]
				jobArgs := args
				jobArgs.autoOut = job.AutoOut
				if job.Model != "" {
					jobArgs.model = job.Model
				}
				if job.Size != "" {
					jobArgs.size = job.Size
				}
				if job.Quality != "" {
					jobArgs.quality = job.Quality
				}
				if job.N != 0 {
					jobArgs.n = job.N
				}
				data, err := generate(strings.TrimSpace(job.Prompt), job.Out, jobArgs)
				results[index] = result{Index: index + 1, OK: err == nil, Data: data}
				if err != nil {
					results[index].Error = err.Error()
					mu.Lock()
					failed = true
					mu.Unlock()
				}
			}
		}()
	}
	for index := range jobs {
		mu.Lock()
		stop := failFast && failed
		mu.Unlock()
		if stop {
			break
		}
		queue <- index
	}
	close(queue)
	wg.Wait()
	succeeded, failures := 0, 0
	for _, item := range results {
		if item.Index == 0 {
			continue
		}
		if item.OK {
			succeeded++
		} else {
			failures++
		}
	}
	printJSON(map[string]any{"jobs": results, "succeeded": succeeded, "failed": failures})
	if failures > 0 {
		return errors.New("one or more batch jobs failed")
	}
	return nil
}

func isProbablyImageModel(id string) bool {
	key := strings.ToLower(id)
	if isProbablyVideoModel(id) {
		return false
	}
	return strings.Contains(key, "image") || strings.Contains(key, "dall") || strings.Contains(key, "flux") || strings.Contains(key, "imagen") || strings.Contains(key, "grok-imagine")
}

func isProbablyVideoModel(id string) bool {
	key := strings.ToLower(id)
	return strings.Contains(key, "video") || strings.Contains(key, "sora") || strings.Contains(key, "kling") || strings.Contains(key, "runway") || strings.Contains(key, "luma") || strings.Contains(key, "jimeng") || strings.Contains(key, "即梦")
}

func jsonString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func asObject(value any) map[string]any {
	object, _ := value.(map[string]any)
	return object
}

func videoTaskID(payload map[string]any) string {
	if id := jsonString(payload, "id", "task_id", "request_id"); id != "" {
		return id
	}
	if nested := asObject(payload["data"]); nested != nil {
		return jsonString(nested, "id", "task_id", "request_id")
	}
	return ""
}

func videoStatus(payload map[string]any) string {
	status := jsonString(payload, "status", "task_status")
	if status == "" {
		if nested := asObject(payload["data"]); nested != nil {
			status = jsonString(nested, "status", "task_status")
		}
	}
	return strings.ToLower(status)
}

func videoResultURL(payload map[string]any) string {
	if url := jsonString(payload, "url", "result_url", "video_url"); url != "" {
		return url
	}
	if nested := asObject(payload["data"]); nested != nil {
		return jsonString(nested, "url", "result_url", "video_url")
	}
	return ""
}

func videoErrorMessage(payload map[string]any) string {
	if msg := stringifyErrorValue(payload["error"]); msg != "" {
		return msg
	}
	if nested := asObject(payload["data"]); nested != nil {
		if msg := stringifyErrorValue(nested["error"]); msg != "" {
			return msg
		}
		if msg := jsonString(nested, "fail_reason", "message"); msg != "" {
			return msg
		}
	}
	return jsonString(payload, "fail_reason", "message")
}

func outputVideoPath(out, outDir, prompt string) string {
	if out == "" {
		out = "video-" + digest(prompt)[:12] + "-" + randomID() + ".mp4"
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(outDir, out)
	}
	if filepath.Ext(out) == "" {
		out += ".mp4"
	}
	return out
}

func postJSON(client *http.Client, endpoint, key string, payload map[string]any, attempts int) (map[string]any, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	raw, err := doRequest(client, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
		}
		return req, err
	}, attempts)
	if err != nil {
		return nil, raw, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, errors.New("API returned invalid JSON")
	}
	return parsed, raw, nil
}

func getJSON(client *http.Client, endpoint, key string, attempts int) (map[string]any, error) {
	raw, err := doRequest(client, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Accept", "application/json")
		}
		return req, err
	}, attempts)
	if err != nil {
		return nil, err
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, errors.New("API returned invalid JSON")
	}
	return parsed, nil
}

func runVideo(argv []string) error {
	fs := newFlagSet("video")
	var prompt, promptFile, out, model, size string
	var seconds int
	var force, dryRun bool
	var timeoutSec float64
	var attempts int
	fs.StringVar(&prompt, "prompt", "", "prompt text")
	fs.StringVar(&promptFile, "prompt-file", "", "UTF-8 prompt file")
	fs.StringVar(&out, "out", "", "output path")
	fs.StringVar(&model, "model", "", "video model; default grok-imagine-video")
	fs.StringVar(&size, "size", defaultSize, "1K, 2K, 4K, or WIDTHxHEIGHT")
	fs.IntVar(&seconds, "seconds", defaultVideoSecs, "clip length in seconds")
	fs.BoolVar(&force, "force", false, "overwrite existing outputs")
	fs.BoolVar(&dryRun, "dry-run", false, "validate without network access")
	fs.Float64Var(&timeoutSec, "timeout", 600, "wait timeout in seconds")
	fs.IntVar(&attempts, "max-attempts", 4, "maximum total attempts (1-4); up to 3 safe retries")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	finalPrompt, err := promptValue(prompt, promptFile)
	if err != nil {
		return err
	}
	result, err := generateVideo(finalPrompt, out, model, size, seconds, force, dryRun, time.Duration(timeoutSec*float64(time.Second)), attempts)
	if result != nil {
		printJSON(result)
	}
	return err
}

func runModels(argv []string) error { return listModels(argv) }

func usage() {
	fmt.Fprintln(os.Stderr, "Codex ImageGen: generate | edit | generate-batch | video | models | doctor | resume")
	fmt.Fprintln(os.Stderr, "Image default: available cached model, priority 2.5 → 2.5-sunburst → 2.5-flare → 2; 1K, quality auto")
	fmt.Fprintln(os.Stderr, "Only advertised image IDs are eligible; other image models follow in API list order. No working record: try until first success.")
	fmt.Fprintln(os.Stderr, "Retry defaults: 3 retries, waits 5s/10s/15s (30s total waiting, excluding requests).")
	fmt.Fprintln(os.Stderr, "Missing URL: configure CODEX_API_URL or explicitly pass --use-default-url (https://apinebula.com).")
	fmt.Fprintln(os.Stderr, "models lists advertised models; --check-availability explicitly performs generation tests.")
}

func main() {
	// This option records the user's explicit choice for this invocation only.
	if len(os.Args) > 1 && os.Args[1] == "--use-default-url" {
		useDefaultURL = true
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	if key, _ := setting("CODEX_API_KEY"); key != "" {
		rememberSecret(key)
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "doctor":
		err = runDoctor(os.Args[2:])
	case "resume":
		err = runResume(os.Args[2:])
	case "generate":
		err = runGenerate(os.Args[2:])
	case "generate-batch":
		err = runBatch(os.Args[2:])
	case "edit":
		err = runEdit(os.Args[2:])
	case "video":
		err = runVideo(os.Args[2:])
	case "models":
		err = runModels(os.Args[2:])
	case "--help", "-h", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command: %s", os.Args[1])
	}
	if err != nil {
		var ae *actionError
		if errors.As(err, &ae) && ae.Kind == "configuration_required" && os.Args[1] != "doctor" {
			printJSON(map[string]any{"ok": false, "error": describeError(err), "choices": []map[string]string{{"action": "configure", "variable": "CODEX_API_URL"}, {"action": "use_default_url", "url": defaultAPIURL, "flag": "--use-default-url"}}})
		}
		fmt.Fprintln(redactedWriter{os.Stderr}, "error:", err)
		os.Exit(2)
	}
}
