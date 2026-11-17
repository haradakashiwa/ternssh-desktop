package main

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"ternssh-desktop/locales"
)

// accessCookie is the cookie of a sign-in to Cloudflare Access, which
// Access gives the pages of the site it protects.
const accessCookie = "CF_Authorization"

// accessLogin opens the site at siteURL in a window, where its Cloudflare
// Access asks the user to sign in, and returns the token Access gives the
// site's pages once they did, closing the window. It fails when the user
// closes the window first.
func accessLogin(ctx context.Context, siteURL string) (string, error) {
	u, err := url.Parse(siteURL)
	if err != nil {
		return "", err
	}
	// The token Access refused would be taken for that of the new
	// sign-in. Access's own cookies stay, so that a sign-in it still
	// remembers needs nothing of the user.
	filter := mygo.CookieFilter{URL: siteURL, Name: accessCookie}
	if old, err := mygo.Cookies.List(filter); err == nil {
		for _, c := range old {
			mygo.Cookies.Delete(c)
		}
	}

	closed := make(chan struct{})
	var once sync.Once
	opts := mygo.WindowOptions{
		Title:    locales.AccessLoginTitle(u.Host),
		Width:    520,
		Height:   720,
		MinWidth: 380,
	}
	popup := opts
	opts.URL = siteURL
	win := mygo.NewWindow(opts)
	// An identity provider's sign-in in a pop-up stays in the app, where
	// its cookies are the window's.
	win.Page().SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions { return &popup })
	win.OnClosed(func() { once.Do(func() { close(closed) }) })
	win.Show()
	win.Focus()
	mygo.App.Focus()

	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-closed:
			return "", errors.New(locales.SiteAccessCanceled())
		case <-ctx.Done():
			win.Close()
			return "", ctx.Err()
		case <-tick.C:
		}
		cookies, err := mygo.Cookies.List(filter)
		if err != nil {
			continue
		}
		for _, c := range cookies {
			if c.Value != "" {
				win.Close()
				return c.Value, nil
			}
		}
	}
}
