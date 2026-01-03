// Package docs contains document embedding
package docs

import "embed"

//go:embed api.yaml
var Docs embed.FS
