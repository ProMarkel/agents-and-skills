package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const dummyKey = "local-tests-only-fake-key-20261121"

func testEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CODEX_IMAGE_LOGIN_SESSION", "0")
	t.Setenv("CODEX_API_KEY", dummyKey)
	t.Setenv("CODEX_API_URL", "")
	t.Setenv("CODEX_IMAGE_DEFAULT_MODEL", "")
	t.Setenv("CODEX_VIDEO_DEFAULT_MODEL", "")
	t.Setenv("CODEX_API_IDEMPOTENCY_SUPPORTED", "")
	t.Setenv("CODEX_VIDEO_ENDPOINT", "")
	t.Setenv("CODEX_IMAGE_STATE_DIR", filepath.Join(dir, "state"))
	old := useDefaultURL
	useDefaultURL = false
	t.Cleanup(func() { useDefaultURL = old })
	return dir
}
func testPNG(w, h int) []byte {
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	im.Set(0, 0, color.RGBA{10, 100, 200, 255})
	_ = png.Encode(&b, im)
	return b.Bytes()
}
func testPayload(w http.ResponseWriter, images ...[]byte) {
	items := []map[string]string{}
	for _, b := range images {
		items = append(items, map[string]string{"b64_json": base64.StdEncoding.EncodeToString(b)})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
}
func testArgs(dir string) commonArgs {
	a := generationDefaults()
	a.outDir = dir
	a.timeout = time.Second
	return a
}
func skipRetrySleep(t *testing.T) *[]time.Duration {
	t.Helper()
	old := retrySleep
	var waits []time.Duration
	retrySleep = func(d time.Duration) { waits = append(waits, d) }
	t.Cleanup(func() { retrySleep = old })
	return &waits
}
func mockError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg}})
}

