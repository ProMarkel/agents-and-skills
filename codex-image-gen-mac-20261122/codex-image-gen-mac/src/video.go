package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func submitVideo(client *http.Client, root, key string, payload map[string]any, attempts int) (map[string]any, string, error) {
	endpoint := root + "/videos"
	if os.Getenv("CODEX_VIDEO_ENDPOINT") == "video/generations" {
		endpoint = root + "/video/generations"
	}
	parsed, _, e := postJSON(client, endpoint, key, payload, attempts)
	// Do not create another job after authentication errors or an uncertain submission.
	// The legacy endpoint is selected explicitly, rather than guessed after an error.
	return parsed, endpoint, e
}

func pollVideoAt(client *http.Client, endpoint, key, id string, timeout time.Duration, attempts int) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	pollContext, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	for time.Now().Before(deadline) {
		raw, e := doRequest(client, func() (*http.Request, error) {
			req, e := http.NewRequest(http.MethodGet, safeTaskPath(endpoint, id), nil)
			if e == nil {
				req.Header.Set("Authorization", "Bearer "+key)
				req.Header.Set("Accept", "application/json")
				req = req.WithContext(pollContext)
			}
			return req, e
		}, attempts)
		if e != nil {
			return nil, e
		}
		var status map[string]any
		if e = json.Unmarshal(raw, &status); e != nil {
			return nil, errors.New("API returned invalid video status JSON")
		}
		switch videoStatus(status) {
		case "completed", "success", "succeed", "succeeded":
			return status, nil
		case "failed", "failure", "fail", "cancelled", "canceled":
			msg := videoErrorMessage(status)
			if msg == "" {
				msg = "video generation failed"
			}
			return status, errors.New(redact(msg))
		}
		wait := 5 * time.Second
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		if wait > 0 {
			time.Sleep(wait)
		}
	}
	return nil, errors.New("video generation wait timed out; resume the same task instead of creating another")
}

func validVideo(b []byte) bool {
	return len(b) >= 12 && (string(b[4:8]) == "ftyp" || bytes.Equal(b[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}))
}
func finishVideo(j *jobState, client *http.Client, key string, timeout time.Duration, attempts int) (map[string]any, error) {
	cached, e := os.ReadFile(filepath.Join(j.dir, "video.bin"))
	if e != nil {
		var status map[string]any
		if raw, re := os.ReadFile(filepath.Join(j.dir, "response.json")); re == nil {
			_ = json.Unmarshal(raw, &status)
		}
		if j.TaskID == "" && videoResultURL(status) == "" {
			if !j.canResumeSubmit() {
				e = &actionError{Kind: "submission_unknown", Message: "Video task id was not received; automatically creating another task could duplicate generation.", SubmissionUnknown: true}
				j.fail(e, "submission_unknown")
				return j.result(), e
			}
			body, re := os.ReadFile(filepath.Join(j.dir, "request.bin"))
			if re != nil {
				return j.result(), re
			}
			var payload map[string]any
			if re = json.Unmarshal(body, &payload); re != nil {
				return j.result(), re
			}
			j.State = "submitting"
			if e = j.save(); e != nil {
				return j.result(), e
			}
			var raw []byte
			status, raw, e = postJSON(client, j.Endpoint, key, payload, attempts)
			if e != nil {
				state := "submission_unknown"
				var ae *actionError
				if errors.As(e, &ae) {
					j.TaskID = ae.TaskID
					j.RequestID = ae.RequestID
					if !ae.SubmissionUnknown {
						state = "rejected"
					}
				}
				j.fail(e, state)
				return j.result(), e
			}
			if e = j.persistResponse(raw); e != nil {
				j.fail(e, "response_persistence_failed")
				return j.result(), e
			}
			j.TaskID = videoTaskID(status)
			if e = j.save(); e != nil {
				return j.result(), e
			}
		}
		if videoStatus(status) != "completed" && videoStatus(status) != "succeeded" && videoStatus(status) != "success" && videoStatus(status) != "succeed" {
			if j.TaskID == "" {
				e = errors.New("API returned neither video task id nor a completed result")
				j.fail(e, "submission_unknown")
				return j.result(), e
			}
			status, e = pollVideoAt(client, j.Endpoint, key, j.TaskID, timeout, attempts)
			if e != nil {
				j.fail(e, "waiting_or_query_failed")
				return j.result(), e
			}
			raw, _ := json.Marshal(status)
			if e = j.persistResponse(raw); e != nil {
				j.fail(e, "response_persistence_failed")
				return j.result(), e
			}
		}
		source := videoResultURL(status)
		if strings.HasSuffix(j.Endpoint, "/videos") && j.TaskID != "" {
			cached, e = doRequest(client, func() (*http.Request, error) {
				req, e := http.NewRequest(http.MethodGet, safeTaskPath(j.Endpoint, j.TaskID)+"/content", nil)
				if e == nil {
					req.Header.Set("Authorization", "Bearer "+key)
				}
				return req, e
			}, attempts)
			if e != nil {
				var ae *actionError
				if source == "" || !errors.As(e, &ae) || (ae.Status != 404 && ae.Status != 405) {
					j.fail(e, "download_failed")
					return j.result(), e
				}
			}
		}
		if len(cached) == 0 && source != "" {
			cached, e = assetBytes(client, source, attempts)
			if e != nil {
				j.fail(e, "download_failed")
				return j.result(), e
			}
		}
		if !validVideo(cached) {
			e = errors.New("video result is not a recognized MP4/WebM file")
			j.fail(e, "output_validation_failed")
			return j.result(), e
		}
		if e = atomicWrite(filepath.Join(j.dir, "video.bin"), cached, 0600, true); e != nil {
			j.fail(e, "output_persistence_failed")
			return j.result(), e
		}
	}
	if !validVideo(cached) {
		e = errors.New("cached video is invalid")
		j.fail(e, "output_validation_failed")
		return j.result(), e
	}
	ext := strings.ToLower(filepath.Ext(j.Paths[0]))
	mp4 := len(cached) >= 8 && string(cached[4:8]) == "ftyp"
	if (mp4 && ext != ".mp4") || (!mp4 && ext != ".webm") {
		e = errors.New("video extension does not match returned container; resume with --out ending in .mp4 or .webm")
		j.fail(e, "output_validation_failed")
		return j.result(), e
	}
	if e = atomicWrite(j.Paths[0], cached, 0644, j.Force); e != nil {
		j.fail(e, "output_write_failed")
		return j.result(), e
	}
	j.State = "complete"
	_ = os.Remove(filepath.Join(j.dir, "request.bin"))
	j.Error = nil
	j.NextActions = nil
	if e = j.save(); e != nil {
		fmt.Fprintln(redactedWriter{os.Stderr}, "warning: output saved but could not finalize job manifest:", e)
	}
	result := j.result()
	result["ok"] = true
	result["outputs"] = j.Paths
	result["status"] = "completed"
	result["requested_size"] = j.Size
	// Container validation does not establish the video's actual pixel dimensions.
	result["actual_size"] = nil
	result["actual_size_note"] = "not inspected; requested dimensions are not verified output dimensions"
	return result, nil
}

