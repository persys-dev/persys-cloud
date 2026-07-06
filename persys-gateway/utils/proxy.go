package utils

import (
	"bytes"
	"crypto/tls"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ProxyOptions configures an outbound proxied request made on behalf of an
// inbound gin request.
type ProxyOptions struct {
	// Timeout bounds the outbound request. Defaults to 10s if zero.
	Timeout time.Duration
	// Headers are added to the outbound request (in addition to the
	// forwarded inbound headers).
	Headers map[string]string
	// TLSConfig, if set, is used for the outbound HTTP client (e.g. mTLS).
	// If nil, a plain HTTP client is used.
	TLSConfig *tls.Config
}

// ProxyRequest forwards the inbound gin request to target (a full URL),
// preserving method, query string, body, and most headers, and streams the
// upstream response (status, headers, body) back to the client.
func ProxyRequest(c *gin.Context, target string, opts *ProxyOptions) {
	if opts == nil {
		opts = &ProxyOptions{}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	var body io.Reader
	if c.Request.Body != nil {
		data, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
			return
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build upstream request"})
		return
	}
	req.URL.RawQuery = c.Request.URL.RawQuery

	for key, values := range c.Request.Header {
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	transport := &http.Transport{}
	if opts.TLSConfig != nil {
		transport.TLSClientConfig = opts.TLSConfig
	}
	client := &http.Client{Timeout: timeout, Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to read upstream response"})
		return
	}

	for key, values := range resp.Header {
		for _, v := range values {
			c.Writer.Header().Add(key, v)
		}
	}
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}
