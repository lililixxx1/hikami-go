//go:build !embed_ffmpeg

package runtime

func embedAssets() ([]byte, bool) {
	return nil, false
}
