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
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	manifestName    = "manifest.json"
	checksumsName   = "SHA256SUMS"
	maxManifestSize = 1 << 20
	maxChecksumSize = 1 << 20
	maxListSize     = 8 << 20
	maxArchiveSize  = 256 << 20
	maxBinarySize   = 64 << 20
	// releasePrefix is the filename convention for published builds:
	//   linux-ops-agent-<version>-linux-<arch>.tar.gz
	// Uploading a file that matches it is all that is needed to publish.
	releasePrefix = "linux-ops-agent-"
)

var releasePattern = regexp.MustCompile(
	`^linux-ops-agent-(\d+\.\d+\.\d+)-linux-([a-z0-9_]+)\.tar\.gz$`)

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
	// Source is "listing" when the version came from scanning the artifact
	// bucket, or "manifest" when manifest.json advertised it (or both agreed).
	Source string `json:"source,omitempty"`
	// Verified reports whether a checksum is known for File. When false the
	// package will be installed without integrity verification (only possible
	// when requireChecksum is disabled).
	Verified bool `json:"verified"`
}

// ApplyResult is returned by Apply.
type ApplyResult struct {
	Updated    bool   `json:"updated"`
	From       string `json:"from"`
	To         string `json:"to"`
	Restarting bool   `json:"restarting"`
	Reason     string `json:"reason,omitempty"`
	Source     string `json:"source,omitempty"`
	Verified   bool   `json:"verified"`

	// Exe is the path that should be re-executed; not serialized.
	Exe string `json:"-"`
}

// release is the resolved newest build, however it was discovered.
type release struct {
	Version string
	File    string
	SHA256  string
	Notes   string
	Source  string
}

// Manager coordinates update checks and applies them one at a time.
type Manager struct {
	baseURL string
	current string
	client  *http.Client
	// requireChecksum rejects releases with no advertised sha256.
	requireChecksum bool

	mu sync.Mutex
}

func New(baseURL, current string, requireChecksum bool) *Manager {
	return &Manager{
		baseURL:         strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		current:         current,
		requireChecksum: requireChecksum,
		client:          &http.Client{Timeout: 5 * time.Minute},
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

// Check compares the running version with the newest published build.
func (m *Manager) Check(ctx context.Context) (*CheckResult, error) {
	rel, err := m.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return &CheckResult{
		Current:   m.current,
		Latest:    rel.Version,
		Available: Compare(rel.Version, m.current) > 0,
		Notes:     rel.Notes,
		File:      rel.File,
		SHA256:    rel.SHA256,
		Source:    rel.Source,
		Verified:  rel.SHA256 != "",
	}, nil
}

// listBucketResult is the subset of the S3 ListObjectsV2 response we need.
type listBucketResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
	} `xml:"Contents"`
}

// listReleases scans the artifact prefix for builds matching the naming
// convention and returns them newest-first. Requires anonymous s3:ListBucket
// on the bucket; a 403 here simply falls back to manifest.json.
func (m *Manager) listReleases(ctx context.Context) ([]release, error) {
	var out []release
	token := ""
	for page := 0; page < 10; page++ {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("max-keys", "1000")
		q.Set("prefix", releasePrefix)
		if token != "" {
			q.Set("continuation-token", token)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			m.baseURL+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := m.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("list releases: %w", err)
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxListSize))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("list releases: http %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, fmt.Errorf("read release list: %w", readErr)
		}
		var lr listBucketResult
		if err := xml.Unmarshal(data, &lr); err != nil {
			return nil, fmt.Errorf("parse release list: %w", err)
		}
		for _, obj := range lr.Contents {
			name := path.Base(obj.Key)
			match := releasePattern.FindStringSubmatch(name)
			if match == nil || match[2] != runtime.GOARCH {
				continue
			}
			out = append(out, release{
				Version: match[1],
				File:    name,
				Source:  "listing",
				Notes:   "discovered by scanning the artifact bucket",
			})
		}
		if !lr.IsTruncated || lr.NextContinuationToken == "" {
			break
		}
		token = lr.NextContinuationToken
	}
	if len(out) == 0 {
		return nil, errors.New("no linux-ops-agent-<version>-linux-" +
			runtime.GOARCH + ".tar.gz found in the artifact bucket")
	}
	return out, nil
}

