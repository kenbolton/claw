// SPDX-License-Identifier: AGPL-3.0-or-later

// Package console provides the embedded claw-console static assets.
// Build the full console with: make embed-console
package console

import "embed"

//go:embed dist/*
var Assets embed.FS
