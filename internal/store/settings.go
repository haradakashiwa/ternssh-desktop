package store

import (
	"golang.org/x/crypto/bcrypt"
)

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
	// CommandTarget is where a quick command clicked is sent:
	// CommandTargetCurrent, CommandTargetAll or CommandTargetAsk.
	CommandTarget string `json:"commandTarget,omitempty"`
	// Web is the web app the app serves.
	Web Web `json:"web"`
}

// Web is how the app serves itself to browsers; its password is a secret,
// WebPasswordHash.
type Web struct {
	// Enabled serves it as the app starts.
	Enabled bool `json:"enabled,omitempty"`
	// Port is the port it listens on: DefaultWebPort when 0.
	Port int `json:"port,omitempty"`
	// LAN lets in the local network, not only this machine.
	LAN bool `json:"lan,omitempty"`
	// Public lets in any address, as of the internet.
	Public bool `json:"public,omitempty"`
	// Username is the name browsers sign in with, with the password of
	// WebPasswordHash, when the app lets in more than this machine.
	Username string `json:"username,omitempty"`
}

// Who the web app lets in (Web.Access).
const (
	WebLocal  = "local"  // this machine
	WebLAN    = "lan"    // and the local network, with a password
	WebPublic = "public" // and any address, with a password
)

// Access is who the web app lets in: WebLocal, WebLAN or WebPublic.
func (w Web) Access() string {
	switch {
	case w.Public:
		return WebPublic
	case w.LAN:
		return WebLAN
	}
	return WebLocal
}

// SetAccess lets in who access says.
func (w *Web) SetAccess(access string) {
	w.LAN, w.Public = access == WebLAN, access == WebPublic
}

// DefaultWebPort is the web app's port unless the settings say another.
const DefaultWebPort = 17722

// Where a quick command clicked is sent.
const (
	CommandTargetCurrent = ""    // the terminal shown
	CommandTargetAll     = "all" // every terminal connected, in every tab
	CommandTargetAsk     = "ask" // asked each time
)

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

// WebPasswordHash returns the bcrypt hash of the password browsers sign
// in with, nil when none was set.
func (s *Store) WebPasswordHash() []byte {
	if s.secrets == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.webPassword
	}
	b, err := s.secrets.Get("web.password")
	if err != nil || len(b) == 0 {
		return nil
	}
	return b
}

// SetWebPassword sets the password browsers sign in with, kept as its bcrypt hash.
func (s *Store) SetWebPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if s.secrets == nil {
		s.mu.Lock()
		s.webPassword = hash
		s.mu.Unlock()
		return nil
	}
	return s.secrets.Set("web.password", hash)
}
