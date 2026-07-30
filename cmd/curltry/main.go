package main

import (
	"encoding/json"
	"fmt"
	"os"

	"content-pipeline-insider/internal/curlimport"
)

func main() {
	req, err := curlimport.Parse(os.Args[1])
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	b, _ := json.MarshalIndent(req, "", "  ")
	fmt.Println(string(b))
}
