package daytrading

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

// browserUA is required, not cosmetic: TWSE answers TWTB4U with an EMPTY body when the
// User-Agent does not look like a browser (verified live). The institution package's
// "stock-scanner/1.0" UA works on T86 but NOT here.
const browserUA = "Mozilla/5.0"

// get performs the GET and returns the body. who prefixes errors so a failure names the
// market that produced it.
func get(ctx context.Context, client *http.Client, url, who string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: request: %w", who, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", who, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", who, err)
	}
	if len(body) == 0 {
		// Distinct from a holiday, which is a well-formed empty payload. An empty BODY
		// means the source refused us (historically: a rejected User-Agent).
		return nil, fmt.Errorf("%s: empty response body", who)
	}
	return body, nil
}
