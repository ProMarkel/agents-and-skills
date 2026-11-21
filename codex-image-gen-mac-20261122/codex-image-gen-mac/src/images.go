package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func imageBody(prompt string, inputs []string, mask string, spec imageSpec) ([]byte, string, error) {
	if len(inputs) == 0 {
		b, e := json.Marshal(spec.payload)
		return b, "application/json", e
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range spec.fields {
		if e := w.WriteField(k, v); e != nil {
			return nil, "", e
		}
	}
	add := func(field, path string) error {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return e
		}
		if info.Size() > maxImageBytes {
			return errors.New("input image exceeds 64 MiB")
		}
		part, e := w.CreateFormFile(field, filepath.Base(path))
		if e != nil {
			return e
		}
		_, e = io.Copy(part, f)
		return e
	}
	for _, p := range inputs {
		if e := add("image", p); e != nil {
			return nil, "", e
		}
	}
	if mask != "" {
		if e := add("mask", mask); e != nil {
			return nil, "", e
		}
	}
	if e := w.Close(); e != nil {
		return nil, "", e
	}
	return b.Bytes(), w.FormDataContentType(), nil
}
func submitImage(client *http.Client, j *jobState, key string, body []byte, attempts int) ([]byte, error) {
	return doRequest(client, func() (*http.Request, error) {
		req, e := http.NewRequest(http.MethodPost, j.Endpoint, bytes.NewReader(body))
		if e == nil {
			req.Header.Set("Authorization", "Bearer "+key)
			req.Header.Set("Content-Type", j.ContentType)
		}
		return req, e
	}, attempts)
}
func runImageTask(prompt string, inputs []string, mask, out string, args commonArgs) (map[string]any, error) {
	if e := validateCommon(args); e != nil {
		return nil, e
	}
	op := "generate"
	operation := "generations"
	if len(inputs) > 0 {
		op = "edit"
		operation = "edits"
	}
	for _, p := range append(append([]string{}, inputs...), mask) {
		if p == "" {
			continue
		}
		f, e := os.Open(p)
		if e != nil {
			return nil, e
		}
		b, e := readLimited(f, maxImageBytes)
		f.Close()
		if e != nil {
			return nil, e
		}
		if e = validateImages([][]byte{b}); e != nil {
			return nil, fmt.Errorf("invalid input %s: %w", p, e)
		}
	}
	root, rootErr := configuredRoot()
	if args.dryRun {
		if rootErr != nil {
			var ae *actionError
			if !errors.As(rootErr, &ae) || ae.Kind != "configuration_required" {
				return nil, rootErr
			}
		}
		candidates, e := selectModels(args, root, "", op)
		if e != nil {
			return nil, e
		}
		// A dry run does not require credentials or mark a model as working.
		selected := args
		selected.model = candidates[0]
		spec, e := prepareImageRequest(prompt, selected)
		if e != nil {
			return nil, e
		}
		endpoint := ""
		if root != "" {
			endpoint = root + "/images/" + operation
		}
		return map[string]any{"dry_run": true, "network_used": false, "model_selection_provisional": true, "selection_note": "Offline plan only; execution checks the API model list before submitting.", "endpoint": endpoint, "configuration_required": rootErr != nil, "model_candidates": candidates, "payload": spec.payload, "fields": spec.fields, "images": inputs, "mask": mask, "outputs": outputPaths(out, args.outDir, prompt, args.n)}, nil
	}
	if rootErr != nil {
		return nil, rootErr
	}
	key, e := apiKey()
	if e != nil {
		return nil, e
	}
	if args.budget == nil {
		args.budget = &retryBudget{limit: args.maxAttempts - 1}
	}
	candidates, e := selectModels(args, root, key, op)
	if e != nil {
		return map[string]any{"ok": false, "state": "not_submitted", "outputs": []string{}, "model_attempts": []any{}, "error": describeError(e)}, e
	}
	var attempts []map[string]any
	var last map[string]any
	for _, model := range candidates {
		chosen := args
		chosen.model = model
		result, err := oneImageTask(prompt, inputs, mask, out, root, key, operation, chosen)
		if err == nil {
			if re := recordModel(root, key, op, model, nil); re != nil {
				fmt.Fprintln(redactedWriter{os.Stderr}, "warning: image succeeded, but availability record could not be saved:", re)
			}
			result["model_attempts"] = append(attempts, map[string]any{"model": model, "ok": true})
			return result, nil
		}
		// Only a rejected submission can authorize trying a different model.
		_ = recordModel(root, key, op, model, err)
		attempts = append(attempts, map[string]any{"model": model, "ok": false, "error": describeError(err)})
		last = result
		if last == nil {
			last = map[string]any{"ok": false, "outputs": []string{}}
		}
		last["model_attempts"] = attempts
		var ae *actionError
		if !errors.As(err, &ae) || !ae.SwitchModel || len(candidates) == 1 {
			return last, err
		}
	}
	return last, &actionError{Kind: "no_available_model", Message: "All selected models rejected this request. Choose another model, explicitly test availability, or retry later."}
}

