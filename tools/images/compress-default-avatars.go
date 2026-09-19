// Run from server/: go run ../tools/images/compress-default-avatars.go
// Originals are retained; only versioned display assets are generated.
package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

func main() {
	dir := filepath.Join("..", "web", "public", "images", "avatars")
	for _, name := range []string{"bunny", "cat", "fox", "panda"} {
		original, err := os.ReadFile(filepath.Join(dir, name+".png"))
		if err != nil {
			panic(err)
		}
		src, _, err := image.Decode(bytes.NewReader(original))
		if err != nil {
			panic(err)
		}
		w, h := src.Bounds().Dx(), src.Bounds().Dy()
		if w != h {
			panic("default avatar must be square")
		}
		edge := min(w, 256)
		dst := image.NewNRGBA(image.Rect(0, 0, edge, edge))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		if !dst.Opaque() {
			panic("refusing to drop avatar transparency")
		}
		var out bytes.Buffer
		if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80}); err != nil {
			panic(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+"-256.jpg"), out.Bytes(), 0644); err != nil {
			panic(err)
		}
		fmt.Printf("%s: %d -> %d bytes\n", name, len(original), out.Len())
	}
}
