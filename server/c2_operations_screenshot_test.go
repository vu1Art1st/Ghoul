package server

import (
	"encoding/base64"
	"testing"
)

const onePixelPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="

func TestDecodeC2Screenshot(t *testing.T) {
	want, err := base64.StdEncoding.DecodeString(onePixelPNG)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{onePixelPNG, "data:image/png;base64," + onePixelPNG} {
		got, mime, err := decodeC2Screenshot(payload)
		if err != nil {
			t.Fatalf("decode screenshot: %v", err)
		}
		if mime != "image/png" || string(got) != string(want) {
			t.Fatalf("decoded screenshot mismatch: mime=%s bytes=%d", mime, len(got))
		}
	}
	if _, _, err := decodeC2Screenshot("not-an-image"); err == nil {
		t.Fatal("invalid screenshot payload should fail")
	}
}