func oneImageTask(prompt string, inputs []string, mask, out, root, key, operation string, args commonArgs) (map[string]any, error) {
	spec, e := prepareImageRequest(prompt, args)
	if e != nil {
		return nil, e
	}
	paths := outputPaths(out, args.outDir, prompt, args.n)
	release, e := reserveOutputs(paths, args.force)
	if e != nil {
		return nil, e
	}
	defer release()
	kind := "generate"
	if len(inputs) > 0 {
		kind = "edit"
	}
	j, e := newJob(kind, root, key, root+"/images/"+operation, spec.model, args, paths)
	if e != nil {
		return nil, e
	}
	j.AutoOutput = out == "" || args.autoOut
	body, ct, e := imageBody(prompt, inputs, mask, spec)
	if e != nil {
		j.fail(e, "local_error")
		return j.result(), e
	}
	j.ContentType = ct
	// Private recovery data contains the prompt/inputs, never the Authorization header.
	if e = atomicWrite(filepath.Join(j.dir, "request.bin"), body, 0600, true); e != nil {
		return j.result(), e
	}
	j.State = "submitting"
	if e = j.save(); e != nil {
		return j.result(), e
	}
	client := newClient(args.timeout, root, args.budget, j.ID)
	raw, e := submitImage(client, j, key, body, args.maxAttempts)
	if e != nil {
		state := "rejected"
		var ae *actionError
		if errors.As(e, &ae) {
			j.RequestID = ae.RequestID
			j.TaskID = ae.TaskID
			if ae.SubmissionUnknown {
				state = "submission_unknown"
			}
		}
		j.fail(e, state)
		return j.result(), e
	}
	if e = j.persistResponse(raw); e != nil {
		j.fail(e, "response_persistence_failed")
		return j.result(), e
	}
	j.State = "response_saved"
	if e = j.save(); e != nil {
		return j.result(), e
	}
	return finishImage(j, client, raw, spec, args)
}

func decodeImages(raw []byte, client *http.Client, attempts int) ([][]byte, error) {
	var result apiResponse
	if e := json.Unmarshal(raw, &result); e != nil {
		return nil, errors.New("API returned invalid JSON")
	}
	if len(result.Data) == 0 {
		return nil, errors.New("API response contains no image data")
	}
	images := make([][]byte, 0, len(result.Data))
	for _, item := range result.Data {
		var b []byte
		var e error
		if item.B64JSON != "" {
			if len(item.B64JSON) > maxImageBytes*4/3+4 {
				return nil, errors.New("base64 image exceeds 64 MiB")
			}
			b, e = base64.StdEncoding.DecodeString(item.B64JSON)
		} else if item.URL != "" {
			b, e = assetBytes(client, item.URL, attempts)
		} else {
			return nil, errors.New("API image entry contains neither b64_json nor url")
		}
		if e != nil {
			return nil, e
		}
		images = append(images, b)
	}
	return images, nil
}

