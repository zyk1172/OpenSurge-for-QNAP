package mihomo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"open-mihomo-gateway/internal/config"
)

// FlushDNSCaches clears both resolver and fake-IP caches after a managed DNS
// mapping changes. The two endpoints are independent, so report both failures
// rather than hiding one behind the other.
func FlushDNSCaches(ctx context.Context, cfg config.Config) error {
	client := &http.Client{Timeout: 5 * time.Second}
	var errs []error
	for _, path := range []string{"/cache/dns/flush", "/cache/fakeip/flush"} {
		req, err := newAPIRequest(ctx, cfg, http.MethodPost, path, nil)
		if err != nil {
			errs = append(errs, fmt.Errorf("prepare %s: %w", path, err))
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			errs = append(errs, fmt.Errorf("flush %s: %w", path, err))
			continue
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		closeErr := resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			errs = append(errs, fmt.Errorf("flush %s: mihomo API returned %s", path, resp.Status))
			continue
		}
		if readErr != nil {
			errs = append(errs, fmt.Errorf("flush %s response: %w", path, readErr))
			continue
		}
		if closeErr != nil {
			errs = append(errs, fmt.Errorf("flush %s response: %w", path, closeErr))
		}
	}
	return errors.Join(errs...)
}
