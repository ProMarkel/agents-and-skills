package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var useDefaultURL bool
var loginOnce sync.Once
var loginSettings map[string]string
var secretMu sync.Mutex
var secrets []string
var retrySleep = time.Sleep
var retryDelays = []time.Duration{5 * time.Second, 10 * time.Second, 15 * time.Second}

const maxResponseBytes int64 = 128 << 20
const maxImageBytes = 64 << 20
const maxImagePixels = 50_000_000

// Login-session values are read once, in memory. Never print or persist launchctl output.
func setting(name string) (string, string) {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v, "process_environment"
	}
	if runtime.GOOS != "darwin" || os.Getenv("CODEX_IMAGE_LOGIN_SESSION") == "0" {
		return "", "not_visible"
	}
	loginOnce.Do(func() {
		loginSettings = map[string]string{}
		allowed := map[string]bool{"CODEX_API_URL": true, "CODEX_API_KEY": true, "CODEX_IMAGE_DEFAULT_MODEL": true, "CODEX_VIDEO_DEFAULT_MODEL": true}
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(c, "/bin/launchctl", "print", "gui/"+strconv.Itoa(os.Getuid()))
		var out boundedBuffer
		cmd.Stdout = &out
		if cmd.Run() != nil {
			return
		}
		// Only the GUI session's top-level environment block is eligible.
		inBlock := false
		for _, line := range strings.Split(out.String(), "\n") {
			if line == "\tenvironment = {" {
				inBlock = true
				continue
			}
			if inBlock && line == "\t}" {
				break
			}
			if !inBlock {
				continue
			}
			k, v, ok := strings.Cut(strings.TrimSpace(line), " => ")
			if ok && allowed[k] {
				v = strings.TrimSpace(v)
				if strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
					if unquoted, e := strconv.Unquote(v); e == nil {
						v = unquoted
					}
				}
				loginSettings[k] = v
			}
		}
	})
	if v := loginSettings[name]; v != "" {
		return v, "macos_login_session"
	}
	return "", "not_visible"
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, errors.New("login-session diagnostic exceeded limit")
	}
	return b.Buffer.Write(p)
}

func rememberSecret(s string) {
	if s == "" {
		return
	}
	secretMu.Lock()
	defer secretMu.Unlock()
	for _, v := range secrets {
		if s == v {
			return
		}
	}
	secrets = append(secrets, s)
}

var bearerPattern = regexp.MustCompile(`(?i)(bearer[ \t]+)[A-Za-z0-9._~+/=-]+`)
var querySecretPattern = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|token|secret|signature|sig|credential)=)[^&\s"<>]+`)

func redact(s string) string {
	secretMu.Lock()
	defer secretMu.Unlock()
	for _, v := range secrets {
		for _, variant := range []string{v, url.QueryEscape(v), url.PathEscape(v), strings.Trim(strconv.Quote(v), `"`)} {
			s = strings.ReplaceAll(s, variant, "[REDACTED]")
		}
	}
	s = bearerPattern.ReplaceAllString(s, "${1}[REDACTED]")
	return querySecretPattern.ReplaceAllString(s, "${1}[REDACTED]")
}

type redactedWriter struct{ dst io.Writer }

func (w redactedWriter) Write(p []byte) (int, error) {
	_, e := io.WriteString(w.dst, redact(string(p)))
	return len(p), e
}

type actionError struct {
	Kind              string        `json:"code"`
	Message           string        `json:"message"`
	Status            int           `json:"http_status,omitempty"`
	RequestID         string        `json:"request_id,omitempty"`
	TaskID            string        `json:"task_id,omitempty"`
	SubmissionUnknown bool          `json:"submission_unknown,omitempty"`
	SafeRetry         bool          `json:"safe_to_retry,omitempty"`
	SwitchModel       bool          `json:"-"`
	RetryAfter        time.Duration `json:"-"`
}

func (e *actionError) Error() string { return redact(e.Message) }
func describeError(err error) any {
	var e *actionError
	if errors.As(err, &e) {
		return e
	}
	return map[string]any{"code": "local_error", "message": redact(err.Error())}
}

func validateURL(raw string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u == nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid URL: host required; embedded credentials and fragments are not allowed")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("API and download URLs must use HTTP or HTTPS")
	}
	return u, nil
}
func configuredRoot() (string, error) {
	value, _ := setting("CODEX_API_URL")
	if value == "" {
		if !useDefaultURL {
			return "", &actionError{Kind: "configuration_required", Message: "CODEX_API_URL is not visible. Choose: configure the API URL, or explicitly continue with https://apinebula.com using --use-default-url."}
		}
		value = defaultAPIURL
	}
	u, e := validateURL(value)
	if e != nil {
		return "", e
	}
	if u.RawQuery != "" {
		return "", errors.New("API base URL must not contain query parameters")
	}
	return apiRoot(u.String()), nil
}
func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
func randomID() string {
	var b [12]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("secure random generator unavailable")
	}
	return hex.EncodeToString(b[:])
}

