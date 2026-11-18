package state

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// AliasRequest is display-only. It crosses ctl/ so an unelevated CLI can ask the
// running tray/run process to save an alias, as with a speed-limit request.
type AliasRequest struct {
	Adapter string `json:"adapter"`
	Alias   string `json:"alias"`
}

func SendAlias(dir string, req AliasRequest) error {
	if err := ValidateAlias(req.Adapter, req.Alias); err != nil {
		return err
	}
	ctl := filepath.Join(dir, "ctl")
	if err := os.MkdirAll(ctl, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(ctl, "alias-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmp.Name(), filepath.Join(ctl, "alias"))
}

func TakeAlias(dir string) (AliasRequest, bool) {
	p := filepath.Join(dir, "ctl", "alias")
	taken := p + ".taken"
	os.Remove(taken)
	if err := os.Rename(p, taken); err != nil {
		return AliasRequest{}, false
	}
	defer os.Remove(taken)
	b, ok := readLimitRequest(taken) // bounded regular-file read; no symlinks/FIFOs on Unix
	var req AliasRequest
	if !ok || json.Unmarshal(b, &req) != nil || ValidateAlias(req.Adapter, req.Alias) != nil {
		return AliasRequest{}, false
	}
	return req, true
}
