package fetcher

import (
	"fmt"
	"mime"
	"strings"
)

// CheckJSON verifies a response is something worth parsing as JSON.
//
// Separate from Do because "the HTTP call succeeded" and "the partner
// returned what we expected" are different failures with different
// fixes: one is a network problem, the other means the admin pointed
// at an endpoint that returns HTML, or the API answered 401 with a
// perfectly valid error document.
func CheckJSON(resp *Response) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	if resp.ContentType == "" {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(resp.ContentType)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrUnexpectedType, resp.ContentType)
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return fmt.Errorf("%w: %q", ErrUnexpectedType, mediaType)
	}
	return nil
}
