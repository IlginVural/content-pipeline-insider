// Command fetchtry makes one guarded request against a real API and
// prints what came back. It hardcodes an UpstreamConfig rather than
// building one from a cURL, which is what internal/draft will do later.
//
//	go run ./cmd/fetchtry          # product 1
//	go run ./cmd/fetchtry 5        # product 5
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"content-pipeline-insider/internal/fetcher"
	"content-pipeline-insider/internal/secrets"
	"content-pipeline-insider/internal/upstream"
)

func main() {
	productID := "1"
	if len(os.Args) > 1 {
		productID = os.Args[1]
	}

	// dummyjson.com returns a product record shaped like the README's
	// own example: a name, a price, a stock count, a nested object, and
	// arrays. No credentials and no rate limiting, which makes it a
	// usable stand-in for a partner API.
	cfg := upstream.UpstreamConfig{
		Method:      "GET",
		URLTemplate: "https://dummyjson.com/products/{productId}",
		Headers: []upstream.Header{
			{Name: "Accept", Source: upstream.HeaderStatic, Value: "application/json"},
			{Name: "User-Agent", Source: upstream.HeaderStatic, Value: "content-pipeline-insider"},
		},
		Parameters: map[string]upstream.ParameterDef{
			"productId": {
				Location:     upstream.LocationPath,
				Type:         "integer",
				Required:     true,
				ExampleValue: "1",
				Validation:   &upstream.Validation{Pattern: `^[0-9]+$`, MaximumLength: 10},
			},
		},
		TimeoutMs: 5000,
	}

	// No credential needed for this endpoint, but the resolver is still
	// required by the signature — proof the plumbing works end to end.
	resolver := secrets.NewMemoryResolver(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := upstream.BuildRequest(ctx, cfg, map[string]string{"productId": productID}, resolver)
	if err != nil {
		fmt.Println("build failed:", err)
		os.Exit(1)
	}
	fmt.Println("REQUEST :", req.Method, req.URL.String())

	f := fetcher.New(fetcher.Options{
		Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond,
	})

	resp, err := f.Do(ctx, req)
	if err != nil {
		fmt.Println("fetch failed:", err)
		os.Exit(1)
	}

	fmt.Println("STATUS  :", resp.StatusCode)
	fmt.Println("TYPE    :", resp.ContentType)
	fmt.Println("BYTES   :", len(resp.Body))

	// Print the body before judging it. When a partner answers 4xx the
	// body usually explains why, and hiding it behind the error makes
	// the tool useless exactly when it is most needed.
	fmt.Println("\nBODY:")
	fmt.Println(string(resp.Body))

	if err := fetcher.CheckJSON(resp); err != nil {
		fmt.Println("\nnot usable:", err)
		os.Exit(1)
	}
	fmt.Println("\nOK — usable JSON, ready for Phase 6 parsing.")
}
