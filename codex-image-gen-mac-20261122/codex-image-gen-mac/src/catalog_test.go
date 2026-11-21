package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

func writeCatalogResponse(w http.ResponseWriter, ids ...string) {
	items := []map[string]string{}
	for _, id := range ids {
		items = append(items, map[string]string{"id": id})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
}

func TestCatalogMissingPreferredSkippedAndExtrasKeepReturnOrder(t *testing.T) {
	dir := testEnv(t)
	gets := 0
	posts := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			gets++
			writeCatalogResponse(w, "flux-z", "gpt-image-2", "gpt-4o", "flux-a", "grok-imagine-video", "flux-z")
			return
		}
		var b struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		posts = append(posts, b.Model)
		if b.Model != "flux-a" {
			mockError(w, 404, "unknown model")
			return
		}
		testPayload(w, testPNG(16, 16))
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	result, e := generate("catalog order", "", testArgs(dir))
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(posts, []string{"gpt-image-2", "flux-z", "flux-a"}) || gets != 1 || result["model"] != "flux-a" {
		t.Fatal(posts, gets, result)
	}
	root, _ := configuredRoot()
	scope := accountScope(root, dummyKey)
	for _, id := range imagePriority[:3] {
		r := loadModelRecord(scope, "generate", id)
		if r.Status != "unsupported" || r.CatalogChecked == "" || r.LastFailure != "" {
			t.Fatal(r)
		}
	}
	posts = nil
	if _, e = generate("cached success", "", testArgs(dir)); e != nil {
		t.Fatal(e)
	}
	if gets != 1 || !reflect.DeepEqual(posts, []string{"flux-a"}) {
		t.Fatal(gets, posts)
	}
}

func TestCatalogOverridesHistoricalSuccessAndRestoresItWhenRelisted(t *testing.T) {
	dir := testEnv(t)
	ids := []string{"gpt-image-2"}
	posts := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeCatalogResponse(w, ids...)
			return
		}
		var b struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		posts = append(posts, b.Model)
		testPayload(w, testPNG(16, 16))
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	root, _ := configuredRoot()
	scope := accountScope(root, dummyKey)
	if e := recordModel(root, dummyKey, "generate", "gpt-image-2.5", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := generate("missing success", "", testArgs(dir)); e != nil {
		t.Fatal(e)
	}
	r := loadModelRecord(scope, "generate", "gpt-image-2.5")
	if r.Status != "unsupported" || r.LastSuccess == "" || !reflect.DeepEqual(posts, []string{"gpt-image-2"}) {
		t.Fatal(r, posts)
	}
	ids = append(ids, "gpt-image-2.5")
	if _, e := getCatalog(root, dummyKey, time.Second, nil, 1, true); e != nil {
		t.Fatal(e)
	}
	if r = loadModelRecord(scope, "generate", "gpt-image-2.5"); r.Status != "available" {
		t.Fatal(r)
	}
	posts = nil
	if _, e := generate("relisted success", "", testArgs(dir)); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(posts, []string{"gpt-image-2.5"}) {
		t.Fatal(posts)
	}
}

func TestCatalogExplicitAbsentStopsWithoutGeneration(t *testing.T) {
	dir := testEnv(t)
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeCatalogResponse(w, "gpt-image-2")
			return
		}
		posts++
		testPayload(w, testPNG(16, 16))
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	a := testArgs(dir)
	a.model = "2.6"
	if _, e := generate("absent explicit", "", a); e == nil || posts != 0 {
		t.Fatal(posts, e)
	}
	root, _ := configuredRoot()
	if r := loadModelRecord(accountScope(root, dummyKey), "generate", "gpt-image-2.6"); r.Status != "unsupported" {
		t.Fatal(r)
	}
	if e := listModels([]string{"--check-availability", "--model", "2.6"}); e == nil || posts != 0 {
		t.Fatal(posts, e)
	}
}

