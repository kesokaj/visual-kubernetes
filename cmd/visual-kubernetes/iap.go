package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/kesokaj/visual-kubernetes/internal/iapauth"
)

// parseAllowedHosts splits comma-separated DNS hostnames for stream.HostGuard, rejecting
// wildcards, URLs/schemes, ports, IP literals, and empty entries so host configuration fails closed.
func parseAllowedHosts(raw string, explicitlySet bool) ([]string, error) {
	if !explicitlySet && raw == "" {
		return nil, nil
	}
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("--allowed-host must not be empty")
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(p), "."))
		if err := validateAllowedHost(h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

func validateAllowedHost(h string) error {
	if h == "" {
		return errors.New("--allowed-host contains an empty hostname")
	}
	if strings.ContainsAny(h, "*:/\\?#@ \t\r\n[]") {
		return fmt.Errorf("--allowed-host %q must be a bare DNS hostname without wildcards, scheme, port, or path", h)
	}
	if net.ParseIP(h) != nil {
		return fmt.Errorf("--allowed-host %q must be a DNS hostname (IP literals are already permitted)", h)
	}
	return nil
}

func resolveIAPMiddleware(ctx context.Context, o serveOptions, allowedHosts []string, log *slog.Logger) (func(http.Handler) http.Handler, bool, error) {
	if !o.iapAudienceSet && o.iapAudience == "" {
		return nil, false, nil
	}
	if err := iapauth.ValidateAudience(o.iapAudience); err != nil {
		return nil, false, err
	}
	if len(allowedHosts) == 0 {
		return nil, false, errors.New("--iap-audience requires a non-empty --allowed-host")
	}
	v := o.iapValidator
	if v == nil {
		var err error
		v, err = iapauth.NewDefaultValidator(ctx)
		if err != nil {
			return nil, false, err
		}
	}
	mw, err := iapauth.NewMiddleware(v, o.iapAudience, log)
	if err != nil {
		return nil, false, err
	}
	return mw, true, nil
}
