package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var imagePriority = []string{"gpt-image-2.5", "gpt-image-2.5-sunburst", "gpt-image-2.5-flare", "gpt-image-2"}
var modelMu sync.Mutex

func stateDir() string {
	if s := os.Getenv("CODEX_IMAGE_STATE_DIR"); s != "" {
		return s
	}
	return filepath.Join(defaultOutDir, ".state")
}
func digest(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
func accountScope(root, key string) string {
	return digest("codex-image-gen-scope-v1\x00" + root + "\x00" + key)
}

type modelRecord struct {
	Model          string `json:"model"`
	Operation      string `json:"operation"`
	Status         string `json:"status"`
	LastSuccess    string `json:"last_success,omitempty"`
	LastFailure    string `json:"last_failure,omitempty"`
	LastOutcome    string `json:"last_outcome,omitempty"`
	HTTPStatus     int    `json:"last_http_status,omitempty"`
	CatalogSupport string `json:"catalog_support,omitempty"`
	CatalogChecked string `json:"catalog_checked_at,omitempty"`
}

func modelRecordPath(scope, op, model string) string {
	return filepath.Join(stateDir(), "models", scope, op, digest(model)+".json")
}
func loadModelHistory(scope, op, model string) modelRecord {
	r := modelRecord{Model: model, Operation: op, Status: "unverified"}
	data, e := os.ReadFile(modelRecordPath(scope, op, model))
	if e == nil {
		_ = json.Unmarshal(data, &r)
	}
	return r
}
func loadModelRecord(scope, op, model string) modelRecord {
	r := loadModelHistory(scope, op, model)
	if c, e := readCatalog(scope); e == nil {
		r.CatalogChecked = c.CheckedAt
		r.CatalogSupport = "supported"
		if !containsModel(c.IDs, model) {
			r.CatalogSupport = "unsupported"
			r.Status = "unsupported"
		}
	}
	return r
}
func recordModel(root, key, op, model string, err error) error {
	return recordModelScope(accountScope(root, key), op, model, err)
}
func recordModelScope(scope, op, model string, err error) error {
	modelMu.Lock()
	defer modelMu.Unlock()
	path := modelRecordPath(scope, op, model)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, e := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			f.Close()
			break
		}
		if !os.IsExist(e) || time.Now().After(deadline) {
			return fmt.Errorf("availability record is locked or unwritable: %w", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.Remove(lockPath)
	r := loadModelHistory(scope, op, model)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err == nil {
		r.Status = "available"
		r.LastSuccess = now
		r.LastOutcome = "success"
		r.HTTPStatus = 0
	} else {
		r.LastFailure = now
		r.LastOutcome = "temporary_or_unclassified_failure"
		var e *actionError
		if errors.As(err, &e) {
			r.HTTPStatus = e.Status
			if e.Status == 401 {
				return nil
			} // Account authentication failure is not model availability.
			text := strings.ToLower(e.Message)
			if e.SwitchModel && (strings.Contains(text, "model_not_found") || strings.Contains(text, "unknown model") || strings.Contains(text, "model does not exist")) {
				r.Status = "unavailable"
				r.LastOutcome = "model_unavailable"
			}
			if e.SwitchModel {
				r.Status = "unavailable"
				r.LastOutcome = "request_rejected_by_current_service"
			}
		}
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	return atomicWrite(modelRecordPath(scope, op, model), data, 0600, true)
}
func selectModels(args commonArgs, root, key, op string) ([]string, error) {
	name := strings.TrimSpace(args.model)
	if name == "" {
		if configured, _ := setting("CODEX_IMAGE_DEFAULT_MODEL"); configured != "" && !strings.EqualFold(configured, "auto") {
			name = configured
		}
	}
	explicit := ""
	if name != "" && !strings.EqualFold(name, "auto") {
		id, e := resolveModel(name)
		if e != nil {
			return nil, e
		}
		explicit = id
	}
	// Plans are offline and provisional; execution must consult the API catalog.
	if args.dryRun {
		if explicit != "" {
			return []string{explicit}, nil
		}
		key, _ = setting("CODEX_API_KEY")
		if root != "" && key != "" {
			if c, e := readCatalog(accountScope(root, key)); e == nil && len(c.Images) > 0 {
				return catalogOrder(c.Images), nil
			}
		}
		return append([]string{}, imagePriority...), nil
	}
	c, e := getCatalog(root, key, args.timeout, args.budget, args.maxAttempts, false)
	if e != nil {
		return nil, e
	}
	if explicit != "" {
		if !containsModel(c.IDs, explicit) {
			return nil, &actionError{Kind: "model_unsupported", Message: fmt.Sprintf("Model %q is absent from the API model list (unsupported); no generation submitted. Refresh with models or choose an advertised image model.", explicit)}
		}
		return []string{explicit}, nil
	}
	ordered := catalogOrder(c.Images)
	if len(ordered) == 0 {
		return nil, &actionError{Kind: "no_supported_image_model", Message: "The API model list contains no recognized image-generation models; no generation submitted. Use models --all to inspect the list and explicitly select an advertised image-capable ID if needed."}
	}
	var known []string
	for _, m := range ordered {
		if loadModelRecord(accountScope(root, key), op, m).Status == "available" {
			known = append(known, m)
		}
	}
	if len(known) > 0 {
		return known, nil
	}
	return ordered, nil
}

type jobState struct {
	Version     int      `json:"version"`
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	State       string   `json:"state"`
	Created     string   `json:"created"`
	Updated     string   `json:"updated"`
	Scope       string   `json:"account_scope"`
	Root        string   `json:"api_root"`
	Endpoint    string   `json:"endpoint"`
	Model       string   `json:"model"`
	Size        string   `json:"size"`
	Quality     string   `json:"quality,omitempty"`
	Paths       []string `json:"planned_outputs"`
	N           int      `json:"n"`
	Force       bool     `json:"overwrite_authorized"`
	AutoOutput  bool     `json:"automatic_output_name,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Idempotent  bool     `json:"idempotency_confirmed"`
	TaskID      string   `json:"task_id,omitempty"`
	RequestID   string   `json:"request_id,omitempty"`
	Error       any      `json:"error,omitempty"`
	NextActions []string `json:"next_actions,omitempty"`
	dir         string
}

func newJob(kind, root, key, endpoint, model string, args commonArgs, paths []string) (*jobState, error) {
	id := time.Now().UTC().Format("20060102T150405") + "-" + randomID()
	abs := make([]string, len(paths))
	for i, p := range paths {
		a, e := filepath.Abs(p)
		if e != nil {
			return nil, e
		}
		abs[i] = a
	}
	dir, e := filepath.Abs(filepath.Join(stateDir(), "jobs", id))
	if e != nil {
		return nil, e
	}
	j := &jobState{Version: 1, ID: id, Kind: kind, State: "prepared", Created: time.Now().UTC().Format(time.RFC3339Nano), Scope: accountScope(root, key), Root: root, Endpoint: endpoint, Model: model, Size: args.size, Quality: args.quality, Paths: abs, N: args.n, Force: args.force, Idempotent: os.Getenv("CODEX_API_IDEMPOTENCY_SUPPORTED") == "1", dir: dir}
	return j, j.save()
}
func (j *jobState) save() error {
	j.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	data, e := json.MarshalIndent(j, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(filepath.Join(j.dir, "job.json"), []byte(redact(string(data))), 0600, true)
}
func (j *jobState) fail(err error, state string) {
	j.State = state
	j.Error = describeError(err)
	j.NextActions = []string{"generate_new_with_confirmation", "stop"}
	_, responseErr := os.Stat(filepath.Join(j.dir, "response.json"))
	if responseErr == nil || (j.Kind == "video" && j.TaskID != "") || j.canResumeSubmit() {
		j.NextActions = append([]string{"resume_original"}, j.NextActions...)
	} else if state == "submission_unknown" {
		j.NextActions = append([]string{"check_provider_manually"}, j.NextActions...)
	}
	if e := j.save(); e != nil {
		fmt.Fprintln(redactedWriter{os.Stderr}, "warning: could not update recovery manifest:", e)
	}
}
func (j *jobState) canResumeSubmit() bool {
	if j.Idempotent && os.Getenv("CODEX_API_IDEMPOTENCY_SUPPORTED") == "1" {
		return true
	}
	if j.State != "rejected" {
		return false
	}
	b, _ := json.Marshal(j.Error)
	var e actionError
	_ = json.Unmarshal(b, &e)
	return e.SafeRetry && !e.SubmissionUnknown && e.Status == 429
}
func (j *jobState) completedFilesValid() bool {
	for i, p := range j.Paths {
		f, e := os.Open(p)
		if e != nil {
			return false
		}
		b, e := readLimited(f, maxResponseBytes)
		f.Close()
		if e != nil {
			return false
		}
		if j.Kind == "video" {
			if !validVideo(b) {
				return false
			}
		} else if validateImages([][]byte{b}) != nil {
			return false
		}
		cache := fmt.Sprintf("image-%d.bin", i+1)
		if j.Kind == "video" {
			cache = "video.bin"
		}
		if original, e := os.ReadFile(filepath.Join(j.dir, cache)); e == nil && sha256.Sum256(original) != sha256.Sum256(b) {
			return false
		}
	}
	return len(j.Paths) > 0
}
func (j *jobState) result() map[string]any {
	return map[string]any{"ok": false, "job_id": j.ID, "job_file": filepath.Join(j.dir, "job.json"), "state": j.State, "model": j.Model, "task_id": j.TaskID, "request_id": j.RequestID, "outputs": []string{}, "error": j.Error, "next_actions": j.NextActions}
}
func (j *jobState) persistResponse(raw []byte) error {
	// No primary credential is retained, even if an upstream unexpectedly echoes it.
	secretMu.Lock()
	safe := string(raw)
	for _, s := range secrets {
		safe = strings.ReplaceAll(safe, s, "[REDACTED]")
	}
	secretMu.Unlock()
	return atomicWrite(filepath.Join(j.dir, "response.json"), []byte(safe), 0600, true)
}
func loadJob(value string) (*jobState, error) {
	file := value
	if filepath.Base(value) == value {
		file = filepath.Join(stateDir(), "jobs", value, "job.json")
	}
	data, e := os.ReadFile(file)
	if e != nil {
		return nil, e
	}
	var j jobState
	if e = json.Unmarshal(data, &j); e != nil {
		return nil, e
	}
	if j.Version != 1 || j.ID == "" {
		return nil, errors.New("unsupported recovery manifest")
	}
	j.dir, e = filepath.Abs(filepath.Dir(file))
	return &j, e
}
func runResume(argv []string) error {
	fs := newFlagSet("resume")
	file := fs.String("job", "", "job id or manifest path")
	out := fs.String("out", "", "new output base path, without regenerating")
	force := fs.Bool("force", false, "explicitly authorize overwrite")
	timeout := fs.Duration("timeout", 150*time.Second, "request timeout, e.g. 150s")
	attempts := fs.Int("max-attempts", 4, "maximum attempts, including the first")
	if e := fs.Parse(argv); e != nil {
		return e
	}
	if *file == "" {
		return errors.New("--job is required")
	}
	if *attempts < 1 || *attempts > 4 || *timeout <= 0 {
		return errors.New("resume allows 1-4 attempts and a positive timeout")
	}
	j, e := loadJob(*file)
	if e != nil {
		return e
	}
	root, e := configuredRoot()
	if e != nil {
		return e
	}
	key, e := apiKey()
	if e != nil {
		return e
	}
	if j.Scope != accountScope(root, key) {
		return errors.New("recovery job belongs to a different API URL or credential")
	}
	u, ue := validateURL(j.Endpoint)
	ru, _ := url.Parse(root)
	if ue != nil || !sameOrigin(u, ru) || j.Root != root {
		return errors.New("recovery endpoint does not match the configured API origin")
	}
	if j.State == "complete" && j.completedFilesValid() {
		result := j.result()
		result["ok"] = true
		result["outputs"] = j.Paths
		if j.Kind != "video" {
			var images [][]byte
			for _, p := range j.Paths {
				b, _ := os.ReadFile(p)
				images = append(images, b)
			}
			spec := imageSpec{model: j.Model, extra: map[string]any{"model": j.Model, "requested_quality": j.Quality}}
			attachActual(&spec, images, j.Size)
			for k, v := range spec.extra {
				result[k] = v
			}
		} else {
			result["actual_size"] = nil
		}
		printJSON(result)
		return nil
	}
	if *out != "" {
		j.AutoOutput = false
		j.Paths = outputPaths(*out, ".", j.ID, j.N)
		for i, p := range j.Paths {
			j.Paths[i], _ = filepath.Abs(p)
		}
	}
	j.Force = *force // Previous overwrite permission never authorizes a later resume overwrite.
	release, e := reserveOutputs(j.Paths, j.Force)
	if e != nil {
		return e
	}
	defer release()
	budget := &retryBudget{limit: *attempts - 1}
	client := newClient(*timeout, root, budget, j.ID)
	var result map[string]any
	if j.Kind == "video" {
		result, e = finishVideo(j, client, key, *timeout, *attempts)
	} else {
		raw, re := os.ReadFile(filepath.Join(j.dir, "response.json"))
		if re != nil {
			if !j.canResumeSubmit() {
				e = &actionError{Kind: "submission_unknown", Message: "No saved response. The API does not have confirmed idempotency support; automatically resubmitting could create another paid job.", SubmissionUnknown: true}
				j.fail(e, "submission_unknown")
				printJSON(j.result())
				return e
			}
			body, be := os.ReadFile(filepath.Join(j.dir, "request.bin"))
			if be != nil {
				return be
			}
			j.State = "submitting"
			if e = j.save(); e != nil {
				return e
			}
			raw, e = submitImage(client, j, key, body, *attempts)
			if e != nil {
				state := "submission_unknown"
				var ae *actionError
				if errors.As(e, &ae) && !ae.SubmissionUnknown {
					state = "rejected"
				}
				j.fail(e, state)
				printJSON(j.result())
				return e
			}
			if e = j.persistResponse(raw); e != nil {
				j.fail(e, "response_persistence_failed")
				printJSON(j.result())
				return e
			}
		}
		args := commonArgs{model: j.Model, size: j.Size, quality: j.Quality, n: j.N, force: j.Force, maxAttempts: *attempts, timeout: *timeout, budget: budget}
		spec, se := prepareImageRequest("", args)
		if se != nil {
			return se
		}
		result, e = finishImage(j, client, raw, spec, args)
	}
	if e != nil {
		printJSON(j.result())
		return e
	}
	if j.Kind != "video" {
		_ = recordModel(root, key, j.Kind, j.Model, nil)
	}
	printJSON(result)
	return nil
}

func assetBytes(client *http.Client, source string, attempts int) ([]byte, error) {
	u, e := validateURL(source)
	if e != nil {
		return nil, e
	}
	if p, ok := client.Transport.(*policyTransport); ok {
		root, _ := url.Parse(p.root)
		if root != nil && root.Scheme == "https" && u.Scheme != "https" {
			return nil, errors.New("result download would downgrade the configured HTTPS API to HTTP")
		}
	}
	return doRequest(client, func() (*http.Request, error) { return http.NewRequest(http.MethodGet, source, nil) }, attempts)
}
func safeTaskPath(endpoint, taskID string) string { return endpoint + "/" + url.PathEscape(taskID) }
