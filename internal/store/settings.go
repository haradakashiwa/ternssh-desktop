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

// Shortcut returns the shortcut of an action of the window the user set,
// "" for none, and whether they set one.
func (s *Store) Shortcut(action string) (acc string, set bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, set = s.d.Shortcuts[action]
	return acc, set
}

// SetShortcut saves the shortcut of an action of the window, "" for none.
func (s *Store) SetShortcut(action, acc string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.d.Shortcuts[action]; ok && old == acc {
		return nil
	}
	if s.d.Shortcuts == nil {
		s.d.Shortcuts = map[string]string{}
	}
	s.d.Shortcuts[action] = acc
	return s.save()
}

// ResetShortcuts gives every action of the window its own shortcut back.
func (s *Store) ResetShortcuts() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.d.Shortcuts) == 0 {
		return nil
	}
	s.d.Shortcuts = nil
	return s.save()
}
