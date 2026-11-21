package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"time"
)

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(redactedWriter{os.Stderr})
	fs.BoolVar(&useDefaultURL, "use-default-url", useDefaultURL, "explicitly use https://apinebula.com only when CODEX_API_URL is missing")
	return fs
}
func settingValue(name string) string { v, _ := setting(name); return v }

func listModels(argv []string) error {
	fs := newFlagSet("models")
	all := fs.Bool("all", false, "include non-image/video models")
	check := fs.Bool("check-availability", false, "explicitly run one paid image generation per selected model")
	var selected stringList
	fs.Var(&selected, "model", "limit availability checks to this image model; repeat as needed")
	outDir := fs.String("out-dir", defaultOutDir, "directory for test images")
	timeout := fs.Duration("timeout", 150*time.Second, "per-request timeout, e.g. 150s")
	if e := fs.Parse(argv); e != nil {
		return e
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if len(selected) > 0 && !*check {
		return errors.New("--model on models requires --check-availability")
	}
	root, e := configuredRoot()
	if e != nil {
		return e
	}
	key, e := apiKey()
	if e != nil {
		return e
	}
	catalog, e := getCatalog(root, key, 30*time.Second, &retryBudget{limit: 3}, 4, true)
	if e != nil {
		return e
	}
	images, videos, others := catalog.Images, catalog.Videos, []string{}
	for _, id := range catalog.IDs {
		if !containsModel(images, id) && !containsModel(videos, id) {
			others = append(others, id)
		}
	}
	records := catalogRecords(catalog, root, key)
	result := map[string]any{"ok": true, "images": images, "videos": videos, "availability": records, "priority": imagePriority, "selection_order": catalogOrder(images), "unsupported_priority_models": catalog.Unsupported, "catalog_checked_at": catalog.CheckedAt, "catalog_ttl_seconds": int(catalogTTL / time.Second), "note": "Listed means advertised, not verified working. Absent models are unsupported for this API/credential catalog observation; historical successes do not override absence. Availability is operation-specific."}
	if *all {
		result["other"] = others
	}
	if !*check {
		printJSON(result)
		return nil
	}
	candidates := images
	if len(selected) > 0 {
		candidates = nil
		for _, m := range selected {
			id, e := resolveModel(m)
			if e != nil {
				return e
			}
			candidates = append(candidates, id)
		}
	}
	// Explicit probe mode is the only command that enumerates models with generation calls.
	checks := []map[string]any{}
	failures := 0
	budget := &retryBudget{limit: 0}
	for _, m := range candidates {
		args := generationDefaults()
		args.model = m
		args.maxAttempts = 1
		args.budget = budget
		args.timeout = *timeout
		args.outDir = *outDir
		data, err := generate("A simple blue ceramic sphere on a plain white background, soft studio lighting, no text.", "", args)
		item := map[string]any{"model": m, "ok": err == nil, "result": data, "availability": loadModelRecord(accountScope(root, key), "generate", m)}
		if err != nil {
			item["error"] = describeError(err)
			failures++
		}
		checks = append(checks, item)
		// A submission with uncertain acceptance stops even an explicit sweep.
		var ae *actionError
		if errors.As(err, &ae) && (ae.SubmissionUnknown || ae.Status == 401) {
			result["stopped_early"] = true
			break
		}
	}
	result["checks"] = checks
	result["availability"] = catalogRecords(catalog, root, key)
	result["tested"] = len(checks)
	result["failed"] = failures
	result["ok"] = failures == 0
	result["output_directory"], _ = filepath.Abs(*outDir)
	printJSON(result)
	if failures > 0 {
		return errors.New("one or more availability checks failed; see per-model results")
	}
	return nil
}

func catalogRecords(c modelCatalog, root, key string) []modelRecord {
	ids := append([]string{}, imagePriority...)
	for _, id := range c.Images {
		if !containsModel(ids, id) {
			ids = append(ids, id)
		}
	}
	records := []modelRecord{}
	for _, id := range ids {
		records = append(records, loadModelRecord(accountScope(root, key), "generate", id))
	}
	return records
}
