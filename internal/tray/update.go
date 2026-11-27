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

// watchUpdates adds "(Update Available)" to the version line when GitHub has a newer release, while
// Check For Updates is ticked. It stays quiet when offline: the check is a convenience, not something
// to warn about.
func (u *trayUI) watchUpdates() {
	if !update.IsVersion(u.version) { // a build from source says "dev": nothing to compare
		return
	}
	retry := updateRetry
	for {
		wait := updateEvery
		if u.checkUpdates.Load() {
			latest, err := latestRelease()
			if err != nil {
				logx.Printf("update check: %v (next try in %s)\n", err, retry)
				wait, retry = retry, min(2*retry, updateMax)
			} else {
				retry = updateRetry                                           // a success means the network works again
				if update.Newer(u.version, latest) && u.checkUpdates.Load() { // not unticked meanwhile
					u.showUpdate(latest)
				}
			}
		}
		select {
		case <-u.recheck: // the box was ticked again
		case <-time.After(wait):
		}
	}
}

// setUpdateCheck applies the Check For Updates box: off, nothing is asked and nothing is offered.
func (u *trayUI) setUpdateCheck(on bool) {
	u.checkUpdates.Store(on)
	if on {
		select {
		case u.recheck <- struct{}{}:
		default:
		}
		return
	}
	u.updateShown.Store(false)
	u.versionItem.SetTitle(versionLabel(u.version))
	u.versionItem.SetTooltip(versionTip)
	setSymbol(u.versionItem, "info")
}

// latestRelease asks GitHub with a deadline: the tray must never wait on the network.
func latestRelease() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return update.Latest(ctx)
}

const versionTip = "The version of net-merge that is running (the same as net-merge version). Click to open the release page on GitHub."

// showUpdate keeps the version on the line, so a bug report still has it.
func (u *trayUI) showUpdate(latest string) {
	logx.Printf("update check: %s is available\n", latest)
	u.updateShown.Store(true)
	u.versionItem.SetTitle(updateLabel(u.version))
	u.versionItem.SetTooltip(versionLabel(latest) + " is out. Click to open net-merge.org and update.")
	setSymbol(u.versionItem, "download")
}

// versionURL is where a click on the version line goes.
func (u *trayUI) versionURL() string {
	if u.updateShown.Load() {
		return update.HomeURL
	}
	return update.ReleasesURL
}
