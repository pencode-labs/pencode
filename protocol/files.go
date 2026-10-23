package protocol

import (
	"path"
	"strings"
)

func IsTextFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".ttf", ".otf", ".woff", ".woff2", ".mp3", ".wav", ".ogg", ".mp4", ".mov", ".webm", ".mkv", ".avi", ".bin", ".glb", ".gltf", ".zip", ".gz", ".pdf":
		return false
	default:
		return true
	}
}
