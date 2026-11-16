// Package store keeps the app's sites, the servers, local terminals and
// groups of this machine (the 本机 tab, which needs no site), and the host
// keys of the servers it connected to: what is not secret in a JSON file,
// and passwords, tokens and keys in the system's credential store.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AuthKind is how a site authenticates.
type AuthKind string

const (
	AuthNone   AuthKind = "none"
	AuthBasic  AuthKind = "basic"
	AuthAccess AuthKind = "access" // a Cloudflare Access service token
)

// Site is a ternssh site, a tab of the window.
type Site struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Auth     AuthKind `json:"auth"`
	Username string   `json:"username,omitempty"` // basic
	ClientID string   `json:"clientId,omitempty"` // Access
}

// Server is a server kept only on this machine.
type Server struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthType string `json:"authType"` // "password" or "private_key"
	HostKey  string `json:"hostKey,omitempty"`
	GroupID  string `json:"groupId,omitempty"`
	Sort     int    `json:"sort,omitempty"` // its place among what is in its group
}

// Group is a group of the 本机 tab, of servers, terminals and groups.
type Group struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"` // empty at the top
	Sort     int    `json:"sort,omitempty"`
}

// Terminal is a shell on this machine, in the 本机 tab.
type Terminal struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Dir is where the shell starts: the home directory when empty.
	Dir     string `json:"dir,omitempty"`
	GroupID string `json:"groupId,omitempty"`
	Sort    int    `json:"sort,omitempty"`
}

// Credential is a server's secret.
type Credential struct {
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

// Secrets is where secrets go: mygo.Secrets in the app.
type Secrets interface {
	Set(key string, value []byte) error
	Get(key string) ([]byte, error)
	Delete(key string) error
}

type data struct {
	Sites     []Site     `json:"sites"`
	Servers   []Server   `json:"servers"`
	Terminals []Terminal `json:"terminals,omitempty"`
	Groups    []Group    `json:"groups,omitempty"`
	// QuickCommands are the quick commands of the 本机 tab.
	QuickCommands []QuickCommand `json:"quickCommands,omitempty"`
	ActiveSite    string         `json:"activeSite,omitempty"`
	// SiteHostKeys are the host keys of the servers a site keeps, by
	// siteHostKey.
	SiteHostKeys map[string]string `json:"siteHostKeys,omitempty"`
	Settings     Settings          `json:"settings"`
}

func siteHostKey(siteID, serverID string) string { return siteID + "/" + serverID }

// Store is the app's saved state. Its methods are safe from any goroutine.
type Store struct {
	path    string
	secrets Secrets

	mu sync.Mutex
	d  data
}

// Open reads the store at path, empty when there is none yet.
func Open(path string, secrets Secrets) (*Store, error) {
	s := &Store{path: path, secrets: secrets}
	removeHistories(path)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		return nil, err
	}
	return s, nil
}

// NewID returns a random ID.
func NewID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Sites returns the sites, in the order of their tabs.
func (s *Store) Sites() []Site {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Site(nil), s.d.Sites...)
}

// ActiveSite returns the ID of the site whose tab was chosen last.
func (s *Store) ActiveSite() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.ActiveSite
}

// SetActiveSite remembers the tab chosen.
func (s *Store) SetActiveSite(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.d.ActiveSite == id {
		return nil
	}
	s.d.ActiveSite = id
	return s.save()
}

// PutSite adds a site, or replaces the one with its ID, with its secret:
// the basic password or the Access client secret.
func (s *Store) PutSite(site Site, secret string) error {
	if s.secrets != nil {
		if err := s.secrets.Set("site."+site.ID, []byte(secret)); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.Sites {
		if s.d.Sites[i].ID == site.ID {
			s.d.Sites[i] = site
			return s.save()
		}
	}
	s.d.Sites = append(s.d.Sites, site)
	return s.save()
}

// SiteSecret returns the secret PutSite stored.
func (s *Store) SiteSecret(id string) string {
	if s.secrets == nil {
		return ""
	}
	b, _ := s.secrets.Get("site." + id)
	return string(b)
}

// MoveSite moves the tab of a site to index i.
func (s *Store) MoveSite(id string, i int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := -1
	for j := range s.d.Sites {
		if s.d.Sites[j].ID == id {
			from = j
		}
	}
	if from < 0 || i < 0 || i >= len(s.d.Sites) || i == from {
		return nil
	}
	site := s.d.Sites[from]
	s.d.Sites = append(s.d.Sites[:from], s.d.Sites[from+1:]...)
	s.d.Sites = append(s.d.Sites[:i], append([]Site{site}, s.d.Sites[i:]...)...)
	return s.save()
}

// DeleteSite removes a site, with what this machine remembers of it.
func (s *Store) DeleteSite(id string) error {
	s.mu.Lock()
	sites := s.d.Sites[:0]
	for _, site := range s.d.Sites {
		if site.ID != id {
			sites = append(sites, site)
		}
	}
	s.d.Sites = sites
	for k := range s.d.SiteHostKeys {
		if strings.HasPrefix(k, id+"/") {
			delete(s.d.SiteHostKeys, k)
		}
	}
	if s.d.ActiveSite == id {
		s.d.ActiveSite = ""
	}
	err := s.save()
	s.mu.Unlock()
	if s.secrets != nil {
		s.secrets.Delete("site." + id)
	}
	return err
}

// Servers returns the local servers.
func (s *Store) Servers() []Server {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Server(nil), s.d.Servers...)
}

