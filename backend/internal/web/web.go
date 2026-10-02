// Package web embeds the built frontend (Vite output copied into dist/ by
// the Makefile). A placeholder index.html is committed so the Go build works
// before the first frontend build; `make frontend` replaces it.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
