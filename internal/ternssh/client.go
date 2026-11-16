// Package ternssh is a client of a ternssh site's HTTP API: the server
// tree, adding servers, and the credentials this machine connects to them
// with.
package ternssh

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ternssh-desktop/locales"
)

// Auth is how requests to a site authenticate.
type Auth struct {
	// Username and Password are the site's basic authentication, when it
	// uses it.
	Username, Password string
	// AccessClientID and AccessClientSecret are a Cloudflare Access
	// service token, for a site behind Access.
	AccessClientID, AccessClientSecret string
}

// Header returns the headers that authenticate a request.
func (a Auth) Header() http.Header {
	h := http.Header{}
	if a.Username != "" || a.Password != "" {
		h.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(a.Username+":"+a.Password)))
	}
	if a.AccessClientID != "" {
		h.Set("CF-Access-Client-Id", a.AccessClientID)
		h.Set("CF-Access-Client-Secret", a.AccessClientSecret)
	}
	return h
}

// Client talks to one site.
type Client struct {
	base *url.URL
	auth Auth
	http *http.Client
}

// NormalizeURL turns what a user types into a site's base URL: https is
// assumed, and the path is dropped.
func NormalizeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New(locales.SiteAddressRequired())
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New(locales.SiteAddressInvalid(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New(locales.SiteAddressScheme())
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

// New returns a client of the site at baseURL.
func New(baseURL string, auth Auth) (*Client, error) {
	u, err := NormalizeURL(baseURL)
	if err != nil {
		return nil, err
	}
	return &Client{base: u, auth: auth, http: &http.Client{Timeout: 20 * time.Second}}, nil
}

// BaseURL returns the site's base URL.
func (c *Client) BaseURL() string { return c.base.String() }

// Error is a response of the API that is not a success.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		if e.Message != "" {
			return locales.SiteAuthFailedMessage(e.Message)
		}
		return locales.SiteAuthFailed()
	case http.StatusForbidden:
		if e.Message == "Setup required" {
			return locales.SiteSetupRequired()
		}
	}
	if e.Message != "" {
		return e.Message
	}
	return locales.SiteRequestFailed(strconv.Itoa(e.Status))
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), r)
	if err != nil {
		return err
	}
	for k, v := range c.auth.Header() {
		req.Header[k] = v
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", locales.SiteUnreachable(), err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var e struct{ Error string }
		_ = json.Unmarshal(data, &e)
		if e.Error == "" && !strings.HasPrefix(strings.TrimSpace(string(data)), "<") {
			e.Error = strings.TrimSpace(string(data))
		}
		return &Error{Status: res.StatusCode, Message: e.Error}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		// Cloudflare Access answers an unauthenticated request with its
		// login page.
		if strings.HasPrefix(strings.TrimSpace(string(data)), "<") {
			return errPage
		}
		return fmt.Errorf("%s: %w", locales.SiteBadResponse(), err)
	}
	return nil
}

// errPage is the answer of a web page where the API was asked.
var errPage error = &msgError{locales.SiteGotPage}

// msgError is an error whose message is said in the language the app
// shows as it is asked for, so that a sentinel error keeps its identity.
type msgError struct{ msg func() string }

func (e *msgError) Error() string { return e.msg() }

// User is the site's user.
type User struct {
	ID       string  `json:"id"`
	SiteName *string `json:"site_name"`
}

// Me is GET /api/v1/me.
type Me struct {
	User     User   `json:"user"`
	AuthMode string `json:"authMode"`
}

// Me returns the site's user and how it authenticates, which checks that
// the client reaches it.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var me Me
	if err := c.do(ctx, http.MethodGet, "/api/v1/me", nil, &me); err != nil {
		return nil, err
	}
	return &me, nil
}

// Node is a group or a server of the server tree.
type Node struct {
	Type     string  `json:"type"` // "group" or "server"
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	ParentID *string `json:"parent_id,omitempty"`
	Children []Node  `json:"children,omitempty"`

	Host     string  `json:"host,omitempty"`
	Port     int     `json:"port,omitempty"`
	Username string  `json:"username,omitempty"`
	AuthType string  `json:"auth_type,omitempty"`
	GroupID  *string `json:"group_id,omitempty"`
}

