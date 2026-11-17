package tray

import (
	"context"
	"time"

	"net-merge/internal/logx"
	"net-merge/internal/update"
)

// updateEvery is how often the tray asks GitHub for a newer release. Releases are rare and GitHub
// rate-limits anonymous requests, so this is deliberately slow.
const updateEvery = 6 * time.Hour

// updateRetry is the first wait after a failed check. Started at sign-in the network is often not up
// yet, so a failure must not hide an update for six hours. It doubles per failure up to updateMax.
const (
	updateRetry = 2 * time.Minute
	updateMax   = 30 * time.Minute
)

// watchUpdates turns the version line into "Update to vX" when GitHub has a newer release. It stays
// quiet when offline: the check is a convenience, not something to warn about.
func (u *trayUI) watchUpdates() {
	if !update.IsVersion(u.version) { // a build from source says "dev": nothing to compare
		return
	}
	retry := updateRetry
	for {
		latest, err := latestRelease()
		if err != nil {
			logx.Printf("update check: %v (next try in %s)\n", err, retry)
			time.Sleep(retry)
			retry = min(2*retry, updateMax)
			continue
		}
		if update.Newer(u.version, latest) {
			u.showUpdate(latest)
		}
		retry = updateRetry // a success means the network works again
		time.Sleep(updateEvery)
	}
}

// latestRelease asks GitHub with a deadline: the tray must never wait on the network.
func latestRelease() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return update.Latest(ctx)
}

// showUpdate keeps the version in the tooltip, so a bug report still has it.
func (u *trayUI) showUpdate(latest string) {
	logx.Printf("update check: %s is available\n", latest)
	u.versionItem.SetTitle(updateLabel(latest))
	u.versionItem.SetTooltip("You are running " + versionLabel(u.version) +
		". Click to open the " + versionLabel(latest) + " release page on GitHub.")
	if u.native != nil {
		u.native.SetLatest(latest)
	}
}