func generateVideo(prompt, out, model, size string, seconds int, force, dryRun bool, timeout time.Duration, attempts int) (map[string]any, error) {
	if timeout <= 0 || attempts < 1 || attempts > 4 {
		return nil, errors.New("video requires positive timeout and 1-4 total attempts")
	}
	m, e := resolveVideoModel(model)
	if e != nil {
		return nil, e
	}
	pixels, band, note, e := resolveVideoSize(size)
	if e != nil {
		return nil, e
	}
	if seconds < 1 {
		return nil, errors.New("seconds must be positive")
	}
	dest := outputVideoPath(out, defaultVideoOutDir, prompt)
	payload := map[string]any{"model": m, "prompt": prompt, "size": pixels, "seconds": fmt.Sprint(seconds)}
	if dryRun {
		return map[string]any{"dry_run": true, "network_used": false, "payload": payload, "outputs": []string{dest}, "size": band, "note": note}, nil
	}
	root, e := configuredRoot()
	if e != nil {
		return nil, e
	}
	key, e := apiKey()
	if e != nil {
		return nil, e
	}
	release, e := reserveOutputs([]string{dest}, force)
	if e != nil {
		return nil, e
	}
	defer release()
	endpoint := root + "/videos"
	if os.Getenv("CODEX_VIDEO_ENDPOINT") == "video/generations" {
		endpoint = root + "/video/generations"
	}
	args := commonArgs{size: size, n: 1, force: force}
	j, e := newJob("video", root, key, endpoint, m, args, []string{dest})
	if e != nil {
		return nil, e
	}
	b, _ := json.Marshal(payload)
	if e = atomicWrite(filepath.Join(j.dir, "request.bin"), b, 0600, true); e != nil {
		return j.result(), e
	}
	j.State = "submitting"
	if e = j.save(); e != nil {
		return j.result(), e
	}
	client := newClient(120*time.Second, root, &retryBudget{limit: attempts - 1}, j.ID)
	submitted, raw, e := postJSON(client, endpoint, key, payload, attempts)
	if e != nil {
		state := "rejected"
		var ae *actionError
		if errors.As(e, &ae) {
			j.TaskID = ae.TaskID
			j.RequestID = ae.RequestID
			if ae.SubmissionUnknown {
				state = "submission_unknown"
			}
		}
		j.fail(e, state)
		return j.result(), e
	}
	j.TaskID = videoTaskID(submitted)
	if e = j.persistResponse(raw); e != nil {
		j.fail(e, "response_persistence_failed")
		return j.result(), e
	}
	j.State = "submitted"
	if e = j.save(); e != nil {
		return j.result(), e
	}
	result, e := finishVideo(j, client, key, timeout, attempts)
	if result != nil {
		result["size"] = band
		result["resolved_size"] = pixels
		if note != "" {
			result["note"] = note
		}
	}
	return result, e
}
