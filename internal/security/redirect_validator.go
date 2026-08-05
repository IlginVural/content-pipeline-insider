package security

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// MaxRedirects caps how many hops a request may follow. Redirects are
// legitimate — APIs move, http upgrades to https — but each hop is
// another URL we did not approve, and an unbounded chain is a way to
// tie up a connection indefinitely.
const MaxRedirects = 3

// ValidateRedirect re-runs the full check on a redirect target.
//
// This exists because validating only the original URL is a complete
// bypass. An attacker registers a perfectly innocent public host and
// has it answer 302 Location: http://169.254.169.254/. Your first
// check passed, the client dutifully follows the redirect, and the
// request lands exactly where the check was supposed to prevent. Every
// hop is a new destination and gets judged like the first one.
func ValidateRedirect(ctx context.Context, resolver Resolver, req *http.Request, via []*http.Request, policy Policy) error {
	if len(via) >= MaxRedirects {
		return fmt.Errorf("%w: more than %d", ErrTooManyRedirects, MaxRedirects)
	}
	return ValidateTarget(ctx, resolver, req.URL, policy)
}

// ValidateTarget is the full pre-flight check for one URL: scheme,
// port, literal IP, then DNS resolution and per-address checks.
func ValidateTarget(ctx context.Context, resolver Resolver, u *url.URL, policy Policy) error {
	if err := ValidateURL(u, policy); err != nil {
		return err
	}
	if resolver == nil {
		resolver = DefaultResolver
	}
	_, err := ValidateHost(ctx, resolver, u.Hostname(), policy)
	return err
}
