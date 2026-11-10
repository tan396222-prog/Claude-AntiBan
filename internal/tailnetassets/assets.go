package tailnetassets

import (
	"embed"
	"io/fs"
)

//go:embed assets/*
var payload embed.FS

func Linux(arch string) (fs.File, error) {
	return payload.Open("assets/cage-tailnet-linux-" + arch + ".gz")
}
