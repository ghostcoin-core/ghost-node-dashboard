// Package web holds the dashboard page, embedded into the program.
package web

import "embed"

// Files is the page: index.html and the static files it loads.
//
//go:embed index.html static
var Files embed.FS
