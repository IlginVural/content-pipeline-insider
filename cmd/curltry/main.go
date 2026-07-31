// Command curltry is a throwaway inspection tool for the cURL importer.
// It parses a cURL string and prints what the backend holds internally,
// what it would return over an API, and how the path breaks into
// segments for the dynamic-parameter step. Not part of the service.
//
// Usage:
//
//	go run ./cmd/curltry "curl --request GET 'https://api.partner.com/...'"
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"content-pipeline-insider/internal/curlimport"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, `usage: curltry "curl ..."`)
		os.Exit(2)
	}

	req, err := curlimport.Parse(os.Args[1])
	if err != nil {
		fmt.Println("REJECTED:", err)
		os.Exit(1)
	}

	fmt.Println("=== INTERNAL (holds plaintext secrets — never leaves the backend) ===")
	printJSON(req)

	fmt.Println("\n=== REDACTED (what an API response / audit log may contain) ===")
	printJSON(req.Redacted())

	fmt.Println("\n=== PATH SEGMENTS (candidates for 'make dynamic') ===")
	segments := req.PathSegments()
	if len(segments) == 0 {
		fmt.Println("  (none)")
	}
	for i, seg := range segments {
		fmt.Printf("  path[%d] = %q\n", i, seg)
	}
}

func printJSON(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal error:", err)
		return
	}
	fmt.Println(string(b))
}
