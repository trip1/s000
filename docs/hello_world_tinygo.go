package main

import (
	"encoding/json"
	"io"
	"os"
)

func main() {
	in, _ := io.ReadAll(os.Stdin)

	resp := map[string]any{
		"continue": true,
		"output": map[string]any{
			"message":  "hello-world",
			"input":    json.RawMessage(in),
			"function": os.Getenv("S000_FUNCTION_NAME"),
			"trigger":  os.Getenv("S000_FUNCTION_TRIGGER"),
		},
	}

	_ = json.NewEncoder(os.Stdout).Encode(resp)
}
