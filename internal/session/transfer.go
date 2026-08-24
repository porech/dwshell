package session

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
)

// Download opens a file transfer from an app (e.g. "filesystem") and returns the
// response body stream plus its length (-1 if unknown). fullPath is the absolute
// remote path. Transfers authenticate with the _sk query parameter (regardless
// of customHeaders), matching the browser client.
func (s *Session) Download(ctx context.Context, module, fullPath string) (io.ReadCloser, int64, error) {
	u, err := s.transferURL(module, "download", fullPath)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("download: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, 0, fmt.Errorf("download %s: HTTP %d", fullPath, resp.StatusCode)
	}
	return resp.Body, resp.ContentLength, nil
}

// Upload streams r to the remote fullPath via a multipart POST (field "UPFile"),
// matching the browser client's upload transfer.
func (s *Session) Upload(ctx context.Context, module, fullPath string, r io.Reader) error {
	u, err := s.transferURL(module, "upload", fullPath)
	if err != nil {
		return err
	}

	// Buffer the multipart body so the request carries a Content-Length; the
	// node's upstream rejects chunked uploads (502).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("UPFile", path.Base(fullPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, r); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("upload: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload %s: HTTP %d: %s", fullPath, resp.StatusCode, string(body))
	}
	// The transfer endpoint replies with the framed status; a leading 'E' or 'D'
	// signals failure.
	if len(body) > 0 && (body[0] == 'E' || body[0] == 'D') {
		return fmt.Errorf("upload %s failed: %s", fullPath, trimStatus(string(body)))
	}
	return nil
}

// transferURL builds and signs a transfer URL (_sk query parameter).
func (s *Session) transferURL(module, request, fullPath string) (string, error) {
	key, err := s.signKey.NextSessionKey()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s?module=%s&request=%s&path=%s&key=K1&_sk=%s",
		s.commandURL, url.QueryEscape(module), request,
		url.QueryEscape(fullPath), url.QueryEscape(key)), nil
}
