// Package scripts embeds the PandaScript parsers in the Go executable.
package scripts

import "embed"

//go:embed common.js engines/*.js
var Files embed.FS
