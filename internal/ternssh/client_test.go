package ternssh

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServerCredential(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/servers/{id}/credential", func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "admin" || p != "secret" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":"Invalid basic authentication credentials"}`)
			return
		}
		switch r.PathValue("id") {
		case "s1":
			io.WriteString(w, `{"id":"s1","host":"10.0.0.1","port":2222,"username":"root","auth_type":"private_key","private_key":"KEY","passphrase":"pp"}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"error":"server not found"}`)
		}
	})
	site := httptest.NewServer(mux)
	defer site.Close()
	// A site from before the API serves its web app at every other path.
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!doctype html><html></html>")
	}))
	defer old.Close()

	ctx := context.Background()
	c, _ := New(site.URL, Auth{Username: "admin", Password: "secret"})
	cred, err := c.ServerCredential(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if *cred != (ServerCredential{Host: "10.0.0.1", Port: 2222, Username: "root", AuthType: "private_key", PrivateKey: "KEY", Passphrase: "pp"}) {
		t.Errorf("credential %+v", cred)
	}
	for _, tc := range []struct {
		url, user, id, want string
	}{
		{site.URL, "admin", "s9", "server not found"},
		{site.URL, "nope", "s1", "认证失败"},
		{old.URL, "admin", "s1", "站点版本过旧"},
	} {
		c, _ := New(tc.url, Auth{Username: tc.user, Password: "secret"})
		if _, err := c.ServerCredential(ctx, tc.id); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s %s: error %v, want %q", tc.user, tc.id, err, tc.want)
		}
	}
}

func TestGroupsAndMove(t *testing.T) {
	var got []string
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(b))
		switch r.URL.Path {
		case "/api/v1/servers/groups":
			io.WriteString(w, `{"group":{"id":"g9","name":"新组"}}`)
		case "/api/v1/servers/move":
			io.WriteString(w, `{"tree":[{"type":"group","id":"g9","name":"新组","children":[{"type":"server","id":"s1","name":"web"}]}]}`)
		default:
			io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer site.Close()
	c, _ := New(site.URL, Auth{})
	ctx := context.Background()
	g, err := c.CreateGroup(ctx, "新组", nil)
	if err != nil || g.ID != "g9" || g.Type != "group" {
		t.Fatalf("group %+v, %v", g, err)
	}
	tree, err := c.Move(ctx, "server", "s1", &g.ID, 0)
	if err != nil || len(tree) != 1 || tree[0].Children[0].ID != "s1" {
		t.Fatalf("tree %+v, %v", tree, err)
	}
	c.RenameGroup(ctx, "g9", "改名")
	c.DeleteGroup(ctx, "g9")
	want := []string{
		`POST /api/v1/servers/groups {"name":"新组","parent_id":null}`,
		`PUT /api/v1/servers/move {"id":"s1","index":0,"parentId":"g9","type":"server"}`,
		`PUT /api/v1/servers/groups/g9 {"name":"改名"}`,
		`DELETE /api/v1/servers/groups/g9 `,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAccessLogin(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("CF_Authorization"); err != nil || c.Value != "jwt2" {
			http.Redirect(w, r, "https://team.cloudflareaccess.com/cdn-cgi/access/login/site?redirect_url=/", http.StatusFound)
			return
		}
		io.WriteString(w, `{"user":{"id":"u1"},"authMode":"none"}`)
	}))
	defer site.Close()
	ctx := context.Background()

	c, _ := New(site.URL, Auth{AccessToken: "jwt1"})
	if _, err := c.Me(ctx); err != ErrAccessLogin {
		t.Fatalf("without a sign-in: %v", err)
	}

	var mu sync.Mutex
	logins := 0
	c.SetAccessLogin(func(context.Context) (string, error) {
		mu.Lock()
		logins++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		return "jwt2", nil
	})
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if me, err := c.Me(ctx); err != nil || me.User.ID != "u1" {
				t.Errorf("me %+v, %v", me, err)
			}
		}()
	}
	wg.Wait()
	if logins != 1 {
		t.Errorf("signed in %d times, want once", logins)
	}

	// A sign-in the user gave up fails the requests waiting for it.
	c, _ = New(site.URL, Auth{})
	canceled := errors.New("canceled")
	c.SetAccessLogin(func(context.Context) (string, error) { return "", canceled })
	if _, err := c.Me(ctx); err != canceled {
		t.Errorf("canceled sign-in: %v", err)
	}
}