// Tree returns the site's servers, in their groups.
func (c *Client) Tree(ctx context.Context) ([]Node, error) {
	var res struct{ Tree []Node }
	if err := c.do(ctx, http.MethodGet, "/api/v1/servers/tree", nil, &res); err != nil {
		return nil, err
	}
	return res.Tree, nil
}

// NewServer is a server to add to a site.
type NewServer struct {
	Name       string  `json:"name"`
	Host       string  `json:"host"`
	Port       int     `json:"port"`
	Username   string  `json:"username"`
	AuthType   string  `json:"auth_type"` // "password" or "private_key"
	Credential string  `json:"credential"`
	GroupID    *string `json:"group_id"`
}

// CreateServer adds a server to the site, which keeps its credential.
func (c *Client) CreateServer(ctx context.Context, s NewServer) (*Node, error) {
	var res struct{ Server Node }
	if err := c.do(ctx, http.MethodPost, "/api/v1/servers", s, &res); err != nil {
		return nil, err
	}
	res.Server.Type = "server"
	return &res.Server, nil
}

// ServerUpdate changes a server of a site. Credential and Passphrase are
// left as they are when nil.
type ServerUpdate struct {
	Name       string  `json:"name"`
	Host       string  `json:"host"`
	Port       int     `json:"port"`
	Username   string  `json:"username"`
	AuthType   string  `json:"auth_type"`
	Credential *string `json:"credential,omitempty"`
	Passphrase *string `json:"passphrase,omitempty"`
	GroupID    *string `json:"group_id"`
}

// UpdateServer changes a server of the site.
func (c *Client) UpdateServer(ctx context.Context, id string, u ServerUpdate) error {
	return c.do(ctx, http.MethodPut, "/api/v1/servers/"+url.PathEscape(id), u, nil)
}

// CreateGroup adds a group to the site, in the group parentID or at the
// top when it is nil.
func (c *Client) CreateGroup(ctx context.Context, name string, parentID *string) (*Node, error) {
	var res struct{ Group Node }
	err := c.do(ctx, http.MethodPost, "/api/v1/servers/groups", map[string]any{"name": name, "parent_id": parentID}, &res)
	if err != nil {
		return nil, err
	}
	res.Group.Type = "group"
	return &res.Group, nil
}

// RenameGroup renames a group of the site.
func (c *Client) RenameGroup(ctx context.Context, id, name string) error {
	return c.do(ctx, http.MethodPut, "/api/v1/servers/groups/"+url.PathEscape(id), map[string]string{"name": name}, nil)
}

// DeleteGroup removes a group of the site with the groups in it; the
// servers in them go to the top.
func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/servers/groups/"+url.PathEscape(id), nil, nil)
}

// Move moves a server or group (typ "server" or "group") into the group
// parentID, or to the top when it is nil, at index among what is there,
// and returns the site's tree after.
func (c *Client) Move(ctx context.Context, typ, id string, parentID *string, index int) ([]Node, error) {
	var res struct{ Tree []Node }
	body := map[string]any{"type": typ, "id": id, "parentId": parentID, "index": index}
	if err := c.do(ctx, http.MethodPut, "/api/v1/servers/move", body, &res); err != nil {
		return nil, err
	}
	return res.Tree, nil
}

// DeleteServer removes a server from the site.
func (c *Client) DeleteServer(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v1/servers/"+url.PathEscape(id), nil, nil)
}

// ServerCredential is a server's address and secret, which the site gives
// to a client that connects to the server itself.
type ServerCredential struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	AuthType   string `json:"auth_type"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase"`
}

// ServerCredential returns how to connect to a server of the site.
func (c *Client) ServerCredential(ctx context.Context, id string) (*ServerCredential, error) {
	var cred ServerCredential
	err := c.do(ctx, http.MethodGet, "/api/v1/servers/"+url.PathEscape(id)+"/credential", nil, &cred)
	// A site from before the API had this answers with its web app.
	var apiErr *Error
	if err == errPage || errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound && apiErr.Message == "" {
		return nil, errors.New(locales.SiteTooOld())
	}
	if err != nil {
		return nil, err
	}
	return &cred, nil
}
