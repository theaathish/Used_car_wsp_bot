package dist

import "embed"

//go:embed index.html app.js
var FS embed.FS
