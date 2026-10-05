package main

import (
	"encoding/json"
	"fmt"
	"os"

	"middle-monitor/backend/api"
)

func main() {
	src, err := os.ReadFile("api/server.go")
	if err != nil {
		panic(err)
	}
	routes := api.ParseRegisteredRoutes(string(src))
	total := 0
	for _, r := range routes {
		total += len(r.Methods)
	}
	fmt.Fprintf(os.Stderr, "%d paths, %d operations\n", len(routes), total)
	out, _ := json.MarshalIndent(routes, "", "  ")
	os.Stdout.Write(out)
}
