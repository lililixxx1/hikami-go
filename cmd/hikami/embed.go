//go:build embedded_web

package main

import "embed"

//go:embed all:webdist
var webDistFS embed.FS
