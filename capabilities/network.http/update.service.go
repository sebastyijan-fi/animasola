package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"golang.org/x/net/proxy"
)

const githubLatestReleaseURL = "https://api.github.com/repos/sebastyijan-fi/animasola/releases/latest"

type GitHubRelease struct {
	TagName string `json:"tag_name"`
	HtmlURL string `json:"html_url"`
}

// FetchLatestRelease polls the GitHub API.
// If ALL_PROXY is set, it routes through that proxy; otherwise it uses a direct client.
func FetchLatestRelease() (*GitHubRelease, error) {
	client, err := newHTTPClient()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", githubLatestReleaseURL, nil)
	if err != nil {
		return nil, err
	}
	// GitHub requires a User-Agent
	req.Header.Set("User-Agent", "Animasola-Tor-Client")

	// Retry loop to handle Tor SOCKS latency
	var resp *http.Response
	var fetchErr error
	for i := 0; i < 5; i++ {
		resp, fetchErr = client.Do(req)
		if fetchErr == nil {
			break
		}
		time.Sleep(time.Millisecond * 500)
	}
	if fetchErr != nil {
		return nil, fmt.Errorf("failed to fetch release after retries: %w", fetchErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned non-200 status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var release GitHubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return nil, fmt.Errorf("failed to json parse release: %w", err)
	}

	return &release, nil
}

// DownloadReleaseAsset leverages the Tor SOCKS5 proxy to download the target OS binary
// identically to how FetchLatestRelease checks versions. It writes the result to outPath.
func DownloadReleaseAsset(downloadURL string, outPath string) error {
	client, err := newHTTPClient()
	if err != nil {
		return err
	}
	client.Timeout = time.Minute * 10 // Binaries are large and proxies may be slow.

	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Animasola-Tor-Client")

	// GitHub Release Assets actually redirect to objects.githubusercontent.com
	// The Go HTTP client follows redirects natively, so this will seamlessly work over Tor.
	var resp *http.Response
	var fetchErr error
	for i := 0; i < 5; i++ {
		resp, fetchErr = client.Do(req)
		if fetchErr == nil {
			break
		}
		time.Sleep(time.Millisecond * 500)
	}
	if fetchErr != nil {
		return fmt.Errorf("failed to download asset after retries: %w", fetchErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github api returned non-200 status during download: %d", resp.StatusCode)
	}

	// Create the temporary file we'll stream the payload into
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temporary binary file: %w", err)
	}
	defer out.Close()

	// Stream the response body securely onto disk
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to stream binary: %w", err)
	}

	return nil
}

func newHTTPClient() (*http.Client, error) {
	transport := &http.Transport{}
	proxyStr := os.Getenv("ALL_PROXY")
	if proxyStr != "" {
		proxyURL, err := url.Parse(proxyStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ALL_PROXY url: %w", err)
		}

		dialer, err := proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create proxy dialer: %w", err)
		}
		transport.Dial = dialer.Dial
	}

	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}, nil
}
