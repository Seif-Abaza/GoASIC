package miners

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

// buildMultipartFile builds a multipart/form-data body containing the file
// at localPath under the given field name.
// Returns the body reader, the Content-Type header value, and any error.
func buildMultipartFile(fieldName, localPath string) (io.Reader, string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return nil, "", fmt.Errorf("firmware: open %q: %w", localPath, err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, "", fmt.Errorf("firmware: read %q: %w", localPath, err)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(fieldName, filepath.Base(localPath))
	if err != nil {
		return nil, "", fmt.Errorf("firmware: create form file: %w", err)
	}
	if _, err = fw.Write(data); err != nil {
		return nil, "", fmt.Errorf("firmware: write form file: %w", err)
	}
	w.Close()
	return &buf, w.FormDataContentType(), nil
}

// downloadToTemp downloads the URL to a temporary file and returns its path.
// The caller is responsible for calling removeTemp on the returned path.
func downloadToTemp(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("firmware: build download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("firmware: download %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("firmware: download %q returned HTTP %d", url, resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "goasic-fw-*.bin")
	if err != nil {
		return "", fmt.Errorf("firmware: create temp file: %w", err)
	}
	defer tmp.Close()

	if _, err = io.Copy(tmp, io.LimitReader(resp.Body, 500*1024*1024)); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("firmware: write temp file: %w", err)
	}
	return tmp.Name(), nil
}

// removeTemp deletes a temporary file, ignoring any error.
func removeTemp(path string) { os.Remove(path) }