type retryBudget struct {
	mu    sync.Mutex
	used  int
	limit int
}

func (b *retryBudget) next(attempt int, after time.Duration) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used >= b.limit || b.used >= len(retryDelays) {
		return 0, false
	}
	d := retryDelays[b.used]
	// Do not violate a server Retry-After or silently exceed the user's wait budget.
	if after > d {
		return 0, false
	}
	b.used++
	return d, true
}

type policyTransport struct {
	base       http.RoundTripper
	root       string
	idempotent bool
	budget     *retryBudget
	requestID  string
}

func (t *policyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("Authorization") != "" {
		root, err := url.Parse(t.root)
		if err != nil || !sameOrigin(root, r.URL) {
			return nil, errors.New("API credential refused for a different origin")
		}
	}
	return t.base.RoundTrip(r)
}
func newClient(timeout time.Duration, root string, budget *retryBudget, id string) *http.Client {
	if budget == nil {
		budget = &retryBudget{limit: 3}
	}
	t := &policyTransport{base: http.DefaultTransport, root: root, budget: budget, requestID: id, idempotent: os.Getenv("CODEX_API_IDEMPOTENCY_SUPPORTED") == "1"}
	return &http.Client{Timeout: timeout, Transport: t, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		// Never replay a generation POST through a redirect.
		first := via[0]
		if first.Method != http.MethodGet && first.Method != http.MethodHead {
			return http.ErrUseLastResponse
		}
		if _, err := validateURL(req.URL.String()); err != nil {
			return err
		}
		if first.URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("HTTPS downgrade refused")
		}
		for _, previous := range via {
			if !sameOrigin(previous.URL, req.URL) {
				req.Header.Del("Authorization")
				req.Header.Del("Proxy-Authorization")
				req.Header.Del("Cookie")
				break
			}
		}
		return nil
	}}
}
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("response exceeds configured size limit")
	}
	return b, nil
}
func headerRetryAfter(h http.Header) time.Duration {
	v := h.Get("Retry-After")
	if n, e := strconv.Atoi(v); e == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, e := http.ParseTime(v); e == nil {
		return time.Until(t)
	}
	return 0
}

// GET/download retries and POST submission retries deliberately have different boundaries.
func reliableRequest(client *http.Client, makeRequest func() (*http.Request, error), maxAttempts int) ([]byte, error) {
	p, _ := client.Transport.(*policyTransport)
	budget := &retryBudget{limit: 3}
	if p != nil {
		budget = p.budget
	}
	if maxAttempts < 1 {
		return nil, errors.New("max attempts must be positive")
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := makeRequest()
		if err != nil {
			return nil, err
		}
		if _, err = validateURL(req.URL.String()); err != nil {
			return nil, err
		}
		if err = req.Context().Err(); err != nil {
			return nil, err
		}
		post := req.Method != http.MethodGet && req.Method != http.MethodHead
		dedup := false
		if post && p != nil && p.idempotent {
			req.Header.Set("Idempotency-Key", p.requestID)
			dedup = p.requestID != ""
		}
		resp, err := client.Do(req)
		issue := &actionError{}
		if err != nil {
			issue.Kind = "network_error"
			issue.Message = "API request failed: network error or timeout"
			issue.SubmissionUnknown = post
			issue.SafeRetry = !post || dedup
		} else {
			raw, readErr := readLimited(resp.Body, maxResponseBytes)
			resp.Body.Close()
			issue.Status = resp.StatusCode
			issue.RequestID = resp.Header.Get("x-request-id")
			issue.RetryAfter = headerRetryAfter(resp.Header)
			if readErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return raw, nil
			}
			if readErr != nil {
				issue.Kind = "response_read_error"
				issue.Message = "Could not read complete API response: " + readErr.Error()
				issue.SubmissionUnknown = post
				issue.SafeRetry = !post || dedup
			} else {
				issue.Kind = "api_error"
				issue.Message = formatAPIError(resp.StatusCode, raw)
				var obj map[string]any
				_ = json.Unmarshal(raw, &obj)
				issue.TaskID = videoTaskID(obj)
				// An upstream task id proves acceptance; a request id alone does not.
				if issue.TaskID == jsonString(obj, "request_id") && jsonString(obj, "id", "task_id") == "" {
					issue.TaskID = ""
				}
				if issue.TaskID == "" {
					issue.TaskID = jsonString(asObject(obj["error"]), "task_id", "id")
				}
				issue.SubmissionUnknown = post && (resp.StatusCode >= 500 || resp.StatusCode < 400 || issue.TaskID != "")
				issue.SafeRetry = retryable[resp.StatusCode] && (!post || dedup || (resp.StatusCode == 429 && issue.TaskID == ""))
				issue.SwitchModel = post && issue.TaskID == "" && (resp.StatusCode == 404 || resp.StatusCode == 405)
				if resp.StatusCode == 401 || resp.StatusCode == 403 {
					issue.Kind = "authentication_or_permission_error"
					issue.SafeRetry = false
				}
			}
		}
		if req.Context().Err() != nil || !issue.SafeRetry || attempt == maxAttempts {
			return nil, issue
		}
		d, ok := budget.next(attempt, issue.RetryAfter)
		if !ok {
			return nil, issue
		}
		fmt.Fprintf(redactedWriter{os.Stderr}, "Retry %d/3 in %s (%s)\n", budget.used, d, issue.Kind)
		retrySleep(d)
	}
	return nil, errors.New("retry limit reached")
}

