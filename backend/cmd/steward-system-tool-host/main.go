package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"mongojson/backend/internal/service/steward"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func runCLI(args []string, input io.Reader, output, errorOutput io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(errorOutput, "usage: steward-system-tool-host <catalog|run>")
		return 2
	}
	switch args[0] {
	case "catalog":
		writeTo(output, map[string]any{"protocol": "steward-system-tool-catalog/1", "tools": steward.WindowsSystemToolCatalog()})
		return 0
	case "run":
		result, err := execute(input)
		if err != nil {
			writeTo(output, map[string]any{"ok": false, "output": map[string]any{}, "error": err.Error(), "evidence": []any{}})
			return 1
		}
		writeTo(output, map[string]any{"ok": true, "output": result.Output, "evidence": result.Evidence})
		return 0
	default:
		fmt.Fprintln(errorOutput, "unknown command")
		return 2
	}
}

func execute(input io.Reader) (steward.RuntimeToolResult, error) {
	decoder := json.NewDecoder(io.LimitReader(input, 256<<10))
	decoder.DisallowUnknownFields()
	var request steward.WindowsSystemToolHostRequest
	if err := decoder.Decode(&request); err != nil {
		return steward.RuntimeToolResult{}, fmt.Errorf("decode request: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return steward.RuntimeToolResult{}, fmt.Errorf("request must contain exactly one JSON object")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	result, err := steward.ExecuteWindowsSystemToolHost(ctx, request)
	if err != nil {
		return steward.RuntimeToolResult{}, err
	}
	return result, nil
}

func writeTo(output io.Writer, value any) {
	_ = json.NewEncoder(output).Encode(value)
}
