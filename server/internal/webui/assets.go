//go:build webassets

package webui

import (
	"embed"
	"io/fs"
)

// Production builds require the real Vite output. Missing assets fail compilation.
//
//go:embed dist/*
var compiledAssets embed.FS

var assets fs.FS = compiledAssets
