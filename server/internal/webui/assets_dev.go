//go:build !webassets

package webui

import "io/fs"

// A source-only Go checkout can run backend tests without Node. UI requests
// explicitly return 503; production builds must select the webassets tag.
var assets fs.FS
