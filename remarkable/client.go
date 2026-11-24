package remarkable

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultURL is the tablet's address on the USB network.
const DefaultURL = "http://10.11.99.1"

// PingTimeout bounds how long Ping waits for the tablet.
const PingTimeout = 2 * time.Second

// Client talks to the tablet's USB web interface.
type Client struct {
	BaseURL string
	// HTTP is the client used for requests; http.DefaultClient if nil.
	// Avoid a global Timeout: large PDFs can take a while to render.
	HTTP *http.Client
}

// NewClient returns a client for baseURL (DefaultURL if empty).
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultURL
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{}}
}

// StatusError is returned when the tablet answers with a non-200 status.
type StatusError struct {
	Method, URL string
	Code        int
	Body        string
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Code)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

func (c *Client) do(ctx context.Context, method, path string) (*http.Response, error) {
	u := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, &StatusError{Method: method, URL: u, Code: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return resp, nil
}

// Ping checks that the tablet's web interface answers, within PingTimeout.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, PingTimeout)
	defer cancel()
	resp, err := c.do(ctx, http.MethodGet, "/")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// List returns the entries of a folder; folderID "" is the root.
func (c *Client) List(ctx context.Context, folderID string) ([]Entry, error) {
	resp, err := c.do(ctx, http.MethodPost, "/documents/"+url.PathEscape(folderID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var entries []Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode listing of folder %q: %w", folderID, err)
	}
	return entries, nil
}

// Download streams a document in the given format. The caller closes the reader.
func (c *Client) Download(ctx context.Context, id string, f Format) (io.ReadCloser, error) {
	if _, err := ParseFormat(string(f)); err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodGet, "/download/"+url.PathEscape(id)+"/"+string(f))
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
