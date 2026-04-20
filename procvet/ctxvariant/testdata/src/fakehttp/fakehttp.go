package fakehttp

import "context"

// Request is the stub net/http.Request shape.
type Request struct{ URL string }

// NewRequest is the non-ctx variant. Callers should
// prefer NewRequestWithContext.
func NewRequest(method, url string) (*Request, error) {
	return &Request{URL: url}, nil
}

// NewRequestWithContext is the ctx-aware variant.
func NewRequestWithContext(
	ctx context.Context, method, url string,
) (*Request, error) {
	return &Request{URL: url}, nil
}

// Get is a non-ctx convenience with no variant — the
// analyzer should NOT flag calls to Get because no
// GetContext/GetWithContext exists in this package.
func Get(url string) (*Request, error) {
	return NewRequest("GET", url)
}

// Command / CommandContext are a different pair,
// testing the "Context" (no "With") suffix.
func Command(name string) string {
	return name
}

func CommandContext(ctx context.Context, name string) string {
	return name
}

// Client is a receiver type to test method-call
// resolution.
type Client struct{}

// Do is the non-ctx method variant.
func (c *Client) Do(req *Request) error {
	return nil
}

// DoContext is the ctx-aware method variant.
func (c *Client) DoContext(
	ctx context.Context, req *Request,
) error {
	return nil
}

// Close is a method with no variant — should NOT be
// flagged.
func (c *Client) Close() error {
	return nil
}
