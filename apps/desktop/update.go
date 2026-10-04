package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Update check v1 = notification only. A release build asks GitHub for the
// latest release once shortly after launch and then daily, and tells the
// frontend when something newer exists. No download, no self-replace.
//
// Privacy: the only request is a conditional GET of the public releases
// endpoint carrying a User-Agent; no query parameters, no identifiers.

// updateAvailableEvent is emitted to the frontend (runtime.EventsOn) with an
// UpdateInfo payload when a newer release than `version` exists.
const updateAvailableEvent = "update-available"

const (
	defaultUpdateURL   = "https://api.github.com/repos/GuilhermeVozniak/calendium/releases/latest"
	updateCheckTimeout = 5 * time.Second
	updateInitialDelay = 10 * time.Second // never delays launch
	updateInterval     = 24 * time.Hour
	updateCacheFile    = "update-check.json"
	updateBodyLimit    = 1 << 20
)

// UpdateInfo is the update-available payload and the GetUpdateStatus result.
type UpdateInfo struct {
	Available bool   `json:"available"`
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
}

// updateCache memoizes the last successful check so the next request can be
// conditional (ETag → 304) and still report the release it described.
type updateCache struct {
	ETag      string    `json:"etag"`
	TagName   string    `json:"tagName"`
	HTMLURL   string    `json:"htmlUrl"`
	CheckedAt time.Time `json:"checkedAt"`
}

type semver struct {
	major, minor, patch int
	pre                 string
}

// parseSemver accepts X.Y.Z or X.Y.Z-pre (a leading "v" is tolerated because
// GitHub tags are vX.Y.Z); build metadata after "+" is dropped; leading zeros
// are rejected.
func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v semver
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre = s[i+1:]
		s = s[:i]
		if v.pre == "" {
			return semver{}, false
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var nums [3]int
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, false
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, true
}

// newer reports whether latest is strictly newer than current: numeric
// major.minor.patch compare; at equal numbers a release beats a prerelease
// and two prereleases compare lexically.
func newer(latest, current semver) bool {
	if latest.major != current.major {
		return latest.major > current.major
	}
	if latest.minor != current.minor {
		return latest.minor > current.minor
	}
	if latest.patch != current.patch {
		return latest.patch > current.patch
	}
	if latest.pre == "" && current.pre != "" {
		return true
	}
	if latest.pre != "" && current.pre == "" {
		return false
	}
	return latest.pre > current.pre
}

// isReleaseVersion is true only for a plain X.Y.Z, so "dev" and
// 0.0.0-dev.<sha> builds never nag (or request anything).
func isReleaseVersion(v string) bool {
	parsed, ok := parseSemver(v)
	return ok && parsed.pre == ""
}

// resolveUpdateURL applies CALENDIUM_UPDATE_URL: empty → GitHub, "off" →
// disabled (empty string), anything else → that URL.
func resolveUpdateURL(env string) string {
	env = strings.TrimSpace(env)
	switch env {
	case "":
		return defaultUpdateURL
	case "off":
		return ""
	default:
		return env
	}
}

func defaultUpdateCachePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Calendium", updateCacheFile)
}

type updateChecker struct {
	version   string
	url       string // "" disables
	hc        *http.Client
	cachePath string // "" disables the on-disk cache
	now       func() time.Time

	mu     sync.Mutex
	emit   func(UpdateInfo)
	latest UpdateInfo
}

func newUpdateChecker(version string) *updateChecker {
	return &updateChecker{
		version:   version,
		url:       resolveUpdateURL(os.Getenv("CALENDIUM_UPDATE_URL")),
		hc:        &http.Client{Timeout: updateCheckTimeout},
		cachePath: defaultUpdateCachePath(),
		now:       time.Now,
		latest:    UpdateInfo{Current: version},
	}
}

