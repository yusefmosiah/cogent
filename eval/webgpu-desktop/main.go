package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func main() {
	addr := flag.String("addr", ":3000", "HTTP listen address")
	root := flag.String("root", "", "workspace root for file browser API")
	flag.Parse()

	workspaceRoot := *root
	if workspaceRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("resolve cwd: %v", err)
		}
		workspaceRoot = filepath.Join(cwd, "data", "fs")
	}

	srv, err := NewServer(workspaceRoot)
	if err != nil {
		log.Fatalf("create server: %v", err)
	}

	fmt.Printf("WebGPU desktop listening on http://localhost%s\n", *addr)
	if err := srv.ListenAndServe(*addr); err != nil {
		log.Fatal(err)
	}
}
