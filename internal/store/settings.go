package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// SettingAutoProcessing is the switch the user flips from the Side Panel to
// stop the resident worker consuming the screening and scoring stages. It is
// the token brake: collection, capture, and the user's own single-job requests
// stay available while it is off, and jobs simply wait at `new` and `queued`.
const SettingAutoProcessing = "auto_processing"

// Setting reads one stored setting. found is false when the user has never set
// it, which is what lets each caller keep its own default.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	if strings.TrimSpace(key) == "" {
		return "", false, errors.New("store: setting key is required")
	}
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting stores one setting, overwriting any previous value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("store: setting key is required")
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at", key, value, s.timestamp()); err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}

// AutoProcessing reports whether the worker may consume the screening and
// scoring stages on its own. A database that has never recorded the switch
// reports on: automatic processing is the normal mode, and turning it off is
// the deliberate act.
func (s *Store) AutoProcessing(ctx context.Context) (bool, error) {
	value, found, err := s.Setting(ctx, SettingAutoProcessing)
	if err != nil || !found {
		return true, err
	}
	return value == "on", nil
}

// SetAutoProcessing records the switch.
func (s *Store) SetAutoProcessing(ctx context.Context, enabled bool) error {
	value := "off"
	if enabled {
		value = "on"
	}
	return s.SetSetting(ctx, SettingAutoProcessing, value)
}