func TestConfigurationRequiresChoiceAndDoesNotSilentlyDefault(t *testing.T) {
	testEnv(t)
	if root, e := configuredRoot(); e == nil || root != "" {
		t.Fatalf("silent default: %q %v", root, e)
	}
	useDefaultURL = true
	root, e := configuredRoot()
	if e != nil || root != "https://apinebula.com/v1" {
		t.Fatalf("explicit default failed: %q %v", root, e)
	}
}
func TestConfigurationSourceAndMissingKey(t *testing.T) {
	testEnv(t)
	if v, source := setting("CODEX_API_KEY"); v != dummyKey || source != "process_environment" {
		t.Fatal("wrong source")
	}
	t.Setenv("CODEX_API_KEY", "")
	if _, e := apiKey(); e == nil {
		t.Fatal("missing key accepted")
	}
}
func TestUnsafeURLsRejected(t *testing.T) {
	testEnv(t)
	for _, s := range []string{"ftp://example.com", "file:///tmp/a", "https://user:password@example.com", "https://example.com/#x"} {
		if _, e := validateURL(s); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	for _, s := range []string{"https://example.com/asset?sig=allowed", "http://127.0.0.1:123/a", "http://[::1]:123/a"} {
		if _, e := validateURL(s); e != nil {
			t.Fatalf("rejected allowed %s: %v", s, e)
		}
	}
}
func TestExplicitConfiguredHTTPAddressIsPreserved(t *testing.T) {
	testEnv(t)
	t.Setenv("CODEX_API_URL", "http://configured.example.invalid:8080")
	root, e := configuredRoot()
	if e != nil || root != "http://configured.example.invalid:8080/v1" {
		t.Fatal(root, e)
	}
}
func TestHTTPSResultDownloadCannotDowngradeToHTTP(t *testing.T) {
	testEnv(t)
	client := newClient(time.Second, "https://api.example.invalid/v1", nil, "download")
	if _, e := assetBytes(client, "http://asset.example.invalid/file.png", 1); e == nil {
		t.Fatal("HTTPS download downgraded")
	}
}
func TestDryRunHasNoImplicitProviderOrAvailabilityWrite(t *testing.T) {
	dir := testEnv(t)
	a := testArgs(dir)
	a.dryRun = true
	r, e := generate("dry run", "", a)
	if e != nil || r["endpoint"] != "" || r["configuration_required"] != true {
		t.Fatalf("%v %v", r, e)
	}
	if _, e = os.Stat(stateDir()); !os.IsNotExist(e) {
		t.Fatal("dry run wrote state")
	}
}
func TestNestedRelativeOutputResolvedUnderDirectory(t *testing.T) {
	testEnv(t)
	got := outputPaths("nested/image.png", "/tmp/output-root", "x", 1)
	if got[0] != "/tmp/output-root/nested/image.png" {
		t.Fatal(got)
	}
}
func TestAutomaticPriorityStopsAtFirstSuccessAndReusesCache(t *testing.T) {
	dir := testEnv(t)
	var calls []string
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		m := b["model"].(string)
		calls = append(calls, m)
		if m != "gpt-image-2" {
			mockError(w, 404, "Upstream request failed (status 404)")
			return
		}
		testPayload(w, testPNG(16, 16))
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	a := testArgs(dir)
	result, e := generate("same", "", a)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(calls, imagePriority) {
		t.Fatalf("wrong priority %v", calls)
	}
	if result["model"] != "gpt-image-2" {
		t.Fatal(result)
	}
	calls = nil
	if _, e = generate("same", "", a); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(calls, []string{"gpt-image-2"}) {
		t.Fatalf("re-probed all models: %v", calls)
	}
}
func TestKnownModelsHonorPriorityAndExplicitChoice(t *testing.T) {
	dir := testEnv(t)
	var calls []string
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		calls = append(calls, b["model"].(string))
		testPayload(w, testPNG(16, 16))
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	root, _ := configuredRoot()
	for _, m := range imagePriority {
		if e := recordModel(root, dummyKey, "generate", m, nil); e != nil {
			t.Fatal(e)
		}
	}
	a := testArgs(dir)
	if _, e := generate("auto", "", a); e != nil {
		t.Fatal(e)
	}
	a.model = "2.5-flare"
	if _, e := generate("explicit", "", a); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(calls, []string{"gpt-image-2.5", "gpt-image-2.5-flare"}) {
		t.Fatal(calls)
	}
}
func TestAllModelsRejectedStopsAfterFour(t *testing.T) {
	dir := testEnv(t)
	calls := 0
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; mockError(w, 404, "unknown model") }))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	_, e := generate("all fail", "", testArgs(dir))
	if e == nil || calls != 4 {
		t.Fatalf("calls=%d %v", calls, e)
	}
}
func TestAvailabilityScopesSeparateAccountAndOperation(t *testing.T) {
	testEnv(t)
	root := "https://example.com/v1"
	_ = recordModel(root, dummyKey, "generate", "gpt-image-2", nil)
	if loadModelRecord(accountScope(root, dummyKey), "edit", "gpt-image-2").Status != "unverified" {
		t.Fatal("cross-operation cache")
	}
	if loadModelRecord(accountScope(root, "other-key"), "generate", "gpt-image-2").Status != "unverified" {
		t.Fatal("cross-account cache")
	}
	_ = recordModel(root, dummyKey, "generate", "gpt-image-2", &actionError{Status: 429, Message: "limited"})
	if loadModelRecord(accountScope(root, dummyKey), "generate", "gpt-image-2").Status != "available" {
		t.Fatal("temporary failure erased successful history")
	}
}
func TestModelListingDoesNotGenerateOrMarkAvailable(t *testing.T) {
	testEnv(t)
	gets, posts := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			return
		}
		gets++
		fmt.Fprint(w, `{"data":[{"id":"gpt-image-2"}]}`)
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	if e := listModels(nil); e != nil {
		t.Fatal(e)
	}
	if gets != 1 || posts != 0 {
		t.Fatal(gets, posts)
	}
	root, _ := configuredRoot()
	if loadModelRecord(accountScope(root, dummyKey), "generate", "gpt-image-2").Status != "unverified" {
		t.Fatal("list marked model working")
	}
}
func TestUncertainSubmissionNotRetriedOrSwitched(t *testing.T) {
	dir := testEnv(t)
	waits := skipRetrySleep(t)
	calls := 0
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("x-request-id", "mock-request-500")
		mockError(w, 500, "temporarily failed")
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	result, e := generate("uncertain", "", testArgs(dir))
	var ae *actionError
	if !errors.As(e, &ae) || !ae.SubmissionUnknown || calls != 1 || len(*waits) != 0 {
		t.Fatalf("calls %d waits %v error %v", calls, *waits, e)
	}
	if result["state"] != "submission_unknown" || result["request_id"] != "mock-request-500" {
		t.Fatal(result)
	}
}
func TestDeduplicatedSubmitRetriesWithSameId(t *testing.T) {
	dir := testEnv(t)
	t.Setenv("CODEX_API_IDEMPOTENCY_SUPPORTED", "1")
	waits := skipRetrySleep(t)
	var ids []string
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("Idempotency-Key"))
		if len(ids) == 1 {
			mockError(w, 500, "temporary")
			return
		}
		testPayload(w, testPNG(16, 16))
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	a := testArgs(dir)
	a.model = "2"
	if _, e := generate("dedup", "", a); e != nil {
		t.Fatal(e)
	}
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] || !reflect.DeepEqual(*waits, []time.Duration{5 * time.Second}) {
		t.Fatal(ids, *waits)
	}
}
func TestThreeRetriesHaveFiveTenFifteenSecondWaits(t *testing.T) {
	testEnv(t)
	waits := skipRetrySleep(t)
	calls := 0
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; mockError(w, 503, "temporary") }))
	defer srv.Close()
	client := newClient(time.Second, srv.URL, &retryBudget{limit: 3}, "retry-test")
	_, e := doRequest(client, func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }, 4)
	if e == nil || calls != 4 || !reflect.DeepEqual(*waits, []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second}) {
		t.Fatal(calls, *waits, e)
	}
}
func TestRetryBudgetSharedAcrossRequests(t *testing.T) {
	testEnv(t)
	waits := skipRetrySleep(t)
	calls := 0
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; mockError(w, 503, "temporary") }))
	defer srv.Close()
	client := newClient(time.Second, srv.URL, &retryBudget{limit: 3}, "shared")
	for i := 0; i < 2; i++ {
		_, _ = doRequest(client, func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }, 4)
	}
	if calls != 5 || len(*waits) != 3 {
		t.Fatal(calls, *waits)
	}
}
func TestLongRetryAfterStopsInsteadOfExceedingBudget(t *testing.T) {
	testEnv(t)
	waits := skipRetrySleep(t)
	calls := 0
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60")
		mockError(w, 429, "rate limit")
	}))
	defer srv.Close()
	_, e := doRequest(newClient(time.Second, srv.URL, nil, "after"), func() (*http.Request, error) { return http.NewRequest("GET", srv.URL, nil) }, 4)
	if e == nil || calls != 1 || len(*waits) != 0 {
		t.Fatal(calls, *waits, e)
	}
}
func TestErrorRedactsCredentialAndPreservesUsefulDetails(t *testing.T) {
	testEnv(t)
	rememberSecret(dummyKey)
	raw := []byte(fmt.Sprintf(`{"error":{"message":"Invalid credential: %s; request=useful-id"}}`, dummyKey))
	s := formatAPIError(401, raw)
	if strings.Contains(s, dummyKey) || !strings.Contains(s, "useful-id") || !strings.Contains(s, "401") {
		t.Fatal(s)
	}
	s = redact("Authorization: Bearer arbitrary-echoed-token https://example.com/a?token=secret&x=visible")
	if strings.Contains(s, "arbitrary-echoed-token") || strings.Contains(s, "token=secret") || !strings.Contains(s, "x=visible") {
		t.Fatal(s)
	}
}
func TestAuthenticationFailureDoesNotRetryModelsOrVideoEndpoint(t *testing.T) {
	dir := testEnv(t)
	calls := 0
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; mockError(w, 401, "bad auth "+dummyKey) }))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	_, e := generate("auth", "", testArgs(dir))
	if e == nil || calls != 1 {
		t.Fatal(calls, e)
	}
	calls = 0
	_, e = generateVideo("auth", filepath.Join(dir, "v.mp4"), "", "1K", 8, false, false, time.Second, 4)
	if e == nil || calls != 1 {
		t.Fatal(calls, e)
	}
}
func TestInvalidAndTruncatedImagesAreNotSaved(t *testing.T) {
	for _, data := range [][]byte{[]byte("not an image"), testPNG(16, 16)[:35]} {
		t.Run(fmt.Sprint(len(data)), func(t *testing.T) {
			dir := testEnv(t)
			srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testPayload(w, data) }))
			defer srv.Close()
			t.Setenv("CODEX_API_URL", srv.URL)
			a := testArgs(dir)
			a.model = "2"
			_, e := generate("bad", "bad.png", a)
			if e == nil {
				t.Fatal("invalid image accepted")
			}
			if _, e = os.Stat(filepath.Join(dir, "bad.png")); !os.IsNotExist(e) {
				t.Fatal("invalid output published")
			}
		})
	}
}
func TestEveryImageReportsItsActualDimensions(t *testing.T) {
	dir := testEnv(t)
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testPayload(w, testPNG(3000, 1000), testPNG(64, 32)) }))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	a := testArgs(dir)
	a.model = "2"
	a.size = "4K"
	a.n = 2
	r, e := generate("sizes", "", a)
	if e != nil {
		t.Fatal(e)
	}
	assets := r["assets"].([]map[string]any)
	if len(assets) != 2 || assets[0]["actual_size"] != "3000x1000" || assets[1]["actual_size"] != "64x32" || r["actual_quality"] != nil || r["warning"] == nil {
		t.Fatal(r)
	}
}
func TestImageCountMismatchPublishesNothing(t *testing.T) {
	dir := testEnv(t)
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testPayload(w, testPNG(16, 16)) }))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	a := testArgs(dir)
	a.model = "2"
	a.n = 2
	_, e := generate("count", "bad.png", a)
	if e == nil {
		t.Fatal("wrong count accepted")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	if len(files) != 0 {
		t.Fatal(files)
	}
}
func TestUniqueAutomaticNamesAndAtomicNoOverwrite(t *testing.T) {
	dir := testEnv(t)
	paths := map[string]bool{}
	for i := 0; i < 100; i++ {
		p := outputPaths("", dir, "same", 1)[0]
		if paths[p] {
			t.Fatal("collision")
		}
		paths[p] = true
	}
	p := filepath.Join(dir, "exclusive.png")
	a, b := []byte("first"), []byte("second")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, data := range [][]byte{a, b} {
		wg.Add(1)
		go func(data []byte) { defer wg.Done(); results <- atomicWrite(p, data, 0600, false) }(data)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	actual, _ := os.ReadFile(p)
	if !bytes.Equal(actual, a) && !bytes.Equal(actual, b) {
		t.Fatal("partial write")
	}
}
func TestExplicitBatchCollisionDetectedBeforeSubmission(t *testing.T) {
	dir := testEnv(t)
	calls := 0
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; testPayload(w, testPNG(16, 16)) }))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	jobs := filepath.Join(dir, "jobs.jsonl")
	_ = os.WriteFile(jobs, []byte("{\"prompt\":\"one\",\"out\":\"same.png\"}\n{\"prompt\":\"two\",\"out\":\"same.png\"}\n"), 0600)
	e := runBatch([]string{"--input", jobs, "--out-dir", dir})
	if e == nil || calls != 0 {
		t.Fatal(calls, e)
	}
}
func TestConcurrentSamePromptRetainsTwoFiles(t *testing.T) {
	dir := testEnv(t)
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testPayload(w, testPNG(16, 16)) }))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	a := testArgs(dir)
	a.model = "2"
	ch := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := generate("same", "", a); ch <- e }()
	}
	wg.Wait()
	close(ch)
	for e := range ch {
		if e != nil {
			t.Fatal(e)
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.png"))
	if len(files) != 2 {
		t.Fatal(files)
	}
}
func TestResumeImageDoesNotRegenerateAndCanFixExtension(t *testing.T) {
	dir := testEnv(t)
	calls := 0
	var jpegData bytes.Buffer
	_ = jpeg.Encode(&jpegData, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil)
	srv := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; testPayload(w, jpegData.Bytes()) }))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	a := testArgs(dir)
	a.model = "2"
	result, e := generate("jpeg", "incorrect.png", a)
	if e == nil {
		t.Fatal("incorrect extension accepted")
	}
	target := filepath.Join(dir, "recovered.jpg")
	if e = runResume([]string{"--job", result["job_file"].(string), "--out", target}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatalf("regenerated during resume: %d", calls)
	}
	if data, e := os.ReadFile(target); e != nil || !bytes.Equal(data, jpegData.Bytes()) {
		t.Fatal(e)
	}
}
func TestCrossOriginVideoDownloadDoesNotReceiveAPIKey(t *testing.T) {
	dir := testEnv(t)
	downloads := 0
	auth := "not-called"
	video := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, make([]byte, 30)...)
	storage := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads++
		auth = r.Header.Get("Authorization")
		_, _ = w.Write(video)
	}))
	defer storage.Close()
	api := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			fmt.Fprint(w, `{"id":"task-1","status":"queued"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/content") {
			mockError(w, 404, "use URL")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "task-1", "status": "completed", "url": storage.URL + "/v.mp4"})
	}))
	defer api.Close()
	t.Setenv("CODEX_API_URL", api.URL)
	_, e := generateVideo("video", filepath.Join(dir, "video.mp4"), "", "1K", 8, false, false, time.Second, 4)
	if e != nil || downloads != 1 || auth != "" {
		t.Fatal(downloads, auth, e)
	}
}
func TestRedirectStripsAPIKeyAndPostIsNotReplayed(t *testing.T) {
	testEnv(t)
	auth := ""
	calls := 0
	dst := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		calls++
		fmt.Fprint(w, "ok")
	}))
	defer dst.Close()
	src := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dst.URL, http.StatusTemporaryRedirect)
	}))
	defer src.Close()
	client := newClient(time.Second, src.URL, nil, "redirect")
	_, e := doRequest(client, func() (*http.Request, error) {
		r, e := http.NewRequest("GET", src.URL, nil)
		r.Header.Set("Authorization", "Bearer "+dummyKey)
		return r, e
	}, 1)
	if e != nil || calls != 1 || auth != "" {
		t.Fatal(calls, auth, e)
	}
	calls = 0
	_, e = doRequest(client, func() (*http.Request, error) { return http.NewRequest("POST", src.URL, strings.NewReader("prompt")) }, 4)
	if e == nil || calls != 0 {
		t.Fatal("POST redirected", calls, e)
	}
}
func TestVideoResumeRetainsTaskAndDoesNotSubmitAgain(t *testing.T) {
	dir := testEnv(t)
	posts := 0
	queries := 0
	fail := true
	video := append([]byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'}, make([]byte, 30)...)
	api := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			fmt.Fprint(w, `{"id":"recover-task"}`)
			return
		}
		queries++
		if fail {
			mockError(w, 401, "fix auth")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/content") {
			_, _ = w.Write(video)
			return
		}
		fmt.Fprint(w, `{"id":"recover-task","status":"completed"}`)
	}))
	defer api.Close()
	t.Setenv("CODEX_API_URL", api.URL)
	result, e := generateVideo("video", filepath.Join(dir, "recover.mp4"), "", "1K", 8, false, false, time.Second, 1)
	if e == nil || result["task_id"] != "recover-task" {
		t.Fatal(result, e)
	}
	fail = false
	if e = runResume([]string{"--job", result["job_file"].(string)}); e != nil {
		t.Fatal(e)
	}
	if posts != 1 || queries != 3 {
		t.Fatal(posts, queries)
	}
}
func TestUnknownImageResumeDoesNotResubmitWithoutDeduplication(t *testing.T) {
	dir := testEnv(t)
	posts := 0
	api := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts++; mockError(w, 500, "uncertain") }))
	defer api.Close()
	t.Setenv("CODEX_API_URL", api.URL)
	a := testArgs(dir)
	a.model = "2"
	result, e := generate("unknown", "", a)
	if e == nil {
		t.Fatal("expected error")
	}
	e = runResume([]string{"--job", result["job_file"].(string)})
	if e == nil || posts != 1 {
		t.Fatal(posts, e)
	}
}

func TestInvalidGPTImage2SizeRejectedBeforeNetwork(t *testing.T) {
	dir := testEnv(t)
	a := testArgs(dir)
	a.model = "2"
	a.size = "999x17"
	a.dryRun = true
	if _, e := generate("invalid size", "", a); e == nil {
		t.Fatal("invalid size accepted")
	}
}
func TestAutomaticJPEGOutputUsesActualExtension(t *testing.T) {
	dir := testEnv(t)
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil)
	api := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { testPayload(w, b.Bytes()) }))
	defer api.Close()
	t.Setenv("CODEX_API_URL", api.URL)
	a := testArgs(dir)
	a.model = "2"
	result, e := generate("jpeg auto", "", a)
	if e != nil {
		t.Fatal(e)
	}
	path := result["outputs"].([]string)[0]
	if filepath.Ext(path) != ".jpg" {
		t.Fatal(path)
	}
	data, e := os.ReadFile(path)
	if e != nil || !bytes.Equal(data, b.Bytes()) {
		t.Fatal(e)
	}
}
func TestConfirmedRateLimitCanResumeAfterRetryLimit(t *testing.T) {
	dir := testEnv(t)
	skipRetrySleep(t)
	calls := 0
	limited := true
	api := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if limited {
			mockError(w, 429, "limited")
			return
		}
		testPayload(w, testPNG(16, 16))
	}))
	defer api.Close()
	t.Setenv("CODEX_API_URL", api.URL)
	a := testArgs(dir)
	a.model = "2"
	result, e := generate("limited", "", a)
	if e == nil || calls != 4 {
		t.Fatal(calls, e)
	}
	limited = false
	if e = runResume([]string{"--job", result["job_file"].(string)}); e != nil {
		t.Fatal(e)
	}
	if calls != 5 {
		t.Fatal(calls)
	}
}
func TestCompletedMissingImageRecoveredWithoutNetwork(t *testing.T) {
	dir := testEnv(t)
	calls := 0
	api := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; testPayload(w, testPNG(16, 16)) }))
	defer api.Close()
	t.Setenv("CODEX_API_URL", api.URL)
	a := testArgs(dir)
	a.model = "2"
	result, e := generate("recover deleted", "", a)
	if e != nil {
		t.Fatal(e)
	}
	p := result["outputs"].([]string)[0]
	if e = os.Remove(p); e != nil {
		t.Fatal(e)
	}
	if e = runResume([]string{"--job", result["job_file"].(string)}); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	if _, e = os.Stat(p); e != nil {
		t.Fatal(e)
	}
}
func TestPromptContainingDefaultFlagIsNotConsent(t *testing.T) {
	testEnv(t)
	fs := newFlagSet("generate")
	var prompt string
	fs.StringVar(&prompt, "prompt", "", "prompt")
	if e := fs.Parse([]string{"--prompt", "--use-default-url"}); e != nil {
		t.Fatal(e)
	}
	if useDefaultURL || prompt != "--use-default-url" {
		t.Fatal("image text changed URL consent")
	}
}
func TestPlainTextErrorRedactedBeforeTruncation(t *testing.T) {
	testEnv(t)
	key := strings.Repeat("fake-credential-", 40)
	rememberSecret(key)
	text := formatAPIError(401, []byte("Invalid key: "+key))
	if strings.Contains(text, "fake-credential-") {
		t.Fatal("partial credential leaked")
	}
}
func TestMainCredentialNeverSavedInFailureState(t *testing.T) {
	dir := testEnv(t)
	api := testImageServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mockError(w, 401, "bad credential "+dummyKey) }))
	defer api.Close()
	t.Setenv("CODEX_API_URL", api.URL)
	a := testArgs(dir)
	a.model = "2"
	_, _ = generate("no key in state", "", a)
	_ = filepath.Walk(stateDir(), func(p string, info os.FileInfo, e error) error {
		if e == nil && !info.IsDir() {
			b, re := os.ReadFile(p)
			if re == nil && bytes.Contains(b, []byte(dummyKey)) {
				t.Errorf("key retained in %s", p)
			}
		}
		return nil
	})
}

// Existing image-delivery regressions get a realistic non-generation catalog route.
// Catalog-specific tests use httptest.NewServer directly.
func testImageServer(handler http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			items := []map[string]string{}
			for _, id := range imagePriority {
				items = append(items, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
			return
		}
		handler.ServeHTTP(w, r)
	}))
}