// setEmit wires the sink that receives a newer release exactly once per
// version seen (runtime.EventsEmit in production; a recorder in tests).
func (c *updateChecker) setEmit(emit func(UpdateInfo)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.emit = emit
}

// enabled: only release builds with a URL ever talk to the network.
func (c *updateChecker) enabled() bool {
	return c.url != "" && isReleaseVersion(c.version)
}

// status returns the last result (zero-value Available=false before any check).
func (c *updateChecker) status() UpdateInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// check performs one conditional GET and returns the resulting status. Errors
// leave the previous status untouched and never emit.
func (c *updateChecker) check(ctx context.Context) (UpdateInfo, error) {
	if !c.enabled() {
		return c.status(), nil
	}
	cached := c.readCache()
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return c.status(), err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Calendium-Desktop/"+c.version)
	if cached.ETag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return c.status(), err
	}
	defer func() { _ = resp.Body.Close() }()

	var rel githubRelease
	switch resp.StatusCode {
	case http.StatusNotModified:
		if cached.TagName == "" {
			return c.status(), errors.New("update check: 304 without a cached release")
		}
		rel = githubRelease{TagName: cached.TagName, HTMLURL: cached.HTMLURL}
	case http.StatusOK:
		if err := json.NewDecoder(io.LimitReader(resp.Body, updateBodyLimit)).Decode(&rel); err != nil {
			return c.status(), fmt.Errorf("update check: decode: %w", err)
		}
		if rel.Draft || rel.Prerelease {
			return c.status(), nil
		}
		parsed, ok := parseSemver(rel.TagName)
		if !ok {
			return c.status(), fmt.Errorf("update check: tag %q is not semver", rel.TagName)
		}
		if parsed.pre != "" {
			// A vX.Y.Z-rc.N tag published without the prerelease flag is
			// still a prerelease: never offered, never cached.
			return c.status(), nil
		}
		c.writeCache(updateCache{ETag: resp.Header.Get("ETag"), TagName: rel.TagName, HTMLURL: rel.HTMLURL, CheckedAt: c.now()})
	default:
		return c.status(), fmt.Errorf("update check: unexpected status %d", resp.StatusCode)
	}
	return c.apply(rel), nil
}

// apply compares a release against the running version, records the result
// and emits when it is newer and not already announced.
func (c *updateChecker) apply(rel githubRelease) UpdateInfo {
	latest, ok := parseSemver(rel.TagName)
	current, _ := parseSemver(c.version)
	info := UpdateInfo{Current: c.version, Latest: strings.TrimPrefix(rel.TagName, "v"), URL: rel.HTMLURL}
	info.Available = ok && latest.pre == "" && newer(latest, current)

	c.mu.Lock()
	prev := c.latest
	c.latest = info
	emit := c.emit
	c.mu.Unlock()

	if info.Available && emit != nil && (!prev.Available || prev.Latest != info.Latest) {
		emit(info)
	}
	return info
}

func (c *updateChecker) readCache() updateCache {
	if c.cachePath == "" {
		return updateCache{}
	}
	b, err := os.ReadFile(c.cachePath)
	if err != nil {
		return updateCache{}
	}
	var cached updateCache
	if err := json.Unmarshal(b, &cached); err != nil {
		return updateCache{} // corrupt → behave as if there were no cache
	}
	return cached
}

func (c *updateChecker) writeCache(cache updateCache) {
	if c.cachePath == "" {
		return
	}
	b, err := json.Marshal(cache)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.cachePath), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(c.cachePath, b, 0o600)
}

// run blocks until ctx is done: first check after `initial`, then every
// `every`. Errors are logged; the loop never panics the host.
func (c *updateChecker) run(ctx context.Context, initial, every time.Duration) {
	if !c.enabled() {
		return
	}
	timer := time.NewTimer(initial)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	if _, err := c.check(ctx); err != nil {
		log.Printf("update check: %v", err)
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.check(ctx); err != nil {
				log.Printf("update check: %v", err)
			}
		}
	}
}
