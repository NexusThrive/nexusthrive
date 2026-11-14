package db

import (
	"embed"
	"fmt"
	"io/fs"
)

// kernelFiles embeds the kernel migrations so the API binary can migrate
// before it receives traffic (ADR-0019). The all: prefix keeps the .gitkeep
// placeholder, which lets the directory exist before the first migration.
//
//go:embed all:migrations
var kernelFiles embed.FS

// KernelMigrations returns the kernel goose files rooted at their directory.
func KernelMigrations() fs.FS {
	sub, err := fs.Sub(kernelFiles, "migrations")
	if err != nil {
		// The directory is embedded at build time, so this cannot happen.
		panic(fmt.Sprintf("kernel migrations: %v", err))
	}
	return sub
}