// lookupChecksum reads SHA256SUMS from the artifact prefix. It is optional:
// when absent the release is installed unverified unless requireChecksum is on.
func (m *Manager) lookupChecksum(ctx context.Context, file string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		m.baseURL+"/"+checksumsName, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: http %d", checksumsName, resp.StatusCode)
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, maxChecksumSize))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if path.Base(name) != file {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != 64 {
			return "", fmt.Errorf("invalid checksum for %s in %s", file, checksumsName)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("invalid checksum for %s in %s", file, checksumsName)
		}
		return sum, nil
	}
	return "", fmt.Errorf("no checksum for %s in %s", file, checksumsName)
}

// resolve finds the newest published build. It scans the bucket for files that
// follow the release naming convention and also consults manifest.json when it
// is present; the highest version wins and a matching manifest supplies the
// checksum and notes. Publishing therefore only requires uploading a correctly
// named tarball — a stale manifest can no longer pin the fleet to an old build.
func (m *Manager) resolve(ctx context.Context) (*release, error) {
	if !m.Enabled() {
		return nil, errors.New("self-update is not configured (update_base_url is empty)")
	}
	var (
		best    *release
		man     *Manifest
		manErr  error
		listErr error
	)
	if got, err := m.fetchManifest(ctx); err != nil {
		manErr = err
	} else {
		man = got
		best = &release{
			Version: man.Version, File: man.File, SHA256: man.SHA256,
			Notes: man.Notes, Source: "manifest",
		}
	}
	if list, err := m.listReleases(ctx); err != nil {
		listErr = err
	} else {
		for _, r := range list {
			switch {
			case best == nil:
				rel := r
				best = &rel
			case Compare(r.Version, best.Version) > 0:
				rel := r
				best = &rel
			}
		}
		if best != nil && best.Source == "listing" && man != nil &&
			Compare(man.Version, best.Version) == 0 {
			// The manifest describes the same build: take its checksum/notes.
			best.Source = "manifest+listing"
			if best.SHA256 == "" {
				best.SHA256 = man.SHA256
			}
			if best.Notes == "" || best.Notes == "discovered by scanning the artifact bucket" {
				best.Notes = man.Notes
			}
		}
	}
	if best == nil {
		if manErr == nil {
			manErr = errors.New("no manifest")
		}
		if listErr == nil {
			listErr = errors.New("no release listed")
		}
		return nil, fmt.Errorf("discover release: %v; %v", manErr, listErr)
	}
	if best.SHA256 == "" {
		if sum, err := m.lookupChecksum(ctx, best.File); err == nil {
			best.SHA256 = sum
		}
	}
	if m.requireChecksum && best.SHA256 == "" {
		return nil, fmt.Errorf(
			"release %s has no checksum (add it to manifest.json or %s)",
			best.File, checksumsName)
	}
	return best, nil
}

// Apply downloads, verifies and installs the newest published build. It does
// not restart the process; the caller decides when to re-exec.
func (m *Manager) Apply(ctx context.Context) (*ApplyResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if runtime.GOOS != "linux" {
		return nil, errors.New("self-update is only supported on linux")
	}
	rel, err := m.resolve(ctx)
	if err != nil {
		return nil, err
	}
	if Compare(rel.Version, m.current) <= 0 {
		return &ApplyResult{
			Updated: false, From: m.current, To: rel.Version,
			Reason: "already up to date",
		}, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate executable: %w", err)
	}

	archive, sum, err := m.download(ctx, rel.File)
	if err != nil {
		return nil, err
	}
	defer os.Remove(archive)

	if rel.SHA256 != "" && !strings.EqualFold(sum, rel.SHA256) {
		return nil, fmt.Errorf("sha256 mismatch: got %s want %s", sum, rel.SHA256)
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
		Updated: true, From: m.current, To: rel.Version,
		Restarting: true, Exe: exe,
		Source: rel.Source, Verified: rel.SHA256 != "",
	}, nil
}

func (m *Manager) download(ctx context.Context, file string) (string, string, error) {
	src := m.baseURL + "/" + url.PathEscape(path.Base(file))
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
