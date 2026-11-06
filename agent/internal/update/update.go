// Package update implements agent self-upgrade. The agent pulls a manifest and
// a signed-by-checksum tarball from an artifact store, replaces its own binary
// and re-executes itself (works under nohup and systemd).
//
// Trust model: the manifest and tarball are fetched over the same channel and
// the checksum comes from the manifest, so this only protects against
// corruption, not against an attacker who can write to the artifact store.
// Keep the bucket write credentials private and prefer an internal network.
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	manifestName    = "manifest.json"
	maxManifestSize = 1 << 20
	maxArchiveSize  = 256 << 20
	maxBinarySize   = 64 << 20
)

// Manifest describes the latest published agent build.
type Manifest struct {
	Version     string `json:"version"`
	File        string `json:"file"`
	SHA256      string `json:"sha256"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
}

// CheckResult is returned by Check.
type CheckResult struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes,omitempty"`
	File      string `json:"file,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// ApplyResult is returned by Apply.
type ApplyResult struct {
	Updated    bool   `json:"updated"`
	From       string `json:"from"`
	To         string `json:"to"`
	Restarting bool   `json:"restarting"`
	Reason     string `json:"reason,omitempty"`

	// Exe is the path that should be re-executed; not serialized.
	Exe string `json:"-"`
}

// Manager coordinates update checks and applies them one at a time.
type Manager struct {
	baseURL string
	current string
	client  *http.Client

	mu sync.Mutex
}

func New(baseURL, current string) *Manager {
	return &Manager{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		current: current,
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (m *Manager) Enabled() bool { return m.baseURL != "" }

func (m *Manager) metaURL() string {
	return m.baseURL + "/" + manifestName
}

func (m *Manager) fetchManifest(ctx context.Context) (*Manifest, error) {
	if !m.Enabled() {
		return nil, errors.New("self-update is not configured (update_base_url is empty)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.metaURL(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch manifest: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestSize))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var man Manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if man.Version == "" || man.File == "" {
		return nil, errors.New("manifest is missing version or file")
	}
	if len(man.SHA256) != 64 {
		return nil, errors.New("manifest has an invalid sha256")
	}
	if _, err := hex.DecodeString(man.SHA256); err != nil {
		return nil, errors.New("manifest has an invalid sha256")
	}
	return &man, nil
}

// Check compares the running version with the published manifest.
func (m *Manager) Check(ctx context.Context) (*CheckResult, error) {
	man, err := m.fetchManifest(ctx)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		Current:   m.current,
		Latest:    man.Version,
		Available: Compare(man.Version, m.current) > 0,
		Notes:     man.Notes,
		File:      man.File,
		SHA256:    man.SHA256,
	}, nil
}

// Apply downloads, verifies and installs the newest published build. It does
// not restart the process; the caller decides when to re-exec.
func (m *Manager) Apply(ctx context.Context) (*ApplyResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if runtime.GOOS != "linux" {
		return nil, errors.New("self-update is only supported on linux")
	}
	man, err := m.fetchManifest(ctx)
	if err != nil {
		return nil, err
	}
	if Compare(man.Version, m.current) <= 0 {
		return &ApplyResult{
			Updated: false, From: m.current, To: man.Version,
			Reason: "already up to date",
		}, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate executable: %w", err)
	}

	archive, sum, err := m.download(ctx, man)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archive)

	if !strings.EqualFold(sum, man.SHA256) {
		return nil, fmt.Errorf("sha256 mismatch: got %s want %s", sum, man.SHA256)
	}

	newPath := exe + ".new"
	_ = os.Remove(newPath)
	if err := extractBinary(archive, newPath); err != nil {
		_ = os.Remove(newPath)
		return nil, err
	}

	oldPath := exe + ".old"
	_ = os.Remove(oldPath)
	if err := os.Rename(exe, oldPath); err != nil {
		_ = os.Remove(newPath)
		return nil, fmt.Errorf("replace binary: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(oldPath, exe)
		_ = os.Remove(newPath)
		return nil, fmt.Errorf("install new binary: %w", err)
	}

	return &ApplyResult{
		Updated: true, From: m.current, To: man.Version,
		Restarting: true, Exe: exe,
	}, nil
}

func (m *Manager) download(ctx context.Context, man *Manifest) (string, string, error) {
	src := m.baseURL + "/" + url.PathEscape(path.Base(man.File))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download package: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download package: http %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "linux-ops-agent-update-*.tar.gz")
	if err != nil {
		return "", "", err
	}
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), io.LimitReader(resp.Body, maxArchiveSize+1))
	closeErr := tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", "", fmt.Errorf("download package: %w", err)
	}
	if closeErr != nil {
		os.Remove(tmp.Name())
		return "", "", closeErr
	}
	if n > maxArchiveSize {
		os.Remove(tmp.Name())
		return "", "", errors.New("package exceeds size limit")
	}
	return tmp.Name(), hex.EncodeToString(hasher.Sum(nil)), nil
}

func extractBinary(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != "agent" {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, maxBinarySize)); err != nil {
			out.Close()
			return fmt.Errorf("extract binary: %w", err)
		}
		if err := out.Sync(); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
	return errors.New("agent binary not found in archive")
}

// Compare compares dotted numeric versions (e.g. "1.2.3"). Pre-release and
// build suffixes are ignored; missing segments count as zero.
func Compare(a, b string) int {
	pa, pb := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(s string) [3]int {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		if i >= 3 {
			break
		}
		n, _ := strconv.Atoi(part)
		out[i] = n
	}
	return out
}