func TestCatalogFailureOrMalformedDoesNotOverwriteObservations(t *testing.T) {
	for _, response := range []string{"http-error", "not-json", `{"wrong":[]}`, `{"data":null}`, `{"data":[{}]}`} {
		t.Run(response, func(t *testing.T) {
			dir := testEnv(t)
			posts := 0
			broken := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					posts++
					return
				}
				if !broken {
					writeCatalogResponse(w, imagePriority...)
					return
				}
				if response == "http-error" {
					mockError(w, 503, "catalog unavailable")
					return
				}
				_, _ = w.Write([]byte(response))
			}))
			defer srv.Close()
			t.Setenv("CODEX_API_URL", srv.URL)
			root, _ := configuredRoot()
			scope := accountScope(root, dummyKey)
			c, e := getCatalog(root, dummyKey, time.Second, nil, 1, true)
			if e != nil {
				t.Fatal(e)
			}
			c.CheckedAt = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
			b, _ := json.Marshal(c)
			if e = atomicWrite(catalogPath(scope), b, 0600, true); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(catalogPath(scope))
			broken = true
			a := testArgs(dir)
			a.maxAttempts = 1
			if _, e = generate("catalog failure", "", a); e == nil || posts != 0 {
				t.Fatal(posts, e)
			}
			after, _ := os.ReadFile(catalogPath(scope))
			if string(before) != string(after) {
				t.Fatal("failed query replaced prior observations")
			}
			if r := loadModelRecord(scope, "generate", "gpt-image-2.5"); r.Status != "unverified" {
				t.Fatal(r)
			}
		})
	}
}

func TestCatalogEmptyOrNoImageListNeverSubmits(t *testing.T) {
	for _, ids := range [][]string{{}, {"gpt-4o", "grok-imagine-video", "sora-2"}} {
		t.Run("empty-or-nonimage", func(t *testing.T) {
			dir := testEnv(t)
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					writeCatalogResponse(w, ids...)
					return
				}
				posts++
			}))
			defer srv.Close()
			t.Setenv("CODEX_API_URL", srv.URL)
			if _, e := generate("no image models", "", testArgs(dir)); e == nil || posts != 0 {
				t.Fatal(posts, e)
			}
		})
	}
}

func TestCatalogDryRunIsOfflineAndProvisionalWithStaleCache(t *testing.T) {
	dir := testEnv(t)
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		writeCatalogResponse(w, "flux-custom", "gpt-image-2")
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	root, _ := configuredRoot()
	scope := accountScope(root, dummyKey)
	c, e := getCatalog(root, dummyKey, time.Second, nil, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	c.CheckedAt = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(c)
	_ = atomicWrite(catalogPath(scope), b, 0600, true)
	before, _ := os.ReadFile(catalogPath(scope))
	a := testArgs(dir)
	a.dryRun = true
	result, e := generate("offline catalog", "", a)
	if e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(catalogPath(scope))
	if gets != 1 || string(before) != string(after) || result["model_selection_provisional"] != true || !reflect.DeepEqual(result["model_candidates"], []string{"gpt-image-2", "flux-custom"}) {
		t.Fatal(result, gets)
	}
}

func TestCatalogMetadataAndScopeIsolation(t *testing.T) {
	testEnv(t)
	c, e := parseCatalog([]byte(`{"data":[{"id":"custom-render-z","output_modalities":["image"]},{"id":"gpt-image-2"},{"id":"custom-render-a","output_modalities":["image"]},{"id":"gpt-image-2"},{"id":"gpt-4o"}]}`))
	if e != nil || !reflect.DeepEqual(catalogOrder(c.Images), []string{"gpt-image-2", "custom-render-z", "custom-render-a"}) {
		t.Fatal(c, e)
	}
	root := "https://example.invalid/v1"
	b, _ := json.Marshal(c)
	_ = atomicWrite(catalogPath(accountScope(root, dummyKey)), b, 0600, true)
	if _, e = readCatalog(accountScope(root, "another-fake-key")); e == nil {
		t.Fatal("catalog crossed credentials")
	}
	if _, e = readCatalog(accountScope("https://other.invalid/v1", dummyKey)); e == nil {
		t.Fatal("catalog crossed API roots")
	}
}

func TestCatalogRetriesShareGenerationBudget(t *testing.T) {
	dir := testEnv(t)
	waits := skipRetrySleep(t)
	gets, posts := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			gets++
			if gets == 1 {
				mockError(w, 503, "temporary catalog failure")
				return
			}
			writeCatalogResponse(w, "gpt-image-2")
			return
		}
		posts++
		mockError(w, 429, "definite rate-limit rejection")
	}))
	defer srv.Close()
	t.Setenv("CODEX_API_URL", srv.URL)
	if _, e := generate("shared budget", "", testArgs(dir)); e == nil {
		t.Fatal("expected exhausted rate limit")
	}
	if gets != 2 || posts != 3 || !reflect.DeepEqual(*waits, []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second}) {
		t.Fatal(gets, posts, *waits)
	}
}
