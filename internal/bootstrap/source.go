package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	releasecontract "github.com/yuanjohn01-byte/codex-skin-plugin/internal/release"
)

const releaseOrigin = "https://github.com/yuanjohn01-byte/codex-skin-plugin/releases/download"

var helperAssetName = regexp.MustCompile(`^codex-skin-helper_[0-9A-Za-z.-]+_(?:macos_arm64|macos_x64|windows_x64\.exe)$`)

var allowedReleaseHosts = map[string]bool{
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

type HTTPReleaseSource struct {
	client *http.Client
}

func NewHTTPReleaseSource() *HTTPReleaseSource {
	return newHTTPReleaseSource(&http.Client{Timeout: 30 * time.Second})
}

func newHTTPReleaseSource(client *http.Client) *HTTPReleaseSource {
	cloned := *client
	if cloned.Timeout <= 0 {
		cloned.Timeout = 30 * time.Second
	}
	cloned.CheckRedirect = func(request *http.Request, previous []*http.Request) error {
		if len(previous) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return validateReleaseURL(request.URL)
	}
	return &HTTPReleaseSource{client: &cloned}
}

func (source *HTTPReleaseSource) Fetch(
	ctx context.Context,
	releaseTag, filename string,
	maxBytes int64,
) ([]byte, error) {
	if source == nil || source.client == nil {
		return nil, fmt.Errorf("HTTP release source is not configured")
	}
	if !validReleaseTag(releaseTag) || !validAssetName(filename) {
		return nil, fmt.Errorf("release tag or asset name is invalid")
	}
	if maxBytes < 1 || maxBytes > releasecontract.MaxArtifactSize+1 {
		return nil, fmt.Errorf("release asset limit is invalid")
	}
	assetURL := releaseOrigin + "/" + url.PathEscape(releaseTag) + "/" + url.PathEscape(filename)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := source.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.Request == nil {
		return nil, fmt.Errorf("release response has no final request URL")
	}
	if err := validateReleaseURL(response.Request.URL); err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub Release returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxBytes {
		return nil, fmt.Errorf("release asset exceeds the trusted size limit")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("release asset exceeds the trusted size limit")
	}
	return content, nil
}

func validAssetName(filename string) bool {
	return filename == descriptorFilename || filename == signatureFilename || helperAssetName.MatchString(filename)
}

func validateReleaseURL(candidate *url.URL) error {
	if candidate == nil || candidate.Scheme != "https" || candidate.User != nil || !allowedReleaseHosts[candidate.Hostname()] || candidate.Port() != "" {
		return fmt.Errorf("release redirect target is not trusted")
	}
	return nil
}
