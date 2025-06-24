package handlers

import (
	_ "embed"
)

//go:embed admin.html
var AdminHTML string

//go:embed admin.js
var AdminJS string

//go:embed admin.css
var AdminCSS string