// PutServer adds a local server, or replaces the one with its ID. A nil
// credential keeps the one stored.
func (s *Store) PutServer(srv Server, cred *Credential) error {
	if cred != nil && s.secrets != nil {
		b, _ := json.Marshal(cred)
		if err := s.secrets.Set("server."+srv.ID, b); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, old := range s.d.Servers {
		if old.ID == srv.ID {
			srv.Sort = old.Sort
			if old.GroupID != srv.GroupID {
				srv.Sort = s.nextSort(srv.GroupID)
			}
			s.d.Servers[i] = srv
			return s.save()
		}
	}
	srv.Sort = s.nextSort(srv.GroupID)
	s.d.Servers = append(s.d.Servers, srv)
	return s.save()
}

// SetHostKey remembers the host key of a local server.
func (s *Store) SetHostKey(serverID, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.Servers {
		if s.d.Servers[i].ID == serverID {
			s.d.Servers[i].HostKey = fingerprint
			return s.save()
		}
	}
	return nil
}

// SiteHostKey returns the host key remembered for a server of a site.
func (s *Store) SiteHostKey(siteID, serverID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.SiteHostKeys[siteHostKey(siteID, serverID)]
}

// SetSiteHostKey remembers the host key of a server of a site, or forgets
// it when fingerprint is empty.
func (s *Store) SetSiteHostKey(siteID, serverID, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := siteHostKey(siteID, serverID)
	if fingerprint == "" {
		delete(s.d.SiteHostKeys, k)
	} else {
		if s.d.SiteHostKeys == nil {
			s.d.SiteHostKeys = map[string]string{}
		}
		s.d.SiteHostKeys[k] = fingerprint
	}
	return s.save()
}

// ServerCredential returns the secret of a local server.
func (s *Store) ServerCredential(id string) (Credential, error) {
	var cred Credential
	if s.secrets == nil {
		return cred, errors.New("没有可用的凭据存储")
	}
	b, err := s.secrets.Get("server." + id)
	if err != nil {
		return cred, err
	}
	err = json.Unmarshal(b, &cred)
	return cred, err
}

// DeleteServer removes a local server and its secret.
func (s *Store) DeleteServer(id string) error {
	s.mu.Lock()
	servers := s.d.Servers[:0]
	for _, srv := range s.d.Servers {
		if srv.ID != id {
			servers = append(servers, srv)
		}
	}
	s.d.Servers = servers
	err := s.save()
	s.mu.Unlock()
	if s.secrets != nil {
		s.secrets.Delete("server." + id)
	}
	return err
}

// Terminals returns the local terminals.
func (s *Store) Terminals() []Terminal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Terminal(nil), s.d.Terminals...)
}

// PutTerminal adds a local terminal, or replaces the one with its ID.
func (s *Store) PutTerminal(t Terminal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, old := range s.d.Terminals {
		if old.ID == t.ID {
			t.Sort = old.Sort
			if old.GroupID != t.GroupID {
				t.Sort = s.nextSort(t.GroupID)
			}
			s.d.Terminals[i] = t
			return s.save()
		}
	}
	t.Sort = s.nextSort(t.GroupID)
	s.d.Terminals = append(s.d.Terminals, t)
	return s.save()
}

// DeleteTerminal removes a local terminal.
func (s *Store) DeleteTerminal(id string) error {
	s.mu.Lock()
	terms := s.d.Terminals[:0]
	for _, t := range s.d.Terminals {
		if t.ID != id {
			terms = append(terms, t)
		}
	}
	s.d.Terminals = terms
	err := s.save()
	s.mu.Unlock()
	return err
}