func atomicWrite(path string, data []byte, mode os.FileMode, force bool) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".codex-write-*")
	if e != nil {
		return e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if e = f.Chmod(mode); e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if force {
		return os.Rename(tmp, path)
	}
	// link is atomic and fails if a concurrent process has already created the destination.
	return os.Link(tmp, path)
}
func reserveOutputs(paths []string, force bool) (func(), error) {
	var locks []string
	release := func() {
		for _, p := range locks {
			_ = os.Remove(p)
		}
	}
	for _, p := range paths {
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			release()
			return nil, e
		}
		lock := p + ".codex-image.lock"
		f, e := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			release()
			return nil, fmt.Errorf("output is reserved by another task: %s", p)
		}
		_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
		f.Close()
		locks = append(locks, lock)
	}
	if e := checkOutputs(paths, force); e != nil {
		release()
		return nil, e
	}
	return release, nil
}
func validateImages(images [][]byte) error {
	for i, data := range images {
		if len(data) > maxImageBytes {
			return fmt.Errorf("image %d exceeds 64 MiB", i+1)
		}
		cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil {
			return fmt.Errorf("image %d is not a supported image", i+1)
		}
		if cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
			return fmt.Errorf("image %d has unsafe dimensions", i+1)
		}
		if _, _, e = image.Decode(bytes.NewReader(data)); e != nil {
			return fmt.Errorf("image %d is damaged or incomplete: %w", i+1, e)
		}
	}
	return nil
}

func runDoctor(argv []string) error {
	fs := newFlagSet("doctor")
	outDir := fs.String("out-dir", defaultOutDir, "output directory")
	if e := fs.Parse(argv); e != nil {
		return e
	}
	key, ks := setting("CODEX_API_KEY")
	rememberSecret(key)
	_, us := setting("CODEX_API_URL")
	root, e := configuredRoot()
	exe, _ := os.Executable()
	result := map[string]any{"network_used": false, "executable": exe, "platform": runtime.GOOS + "/" + runtime.GOARCH, "key_visible": key != "", "key_source": ks, "url_source": us, "api_root": root, "image_default": currentDefaultModel(), "video_default": currentDefaultVideoModel(), "state_dir": stateDir()}
	if u, ue := url.Parse(root); ue == nil && u.Scheme == "http" {
		local := strings.EqualFold(u.Hostname(), "localhost")
		if ip := net.ParseIP(u.Hostname()); ip != nil {
			local = ip.IsLoopback()
		}
		if !local {
			result["transport_warning"] = "The explicitly configured API uses unencrypted HTTP. Prefer HTTPS when your provider supports it."
		}
	}
	if e != nil {
		result["configuration_error"] = describeError(e)
		result["choices"] = []string{"configure CODEX_API_URL", "continue with --use-default-url (https://apinebula.com)"}
	}
	dir := *outDir
	for {
		_, se := os.Stat(dir)
		if se == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	f, we := os.CreateTemp(dir, ".codex-image-doctor-*")
	if we == nil {
		f.Close()
		_ = os.Remove(f.Name())
	}
	result["output_directory"] = *outDir
	result["output_writable"] = we == nil
	result["ok"] = e == nil && key != "" && we == nil
	printJSON(result)
	if e != nil {
		return e
	}
	if key == "" {
		return errors.New("CODEX_API_KEY is not visible in the process or macOS login session")
	}
	return we
}
