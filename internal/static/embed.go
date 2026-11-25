package static

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var distFS embed.FS

// GetRootFS returns the root filesystem (dist/) for SPA fallback
func GetRootFS() http.FileSystem {
	subFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	return http.FS(subFS)
}

// GetAssetsFS returns the assets filesystem (dist/assets) for /assets/* paths
func GetAssetsFS() http.FileSystem {
	subFS, err := fs.Sub(distFS, "dist/assets")
	if err != nil {
		panic(err)
	}
	return http.FS(subFS)
}

// ReadFile reads a file from the dist directory
func ReadFile(name string) ([]byte, error) {
	return distFS.ReadFile("dist/" + name)
}
