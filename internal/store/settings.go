package store

// Settings are the app's own preferences.
type Settings struct {
	// Hotkey is the system-wide shortcut that shows and hides the window,
	// as "Alt+Space"; empty for none.
	Hotkey string `json:"hotkey,omitempty"`
	// QuitOnClose quits the app as its window closes, instead of keeping
	// it in the menu bar or notification area.
	QuitOnClose bool `json:"quitOnClose,omitempty"`
	// Language is the language the app shows, as "en" or "zh-Hans";
	// empty to follow the system's.
	Language string `json:"language,omitempty"`
}

// Settings returns the app's preferences.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings
}

// SetSettings saves the app's preferences.
func (s *Store) SetSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.Settings == v {
		return nil
	}
	s.d.Settings = v
	return s.save()
}
