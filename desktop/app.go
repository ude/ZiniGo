package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"ZiniGo/zinio"
)

// Config is persisted in the OS user-config directory.
type Config struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Fingerprint string `json:"fingerprint"`
	DownloadDir string `json:"downloadDir"`
}

// App is the Wails-bound backend.
type App struct {
	ctx    context.Context
	client *zinio.Client
	cfg    Config

	mu      sync.Mutex
	cancels map[int]context.CancelFunc
}

func NewApp() *App {
	return &App{cancels: map[int]context.CancelFunc{}}
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ZiniGo", "config.json")
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	if data, err := os.ReadFile(configPath()); err == nil {
		json.Unmarshal(data, &a.cfg)
	}
	// Reuse the CLI's default fingerprint: Zinio registers every new
	// fingerprint as a device and returns 403 once the account hits its
	// device limit, so generating a random one per install locks users out.
	if a.cfg.Fingerprint == "" {
		a.cfg.Fingerprint = "abcd123"
	}
	if a.cfg.DownloadDir == "" {
		home, _ := os.UserHomeDir()
		a.cfg.DownloadDir = filepath.Join(home, "Downloads", "ZiniGo")
	}

	if err := zinio.InitPDF(); err != nil {
		runtime.LogErrorf(ctx, "PDFium init failed: %v", err)
	}
}

func (a *App) shutdown(ctx context.Context) {
	zinio.ClosePDF()
}

func (a *App) saveConfig() {
	path := configPath()
	os.MkdirAll(filepath.Dir(path), 0700)
	data, _ := json.MarshalIndent(a.cfg, "", "  ")
	os.WriteFile(path, data, 0600)
}

// UIConfig is what the frontend sees on startup.
type UIConfig struct {
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
	DownloadDir string `json:"downloadDir"`
}

func (a *App) GetConfig() UIConfig {
	return UIConfig{
		Username:    a.cfg.Username,
		HasPassword: a.cfg.Password != "",
		DownloadDir: a.cfg.DownloadDir,
	}
}

// Login authenticates against Zinio. An empty password reuses the saved one.
func (a *App) Login(username, password string, remember bool) (string, error) {
	if password == "" && username == a.cfg.Username {
		password = a.cfg.Password
	}
	client, err := zinio.NewClient(username, password, a.cfg.Fingerprint, 101)
	if err != nil {
		return "", err
	}
	client.Logf = func(format string, args ...interface{}) {
		runtime.LogInfof(a.ctx, format, args...)
	}
	email, err := client.Login()
	if err != nil {
		return "", err
	}
	a.client = client
	if remember {
		a.cfg.Username = username
		a.cfg.Password = password
	} else {
		a.cfg.Username = username
		a.cfg.Password = ""
	}
	a.saveConfig()
	return email, nil
}

// IssueVM is one library entry for the UI.
type IssueVM struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Publication string `json:"publication"`
	CoverURL    string `json:"coverUrl"`
	Downloaded  bool   `json:"downloaded"`
	Path        string `json:"path"`
}

func (a *App) ListIssues() ([]IssueVM, error) {
	if a.client == nil {
		return nil, fmt.Errorf("not logged in")
	}
	issues, err := a.client.Issues()
	if err != nil {
		return nil, err
	}
	var out []IssueVM
	for _, is := range issues {
		path := filepath.Join(a.cfg.DownloadDir, is.FileName())
		_, statErr := os.Stat(path)
		out = append(out, IssueVM{
			ID:          is.ID,
			Name:        is.Name,
			Publication: is.PublicationName,
			CoverURL:    is.CoverURL,
			Downloaded:  statErr == nil,
			Path:        path,
		})
	}
	return out, nil
}

type progressEvent struct {
	ID    int    `json:"id"`
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

// Download starts an asynchronous download; progress arrives via events.
func (a *App) Download(id int, name, publication string) error {
	if a.client == nil {
		return fmt.Errorf("not logged in")
	}
	a.mu.Lock()
	if _, running := a.cancels[id]; running {
		a.mu.Unlock()
		return fmt.Errorf("download already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancels[id] = cancel
	a.mu.Unlock()

	issue := zinio.Issue{ID: id, Name: name, PublicationName: publication}

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.cancels, id)
			a.mu.Unlock()
		}()
		path, err := a.client.DownloadIssue(ctx, issue, a.cfg.DownloadDir, func(p zinio.Progress) {
			runtime.EventsEmit(a.ctx, "download:progress", progressEvent{ID: id, Done: p.PagesDone, Total: p.PagesTotal})
		})
		if err != nil {
			runtime.EventsEmit(a.ctx, "download:error", progressEvent{ID: id, Error: err.Error()})
			return
		}
		runtime.EventsEmit(a.ctx, "download:done", progressEvent{ID: id, Path: path})
	}()
	return nil
}

// Cancel aborts a running download.
func (a *App) Cancel(id int) {
	a.mu.Lock()
	if cancel, ok := a.cancels[id]; ok {
		cancel()
	}
	a.mu.Unlock()
}

// ChooseDownloadDir opens a directory picker and persists the choice.
func (a *App) ChooseDownloadDir() (string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title:            "Carpeta de descargas",
		DefaultDirectory: a.cfg.DownloadDir,
	})
	if err != nil || dir == "" {
		return a.cfg.DownloadDir, err
	}
	a.cfg.DownloadDir = dir
	a.saveConfig()
	return dir, nil
}

// OpenDownloadDir reveals the download folder in Finder/Explorer.
func (a *App) OpenDownloadDir() {
	os.MkdirAll(a.cfg.DownloadDir, 0755)
	runtime.BrowserOpenURL(a.ctx, "file://"+a.cfg.DownloadDir)
}

// OpenFile opens a downloaded magazine with the system PDF viewer.
func (a *App) OpenFile(path string) {
	runtime.BrowserOpenURL(a.ctx, "file://"+path)
}
