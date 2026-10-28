package proofs

import (
	"encoding/base64"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/mseptiaan/loong.lat/backend/services/console/internal/ai"
)

func TestDecodePhotoReadsTheDriverAppsDataURLs(t *testing.T) {
	for name, tc := range map[string]struct {
		url, mime string
	}{
		"jpeg":               {dataURL("image/jpeg", "jpeg bytes"), "image/jpeg"},
		"jpg spelling":       {dataURL("image/jpg", "jpeg bytes"), "image/jpeg"},
		"png":                {dataURL("image/png", "png bytes"), "image/png"},
		"webp":               {dataURL("image/webp", "webp bytes"), "image/webp"},
		"upper case type":    {dataURL("IMAGE/PNG", "png bytes"), "image/png"},
		"another parameter":  {"data:image/png;name=a.png;base64," + base64.StdEncoding.EncodeToString([]byte("png bytes")), "image/png"},
		"exactly the limit":  {dataURL("image/png", strings.Repeat("x", ai.MaxImageBytes)), "image/png"},
		"one byte":           {dataURL("image/png", "x"), "image/png"},
		"padding of two":     {dataURL("image/png", "ab"), "image/png"},
		"no padding at all":  {dataURL("image/png", "abc"), "image/png"},
		"spaces in the type": {"data: image/png ;base64," + base64.StdEncoding.EncodeToString([]byte("x")), "image/png"},
	} {
		t.Run(name, func(t *testing.T) {
			mime, data, err := decodePhoto(tc.url)
			if err != nil || mime != tc.mime || len(data) == 0 {
				t.Errorf("decodePhoto = %q, %d bytes, %v", mime, len(data), err)
			}
		})
	}
	_, data, _ := decodePhoto(dataURL("image/png", "png bytes"))
	if string(data) != "png bytes" {
		t.Errorf("data = %q", data)
	}
}

func TestDecodePhotoRefusesWhatCannotBeAnalysedAndNeverQuotesIt(t *testing.T) {
	body := strings.Repeat("QUJD", 50)
	for name, url := range map[string]string{
		"empty":               "",
		"a web address":       "https://example.test/a.jpg",
		"no comma":            "data:image/png;base64",
		"not base64":          "data:image/png," + body,
		"percent encoded":     "data:image/png;charset=utf-8,%89PNG",
		"a gif":               "data:image/gif;base64," + body,
		"a pdf":               "data:application/pdf;base64," + body,
		"no type":             "data:;base64," + body,
		"broken base64":       "data:image/png;base64,@@@@" + body,
		"no data":             "data:image/png;base64,",
		"one over the limit":  dataURL("image/png", strings.Repeat("x", ai.MaxImageBytes+1)),
		"far over the limit":  "data:image/png;base64," + strings.Repeat("QUJD", ai.MaxImageBytes),
		"a huge type":         "data:" + strings.Repeat("image/png", 5000) + ";base64," + body,
		"base64 only by name": "data:image/png;notbase64," + body,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := decodePhoto(url)
			if !errors.Is(err, errPhoto) {
				t.Fatalf("error = %v, want errPhoto", err)
			}
			if len(err.Error()) > 150 || strings.Contains(err.Error(), body[:20]) {
				t.Errorf("the error quotes the photo: %.200s", err)
			}
		})
	}
}

func TestScrubKeepsAnErrorShortOnOneLineAndFreeOfPhotos(t *testing.T) {
	long := strings.Repeat("é", 1000)
	for name, tc := range map[string]struct{ in, want string }{
		"plain":         {"aiapi: unavailable (http 503)", "aiapi: unavailable (http 503)"},
		"newlines":      {"a\nb\r\n\tc", "a b c"},
		"a NUL":         {"a\x00b", "a b"},
		"a data URL":    {"bad input data:image/jpeg;base64,/9j/4AAQSkZJRg== and more", "bad input [data URL]"},
		"invalid UTF-8": {"a\xffb", "ab"},
		"unicode kept":  {"Foto tidak jelas é", "Foto tidak jelas é"},
		"long":          {long, long[:2*maxErrorChars]},
	} {
		t.Run(name, func(t *testing.T) {
			if got := scrub(errors.New(tc.in)); got != tc.want {
				t.Errorf("scrub = %q, want %q", got, tc.want)
			}
		})
	}
}

// A photo that is plainly too big is refused from its length alone: nothing is allocated to decode it.
func TestAPhotoFarOverTheLimitIsRefusedBeforeAnythingIsAllocatedForIt(t *testing.T) {
	url := "data:image/png;base64," + strings.Repeat("QUJD", ai.MaxImageBytes) // 16 MB of base64
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, _, err := decodePhoto(url)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, errPhoto) {
		t.Fatalf("error = %v", err)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 1<<20 {
		t.Errorf("%d bytes were allocated for a photo that was refused by its size", grown)
	}
}
