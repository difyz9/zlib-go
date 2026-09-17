package zlib

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/difyz9/zlib-go/internal/fetch"
)

// downloadBook streams a resolved download URL to disk, translating a non-200
// response into this package's wall-aware error taxonomy so a blocked download
// reports as an anti-bot wall rather than a bare status code.
func downloadBook(ctx context.Context, client *http.Client, rawURL, outDir, filename, cookie string, logf func(string, ...any), progress fetch.ProgressReporter) (string, error) {
	path, err := fetch.DownloadToFile(ctx, client, rawURL, outDir, filename, cookie, downloadTimeout, logf, progress)
	if err == nil {
		return path, nil
	}

	var statusErr *fetch.StatusError
	if errors.As(err, &statusErr) {
		if walledStatuses[statusErr.Code] {
			return "", &WalledError{Domain: fetch.HostOf(rawURL), Detail: domainStatus(statusErr.Code)}
		}
		return "", fmt.Errorf("download failed: HTTP %d", statusErr.Code)
	}
	return "", err
}
