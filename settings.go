package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings are app preferences, kept in their own file.
//
// They are deliberately not stored inside profiles.json: that file holds the
// user's backup configuration, is the one thing in here that would be painful
// to lose, and every extra reason to rewrite it is another chance to damage
// it. Preferences are cheap to lose, so they live apart.
type Settings struct {
	// DetachJobs keeps copies running when the window is closed. Default on:
	// it is what 2.3 is for. Off restores the 2.2 behaviour, where a job
	// belongs to the window that started it.
	DetachJobs bool `json:"detachJobs"`
	// HistoryRetentionHours is how long finished jobs stay in Attività before
	// the startup cleanup removes them. The status dots on the profiles are
	// derived from these jobs, so this is also how long a profile keeps
	// showing its last outcome.
	HistoryRetentionHours int `json:"historyRetentionHours"`
}

func defaultSettings() Settings { return Settings{DetachJobs: true, HistoryRetentionHours: 8} }

func settingsPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// loadSettings never fails loudly: a missing or damaged preferences file is
// not a reason to stop someone from running a backup, so the defaults win.
func loadSettings() Settings {
	path, err := settingsPath()
	if err != nil {
		return defaultSettings()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultSettings()
	}
	s := defaultSettings()
	if err := json.Unmarshal(data, &s); err != nil {
		return defaultSettings()
	}
	// A file written before the field existed decodes it to zero, which would
	// mean "delete everything at once": fall back to the default instead.
	if s.HistoryRetentionHours <= 0 {
		s.HistoryRetentionHours = defaultSettings().HistoryRetentionHours
	}
	return s
}

func saveSettings(s Settings) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (a *App) detachEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings.DetachJobs
}

// SetDetachJobs is bound to the frontend switch.
func (a *App) SetDetachJobs(on bool) error {
	a.mu.Lock()
	a.settings.DetachJobs = on
	s := a.settings
	a.mu.Unlock()
	return saveSettings(s)
}

// runLabel names a run for the Attività list: the profile's own name when
// there is one, otherwise what the profiles have in common.
func runLabel(list []SyncProfile) string {
	switch {
	case len(list) == 0:
		return "Esecuzione"
	case len(list) == 1:
		return list[0].Name
	}
	tag := list[0].Tag
	for _, p := range list {
		if p.Tag != tag {
			tag = ""
			break
		}
	}
	if tag != "" {
		return fmt.Sprintf("Tag: %s", tag)
	}
	names := make([]string, 0, len(list))
	for _, p := range list {
		names = append(names, p.Name)
	}
	if len(names) > 3 {
		return fmt.Sprintf("%s e altri %d", strings.Join(names[:3], ", "), len(names)-3)
	}
	return strings.Join(names, ", ")
}
