// Package update asks GitHub for the newest release, so the tray menu can point at a newer version.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Repo is where releases are read from.
const Repo = "net-merge/net-merge"

// ReleasesURL is what the tray opens: GitHub redirects it to the newest release.
const ReleasesURL = "https://github.com/" + Repo + "/releases/latest"

// HomeURL is where an update is offered: the download and install command for this computer.
const HomeURL = "https://net-merge.org/#install"

var (
	client  = &http.Client{Timeout: 8 * time.Second}
	apiBase = "https://api.github.com" // tests point this at a local server
)

// Latest returns the tag of the newest release, e.g. "v0.3.4". GitHub leaves out prereleases.
func Latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "net-merge")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	// A release carries its notes, so the reply can be tens of kB; the limit only guards against a
	// wrong server answering.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("github: no tag_name in the release")
	}
	return rel.TagName, nil
}

// IsVersion reports whether v starts with a version number ("0.3.3", "0.3.3-dev", "v1.2").
// A build from source says "dev", which is not one.
func IsVersion(v string) bool {
	_, ok := numbers(v)
	return ok
}

// Newer reports whether release tag latest is newer than current. Anything after the number is
// ignored, so a dev build of 0.3.3 still hears about 0.3.4.
func Newer(current, latest string) bool {
	c, ok := numbers(current)
	if !ok {
		return false
	}
	l, ok := numbers(latest)
	if !ok {
		return false
	}
	for i := range c {
		if c[i] != l[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// numbers reads the leading dotted numbers, up to three: "v0.3.3-dev+1" -> 0.3.3.
func numbers(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	var out [3]int
	n := 0
	for n < len(out) {
		i := 0
		for i < len(v) && v[i] >= '0' && v[i] <= '9' {
			i++
		}
		if i == 0 {
			break
		}
		x, err := strconv.Atoi(v[:i])
		if err != nil {
			return out, false
		}
		out[n] = x
		n++
		v = strings.TrimPrefix(v[i:], ".")
	}
	return out, n > 0
}
