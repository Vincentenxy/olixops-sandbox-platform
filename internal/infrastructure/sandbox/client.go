package sandbox

import (
	"context"
	"errors"
	"fmt"
	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	Lifecycle *osb.LifecycleClient
	BaseURL   string
	APIKey    string
	HTTP      *http.Client
}

func New(base, key string) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	h := &http.Client{Transport: boundedTransport{transport}, Timeout: 150 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{osb.NewLifecycleClient(base+"/v1", key, osb.WithHTTPClient(h)), base, key, h}
}

// ProxyURL never follows an endpoint-supplied host. Credentials stay on the configured trusted origin.
func (c *Client) ProxyURL(id string, port int) string {
	return fmt.Sprintf("%s/sandboxes/%s/proxy/%d", c.BaseURL, url.PathEscape(id), port)
}
func (c *Client) Exec(ctx context.Context, id string) (*osb.ExecdClient, error) {
	p := true
	ep, e := c.Lifecycle.GetEndpoint(ctx, id, 44772, &p)
	if e != nil {
		return nil, e
	}
	headers := map[string]string{}
	for k, v := range ep.Headers {
		if !strings.EqualFold(k, "Host") && !strings.EqualFold(k, "Authorization") {
			headers[k] = v
		}
	}
	headers["OPEN-SANDBOX-API-KEY"] = c.APIKey
	return osb.NewExecdClient(c.ProxyURL(id, 44772), "", osb.WithHTTPClient(c.HTTP), osb.WithHeaders(headers)), nil
}

// SDK helpers buffer response bodies. Enforce limits while reading, including chunked replies.
var ErrResponseTooLarge = errors.New("upstream response exceeds platform size limit")

type boundedTransport struct{ http.RoundTripper }

func (t boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	// Bound SSE too: the SDK buffers a complete line before invoking the event callback.
	limit := int64(8 << 20)
	if strings.HasSuffix(req.URL.Path, "/logs") {
		limit = 1 << 20
	}
	if resp.ContentLength > limit {
		resp.Body.Close()
		return nil, ErrResponseTooLarge
	}
	resp.Body = &boundedBody{ReadCloser: resp.Body, remaining: limit}
	return resp, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int64
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var probe [1]byte
		n, err := b.ReadCloser.Read(probe[:])
		if n > 0 {
			return 0, ErrResponseTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.ReadCloser.Read(p)
	b.remaining -= int64(n)
	return n, err
}
