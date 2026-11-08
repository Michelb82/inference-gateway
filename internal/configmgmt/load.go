package configmgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const defaultFetchTimeout = 15 * time.Second

// Parse decodes and validates a manifest document.
func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := Validate(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// LoadFile reads, parses, and validates a manifest from disk.
func LoadFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest %s: %w", path, err)
	}
	return Parse(data)
}

// FetchURL GETs a remote manifest, then parses and validates it.
func FetchURL(ctx context.Context, client *http.Client, url string) (Manifest, error) {
	return fetchURL(ctx, client, url, InstanceIdentity{})
}

func fetchURL(ctx context.Context, client *http.Client, url string, identity InstanceIdentity) (Manifest, error) {
	if client == nil {
		client = &http.Client{Timeout: defaultFetchTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("create manifest request: %w", err)
	}
	if identity.ID != "" {
		req.Header.Set("X-Gateway-Instance-ID", identity.ID)
	}
	if identity.Hostname != "" {
		req.Header.Set("X-Gateway-Hostname", identity.Hostname)
	}

	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Manifest{}, fmt.Errorf("fetch manifest: unexpected status %d", resp.StatusCode)
	}
	return Parse(body)
}