func finishImage(j *jobState, client *http.Client, raw []byte, spec imageSpec, args commonArgs) (map[string]any, error) {
	images := [][]byte{}
	for i := 0; i < j.N; i++ {
		b, e := os.ReadFile(filepath.Join(j.dir, fmt.Sprintf("image-%d.bin", i+1)))
		if e != nil {
			images = nil
			break
		}
		images = append(images, b)
	}
	var e error
	if len(images) != j.N {
		images, e = decodeImages(raw, client, args.maxAttempts)
	}
	if e == nil && len(images) != j.N {
		e = fmt.Errorf("API returned %d image(s), expected %d", len(images), j.N)
	}
	if e == nil {
		e = validateImages(images)
	}
	if e != nil {
		j.fail(e, "output_validation_or_download_failed")
		return j.result(), e
	}
	if re := recordModelScope(j.Scope, j.Kind, j.Model, nil); re != nil {
		fmt.Fprintln(redactedWriter{os.Stderr}, "warning: could not record successful image response:", re)
	}
	// Persist validated bytes for offline output recovery, including after a URL expires.
	for i, b := range images {
		if e = atomicWrite(filepath.Join(j.dir, fmt.Sprintf("image-%d.bin", i+1)), b, 0600, true); e != nil {
			j.fail(e, "output_persistence_failed")
			return j.result(), e
		}
	}
	if j.AutoOutput {
		var changed []string
		for i, b := range images {
			_, _, format := inspectImage(b)
			ext := "." + format
			if format == "jpeg" {
				ext = ".jpg"
			}
			old := j.Paths[i]
			next := strings.TrimSuffix(old, filepath.Ext(old)) + ext
			if next != old {
				j.Paths[i] = next
				changed = append(changed, next)
			}
		}
		if len(changed) > 0 {
			release, re := reserveOutputs(changed, args.force)
			if re != nil {
				j.fail(re, "output_write_failed")
				return j.result(), re
			}
			defer release()
			if re = j.save(); re != nil {
				return j.result(), re
			}
		}
	}
	attachActual(&spec, images, args.size)
	outputs, e := saveImagesSafely(images, j.Paths, args.force)
	if e != nil {
		j.fail(e, "output_write_failed")
		return j.result(), e
	}
	j.State = "complete"
	_ = os.Remove(filepath.Join(j.dir, "request.bin"))
	j.Error = nil
	j.NextActions = nil
	if e = j.save(); e != nil {
		fmt.Fprintln(redactedWriter{os.Stderr}, "warning: outputs saved, but recovery manifest could not be finalized:", e)
	}
	result := resultJSON(spec, outputs)
	result["ok"] = true
	result["job_id"] = j.ID
	result["job_file"] = filepath.Join(j.dir, "job.json")
	result["model_requested"] = j.Model
	result["model_verified_by_provider"] = false
	// A gateway accepting a model name does not establish which backend model ran.
	var upstream struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &upstream) == nil {
		if upstream.Model != "" {
			result["model_reported_by_provider"] = upstream.Model
		}
	}
	if assets, ok := spec.extra["assets"].([]map[string]any); ok {
		for i := range assets {
			assets[i]["path"] = outputs[i]
		}
	}
	return result, nil
}

func saveImagesSafely(images [][]byte, paths []string, force bool) ([]string, error) {
	if len(images) != len(paths) {
		return nil, errors.New("image/output count mismatch")
	}
	if e := validateImages(images); e != nil {
		return nil, e
	}
	for i, p := range paths {
		_, _, format := inspectImage(images[i])
		ext := strings.ToLower(filepath.Ext(p))
		valid := ext == "."+format || (format == "jpeg" && (ext == ".jpg" || ext == ".jpeg"))
		if !valid {
			return nil, fmt.Errorf("output %s has extension %s but API returned %s. Resume with --out using the correct extension; no new generation is needed", p, ext, format)
		}
	}
	var written []string
	for i, p := range paths {
		if e := atomicWrite(p, images[i], 0644, force); e != nil {
			// Do not remove another process's files or any pre-existing forced output.
			if !force {
				for _, q := range written {
					_ = os.Remove(q)
				}
			}
			return nil, fmt.Errorf("could not publish output %s: %w", p, e)
		}
		written = append(written, p)
	}
	abs := make([]string, len(paths))
	for i, p := range paths {
		abs[i], _ = filepath.Abs(p)
	}
	return abs, nil
}

// Retained wrappers keep source-level integrations compatible with the previous release.
func generationDefaults() commonArgs {
	return commonArgs{size: defaultSize, quality: defaultQuality, n: 1, outDir: defaultOutDir, maxAttempts: 4, timeout: 150 * time.Second}
}
