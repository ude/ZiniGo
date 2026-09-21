package zinio

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Progress reports download progress for one issue.
type Progress struct {
	PagesDone  int
	PagesTotal int
}

// DownloadIssue downloads every page of an issue, decrypts and merges them
// into destDir/<issue.FileName()>, and returns the final path. progress may
// be nil. The context cancels the download between pages.
func (c *Client) DownloadIssue(ctx context.Context, issue Issue, destDir string, progress func(Progress)) (string, error) {
	if progress == nil {
		progress = func(Progress) {}
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", err
	}
	outPath := filepath.Join(destDir, issue.FileName())

	content, err := c.fetchReaderContent(issue.ID)
	if err != nil {
		return "", err
	}
	if len(content.Data.Pages) == 0 {
		return "", fmt.Errorf("no pages found for issue %d", issue.ID)
	}

	// Build deduplicated password list to try: legacy_hash → hash → unencrypted
	seen := map[string]bool{}
	var passwords []string
	for _, pw := range []string{content.Data.Issue.LegacyHash, content.Data.Issue.Hash, ""} {
		if !seen[pw] {
			seen[pw] = true
			passwords = append(passwords, pw)
		}
	}

	pagePrefix := filepath.Join(destDir, strconv.Itoa(issue.ID))
	var pageFiles []string
	cleanup := func() {
		for _, f := range pageFiles {
			os.Remove(f)
		}
	}

	total := len(content.Data.Pages)
	progress(Progress{PagesDone: 0, PagesTotal: total})

	for _, page := range content.Data.Pages {
		if err := ctx.Err(); err != nil {
			cleanup()
			return "", err
		}
		if page.Src == "" {
			continue
		}
		pagePath := pagePrefix + "_" + page.Index + ".pdf"

		var pageErr error
		for attempt := 0; attempt < 2; attempt++ {
			pageErr = c.fetchPage(ctx, page, pagePath, passwords)
			if pageErr == nil || ctx.Err() != nil {
				break
			}
			c.Logf("Page %s failed (attempt %d): %v", page.Index, attempt+1, pageErr)
		}
		if pageErr != nil {
			cleanup()
			return "", fmt.Errorf("page %s: %w", page.Index, pageErr)
		}
		pageFiles = append(pageFiles, pagePath)
		progress(Progress{PagesDone: len(pageFiles), PagesTotal: total})
	}

	if err := mergeIssue(pageFiles, outPath, passwords); err != nil {
		cleanup()
		return "", fmt.Errorf("merge failed: %w", err)
	}
	cleanup()
	return outPath, nil
}

// fetchPage downloads one page PDF (with retries for transient CDN failures)
// and verifies PDFium can open it with one of the password candidates.
func (c *Client) fetchPage(ctx context.Context, page readerPage, pagePath string, passwords []string) error {
	var data []byte
	delays := []time.Duration{0, 5 * time.Second, 15 * time.Second}
	var lastErr error
	for _, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, "GET", page.Src, nil)
		if err != nil {
			return err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		data = body
		lastErr = nil
		break
	}
	if lastErr != nil {
		return fmt.Errorf("download failed: %w", lastErr)
	}

	if err := os.WriteFile(pagePath, data, 0644); err != nil {
		return err
	}

	doc, err := openWithPasswords(pagePath, passwords)
	if err != nil {
		// Keep the failing file for inspection instead of deleting it
		corruptPath := pagePath + ".corrupt.pdf"
		os.Rename(pagePath, corruptPath)
		return fmt.Errorf("PDFium cannot open page: %v (kept at %s)", err, corruptPath)
	}
	closeDoc(doc)
	return nil
}
