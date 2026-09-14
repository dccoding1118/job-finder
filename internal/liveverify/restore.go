package liveverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// restoreRecord holds the original value of every item a run arranged. Each
// field is written before its item is changed, so a run stopped at any point
// leaves exactly the items that need putting back.
type restoreRecord struct {
	APIRunning     *bool  `json:"api_running,omitempty"`
	ConfigBackup   string `json:"config_backup,omitempty"`
	ConfigSHA256   string `json:"config_sha256,omitempty"`
	AutoProcessing *bool  `json:"auto_processing,omitempty"`
	FetchDisabled  *bool  `json:"fetch_disabled,omitempty"`
}

func (v *verifier) recordPath() string { return filepath.Join(v.dir, "restore.json") }

func (v *verifier) saveRecord() error {
	data, err := json.MarshalIndent(v.record, "", "  ")
	if err != nil {
		return err
	}
	temp := v.recordPath() + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write the restore record: %w", err)
	}
	return os.Rename(temp, v.recordPath())
}

func (v *verifier) loadRecord() (bool, error) {
	data, err := os.ReadFile(v.recordPath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	v.record = restoreRecord{}
	return true, json.Unmarshal(data, &v.record)
}

// restore puts back every item the record names, in the reverse of the order
// they were arranged in. It never stops early: an item that cannot be put back
// is returned with the command that restores it by hand, and the record is kept
// for the next run to try again.
func (v *verifier) restore(ctx context.Context) []string {
	present, err := v.loadRecord()
	if err != nil {
		return []string{fmt.Sprintf("復原紀錄 %s 無法讀取：%v", v.recordPath(), err)}
	}
	if !present {
		return nil
	}
	var unrestored []string

	if v.record.FetchDisabled != nil && *v.record.FetchDisabled {
		if err := v.platform.disableFetch(ctx); err == nil {
			v.note("- 復原：每日抓取工作回到停用")
		} else {
			unrestored = append(unrestored, `每日抓取工作應為停用：Disable-ScheduledTask -TaskPath '\jobfinder\' -TaskName 'run'`)
		}
	}

	if v.record.AutoProcessing != nil {
		want := *v.record.AutoProcessing
		if !v.api.responding(ctx) {
			_ = v.platform.startAPI(ctx)
			v.api.waitResponding(ctx)
		}
		if err := v.api.setAutoProcessing(ctx, want); err == nil {
			v.note(fmt.Sprintf("- 復原：自動處理開關回到 %t", want))
		} else {
			unrestored = append(unrestored, fmt.Sprintf("自動處理開關應為 %t：在 Side Panel 切換，或對 %s/settings 送 PUT {\"auto_processing\":%t}", want, v.api.base, want))
		}
	}

	if v.record.ConfigBackup != "" {
		if err := restoreFile(v.record.ConfigBackup, v.cfg.ConfigPath, v.record.ConfigSHA256); err == nil {
			_ = os.Remove(v.record.ConfigBackup)
			if running, _ := v.platform.apiRunning(ctx); running {
				_ = v.platform.restartAPI(ctx)
				v.api.waitResponding(ctx)
			}
			v.note("- 復原：config.yaml 以原檔覆蓋，SHA-256 相符")
		} else {
			unrestored = append(unrestored, fmt.Sprintf("config.yaml 應還原為原檔（%v）：以 %s 覆蓋 %s 後重啟 API 服務", err, v.record.ConfigBackup, v.cfg.ConfigPath))
		}
	}

	if v.record.APIRunning != nil && !*v.record.APIRunning {
		if err := v.platform.stopAPI(ctx); err == nil {
			v.note("- 復原：API 服務回到未執行")
		} else {
			unrestored = append(unrestored, fmt.Sprintf("API 服務應為未執行（%v）", err))
		}
	}

	if len(unrestored) == 0 {
		_ = os.Remove(v.recordPath())
		v.record = restoreRecord{}
	}
	return unrestored
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- installed config or its backup, both from the resolved layout.
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// copyFile copies bytes and keeps the owner-only mode the installed config has.
func copyFile(from, to string) error {
	data, err := os.ReadFile(from) // #nosec G304 -- installed config or its backup.
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o600) // #nosec G703 -- the installed config or its backup.
}

func restoreFile(backup, target, sum string) error {
	if err := copyFile(backup, target); err != nil {
		return err
	}
	got, err := fileSHA256(target)
	if err != nil {
		return err
	}
	if got != sum {
		return fmt.Errorf("SHA-256 %s differs from the original %s", got, sum)
	}
	return nil
}

// rewritePaused sets worker.paused to false and leaves every other byte alone,
// line endings included. It reports whether the key was found.
func rewritePaused(data []byte) ([]byte, bool) {
	lines := bytes.SplitAfter(data, []byte("\n"))
	inWorker, found := false, false
	for i, line := range lines {
		trimmed := bytes.TrimRight(line, "\r\n")
		if len(trimmed) > 0 && trimmed[0] != ' ' && trimmed[0] != '\t' && trimmed[0] != '#' {
			inWorker = bytes.HasPrefix(trimmed, []byte("worker:"))
			continue
		}
		if inWorker && bytes.HasPrefix(trimmed, []byte("  paused:")) {
			ending := line[len(trimmed):]
			lines[i] = append([]byte("  paused: false"), ending...)
			found = true
		}
	}
	return bytes.Join(lines, nil), found
}
