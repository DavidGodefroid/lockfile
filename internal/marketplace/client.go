package marketplace

import (
	"errors"
	"net/http"
	"time"
)

// Client limits for every download of an index, a plugin or a release.
const (
	ClientTimeout = 2 * time.Minute
	maxRedirects  = 10
)

// ErrInsecureRedirect refuses a redirect away from https.
var ErrInsecureRedirect = errors.New("refusing a redirect to a non-https URL")

var errTooManyRedirects = errors.New("too many redirects")

// SafeClient returns a copy of c (http.DefaultClient when nil) that follows
// redirects to https URLs only, at most 10 of them, and gives up after
// ClientTimeout. Integrity still comes from the signatures; this keeps a
// download off plain http and from hanging.
func SafeClient(c *http.Client) *http.Client {
	if c == nil {
		c = http.DefaultClient
	}
	out := *c
	if out.Timeout <= 0 || out.Timeout > ClientTimeout {
		out.Timeout = ClientTimeout
	}
	out.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host == "" {
			return ErrInsecureRedirect
		}
		if len(via) >= maxRedirects {
			return errTooManyRedirects
		}
		return nil
	}
	return &out
}
