// Package zinio implements a client for Zinio's BFF API and a PDFium-based
// pipeline that turns the per-page encrypted PDFs Zinio serves into a single
// DRM-free magazine PDF. It is shared by the CLI (ZiniGo/) and the desktop
// app (desktop/).
package zinio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/icza/gox/stringsx"
)

const zinioBase = "https://www.zinio.com"

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// Issue is one purchased magazine issue from the user's library.
type Issue struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	PublicationName string `json:"publicationName"`
	CoverURL        string `json:"coverUrl"`
	LegacyContent   bool   `json:"legacyContent"`
}

// FileName is the canonical output file name for this issue.
func (i Issue) FileName() string {
	return sanitize(i.PublicationName) + " - " + sanitize(i.Name) + ".pdf"
}

// Client holds HTTP session state and credentials for re-authentication.
type Client struct {
	http        *http.Client
	username    string
	password    string
	fingerprint string
	newsstandID int
	userID      string

	// Logf receives diagnostic messages; defaults to a no-op.
	Logf func(format string, args ...interface{})
}

func NewClient(username, password, fingerprint string, newsstandID int) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}
	return &Client{
		http: &http.Client{
			Jar:     jar,
			Timeout: 30 * time.Second,
		},
		username:    username,
		password:    password,
		fingerprint: fingerprint,
		newsstandID: newsstandID,
		Logf:        func(string, ...interface{}) {},
	}, nil
}

type loginResponse struct {
	Status bool `json:"status"`
	Data   struct {
		User struct {
			UserIDString string `json:"user_id_string"`
			Email        string `json:"email"`
		} `json:"user"`
	} `json:"data"`
}

// Login authenticates the client and returns the account email.
func (c *Client) Login() (string, error) {
	resp, err := c.doLogin()
	if err != nil {
		return "", err
	}
	if !resp.Status || resp.Data.User.UserIDString == "" {
		return "", fmt.Errorf("login failed: server returned status=false")
	}
	c.userID = resp.Data.User.UserIDString
	return resp.Data.User.Email, nil
}

