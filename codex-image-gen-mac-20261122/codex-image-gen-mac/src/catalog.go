package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const catalogTTL = 5 * time.Minute

var catalogMu sync.Mutex

type modelCatalog struct {
	CheckedAt   string   `json:"checked_at"`
	IDs         []string `json:"ids"`
	Images      []string `json:"images"`
	Videos      []string `json:"videos"`
	Unsupported []string `json:"unsupported_priority_models"`
}

func containsModel(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
func catalogOrder(images []string) []string {
	ordered := []string{}
	for _, id := range imagePriority {
		if containsModel(images, id) {
			ordered = append(ordered, id)
		}
	}
	for _, id := range images {
		if !containsModel(ordered, id) {
			ordered = append(ordered, id)
		}
	}
	return ordered
}
func catalogPath(scope string) string { return filepath.Join(stateDir(), "catalogs", scope+".json") }
func readCatalog(scope string) (modelCatalog, error) {
	var c modelCatalog
	b, e := os.ReadFile(catalogPath(scope))
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, e
	}
	if c.IDs == nil {
		return c, errors.New("invalid cached catalog")
	}
	if _, e = time.Parse(time.RFC3339Nano, c.CheckedAt); e != nil {
		return c, e
	}
	return c, nil
}
func (c modelCatalog) fresh() bool {
	t, e := time.Parse(time.RFC3339Nano, c.CheckedAt)
	age := time.Since(t)
	return e == nil && age >= 0 && age < catalogTTL
}
func parseCatalog(raw []byte) (modelCatalog, error) {
	var p struct {
		Data []struct {
			ID               string   `json:"id"`
			OutputModalities []string `json:"output_modalities"`
		} `json:"data"`
	}
	if e := json.Unmarshal(raw, &p); e != nil {
		return modelCatalog{}, fmt.Errorf("could not parse model list: %w", e)
	}
	if p.Data == nil {
		return modelCatalog{}, errors.New("model response is missing the data array")
	}
	c := modelCatalog{CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), IDs: []string{}, Images: []string{}, Videos: []string{}, Unsupported: []string{}}
	for _, item := range p.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			return modelCatalog{}, errors.New("model response contains an entry without a nonempty id")
		}
		if containsModel(c.IDs, id) {
			continue
		}
		c.IDs = append(c.IDs, id)
		if isProbablyVideoModel(id) {
			c.Videos = append(c.Videos, id)
		} else if isProbablyImageModel(id) || containsModel(item.OutputModalities, "image") {
			c.Images = append(c.Images, id)
		}
	}
	for _, id := range imagePriority {
		if !containsModel(c.IDs, id) {
			c.Unsupported = append(c.Unsupported, id)
		}
	}
	return c, nil
}
func getCatalog(root, key string, timeout time.Duration, budget *retryBudget, attempts int, force bool) (modelCatalog, error) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	scope := accountScope(root, key)
	if !force {
		if c, e := readCatalog(scope); e == nil && c.fresh() {
			return c, nil
		}
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if attempts < 1 {
		attempts = 1
	}
	client := newClient(timeout, root, budget, randomID())
	raw, e := doRequest(client, func() (*http.Request, error) {
		r, e := http.NewRequest(http.MethodGet, root+"/models", nil)
		if e == nil {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		return r, e
	}, attempts)
	if e != nil {
		return modelCatalog{}, fmt.Errorf("model catalog refresh failed; no generation submitted and no unsupported marks changed: %w", e)
	}
	c, e := parseCatalog(raw)
	if e != nil {
		return modelCatalog{}, e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return modelCatalog{}, e
	}
	if e = atomicWrite(catalogPath(scope), b, 0600, true); e != nil {
		return modelCatalog{}, fmt.Errorf("could not save model catalog: %w", e)
	}
	return c, nil
}
