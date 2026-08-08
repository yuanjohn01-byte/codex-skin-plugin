package browseropen

import (
	"context"
	"errors"
	"net"
	"net/url"
)

var ErrUnsafeURL = errors.New("browser URL is unsafe")

func Open(ctx context.Context, value string) error {
	parsed, err := url.Parse(value)
	if err != nil ||
		!parsed.IsAbs() ||
		parsed.User != nil ||
		parsed.Hostname() == "" ||
		parsed.Fragment != "" ||
		(parsed.Scheme != "https" && !localHTTP(parsed)) {
		return ErrUnsafeURL
	}
	return openPlatform(ctx, parsed.String())
}

func localHTTP(value *url.URL) bool {
	if value.Scheme != "http" {
		return false
	}
	return value.Hostname() == "localhost" || net.ParseIP(value.Hostname()).IsLoopback()
}