func (c *Client) doLogin() (loginResponse, error) {
	payload := map[string]interface{}{
		"email":    c.username,
		"password": c.password,
		"device": map[string]string{
			"name":        "Windows Chrome",
			"fingerprint": c.fingerprint,
			"device_type": "Desktop",
			"client_type": "Web",
		},
		"newsstand": map[string]interface{}{
			"currency": "USD", "id": c.newsstandID, "country": "US", "name": "United States",
			"cc": "us", "localeCode": "en_US", "userLang": "en_US", "userCountry": "US",
			"userCurrency": "USD",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return loginResponse{}, fmt.Errorf("failed to marshal login payload: %w", err)
	}

	req, err := http.NewRequest("POST", zinioBase+"/api/x7b9q-sync", bytes.NewBuffer(body))
	if err != nil {
		return loginResponse{}, fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", zinioBase)
	req.Header.Set("Referer", zinioBase+"/sign-in")

	resp, err := c.http.Do(req)
	if err != nil {
		return loginResponse{}, fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return loginResponse{}, fmt.Errorf("login returned HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return loginResponse{}, fmt.Errorf("failed to read login response: %w", err)
	}

	var result loginResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return loginResponse{}, fmt.Errorf("failed to parse login response: %w", err)
	}
	return result, nil
}

func (c *Client) relogin() bool {
	delays := []time.Duration{5 * time.Second, 15 * time.Second, 60 * time.Second}
	for attempt, delay := range delays {
		c.Logf("Re-authenticating (attempt %d/%d)...", attempt+1, len(delays))
		if _, err := c.Login(); err == nil {
			return true
		} else {
			c.Logf("Re-login failed: %v — waiting %s before retry", err, delay)
		}
		time.Sleep(delay)
	}
	c.Logf("Re-authentication failed after all attempts")
	return false
}

type libraryResponse struct {
	Data []struct {
		Id          int    `json:"id"`
		Name        string `json:"name"`
		CoverImage  string `json:"cover_image"`
		Publication struct {
			Name          string `json:"name"`
			LegacyContent int    `json:"legacy_content"`
		} `json:"publication"`
	} `json:"data"`
}

// Issues returns the user's full library, newest first.
func (c *Client) Issues() ([]Issue, error) {
	var issues []Issue
	offset := 0
	pageSize := 120
	for {
		page, err := c.fetchLibrary(pageSize, offset)
		if err != nil {
			return nil, err
		}
		if len(page.Data) == 0 {
			break
		}
		for _, it := range page.Data {
			issues = append(issues, Issue{
				ID:              it.Id,
				Name:            it.Name,
				PublicationName: it.Publication.Name,
				CoverURL:        it.CoverImage,
				LegacyContent:   it.Publication.LegacyContent == 1,
			})
		}
		offset += len(page.Data)
		if len(page.Data) < pageSize {
			break
		}
	}
	return issues, nil
}

func (c *Client) fetchLibrary(limit, offset int) (libraryResponse, error) {
	for attempt := 0; attempt < 2; attempt++ {
		u := fmt.Sprintf("%s/api/newsstand/newsstands/%d/users/%s/library-issues?limit=%d&offset=%d&sort=desc",
			zinioBase, c.newsstandID, c.userID, limit, offset)

		data, status, err := c.get(u)
		if err != nil {
			return libraryResponse{}, fmt.Errorf("library request failed: %w", err)
		}
		if status == 401 && attempt == 0 {
			c.Logf("Library 401, re-authenticating...")
			if !c.relogin() {
				return libraryResponse{}, fmt.Errorf("re-authentication failed")
			}
			continue
		}
		if status != http.StatusOK {
			return libraryResponse{}, fmt.Errorf("library returned HTTP %d", status)
		}

		var result libraryResponse
		if err := json.Unmarshal(data, &result); err != nil {
			return libraryResponse{}, fmt.Errorf("failed to parse library response: %w", err)
		}
		return result, nil
	}
	return libraryResponse{}, fmt.Errorf("library fetch failed after re-authentication")
}

type readerContent struct {
	Data struct {
		Issue struct {
			LegacyHash  string `json:"legacy_hash"`
			Hash        string `json:"hash"`
			Publication struct {
				LegacyContent int `json:"legacy_content"`
			} `json:"publication"`
		} `json:"issue"`
		Pages []readerPage `json:"pages"`
	} `json:"data"`
}

type readerPage struct {
	Index string `json:"index"`
	Src   string `json:"src"`
}

func (c *Client) fetchReaderContent(issueID int) (readerContent, error) {
	for attempt := 0; attempt < 2; attempt++ {
		u := fmt.Sprintf("%s/api/reader/content?issue_id=%d&newsstand_id=%d&user_id=%s&format=pdf",
			zinioBase, issueID, c.newsstandID, url.QueryEscape(c.userID))

		data, status, err := c.get(u)
		if err != nil {
			return readerContent{}, fmt.Errorf("reader content fetch failed: %w", err)
		}
		if status == 401 && attempt == 0 {
			c.Logf("Reader 401, re-authenticating...")
			if !c.relogin() {
				return readerContent{}, fmt.Errorf("re-authentication failed")
			}
			continue
		}
		if status != http.StatusOK {
			return readerContent{}, fmt.Errorf("reader returned HTTP %d", status)
		}

		var result readerContent
		if err := json.Unmarshal(data, &result); err != nil {
			return readerContent{}, fmt.Errorf("failed to parse reader response: %w", err)
		}
		if len(result.Data.Pages) == 0 {
			c.Logf("Issue %d: 0 pages — raw response: %s", issueID, string(data[:min(len(data), 300)]))
		}
		return result, nil
	}
	return readerContent{}, fmt.Errorf("reader fetch failed after re-authentication")
}

func (c *Client) get(u string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", zinioBase)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return data, resp.StatusCode, nil
}

var badCharacters = []string{"/", "\\", "<", ">", ":", "\"", "|", "?", "*"}

func sanitize(input string) string {
	temp := input
	for _, badChar := range badCharacters {
		temp = strings.Replace(temp, badChar, "_", -1)
	}
	return stringsx.Clean(temp)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
