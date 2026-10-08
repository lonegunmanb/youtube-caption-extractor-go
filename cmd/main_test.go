package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	caption "github.com/lonegunmanb/youtube-caption-extractor-go"
)

func TestVideoID(t *testing.T) {
	const id = "dQw4w9WgXcQ"
	for _, tc := range []struct {
		url, want string
	}{
		{"https://www.youtube.com/watch?v=" + id + "&t=12", id},
		{"https://youtube.com/watch?feature=shared&v=" + id, id},
		{"https://m.youtube.com/shorts/" + id + "?feature=share", id},
		{"https://youtube.com/live/" + id, id},
		{"https://music.youtube.com/watch?v=" + id, id},
		{"https://youtube.com/embed/" + id, id},
		{"https://www.youtube-nocookie.com/embed/" + id, id},
		{"http://youtu.be/" + id + "?si=abc", id},
		{"https://www.youtu.be/" + id, id},
		{"https://www.youtube.com/watch?v=too-short", ""},
		{"https://youtube.com/watch", ""},
		{"https://youtube.com/playlist?v=" + id, ""},
		{"https://youtube-nocookie.com/watch?v=" + id, ""},
		{"https://youtu.be/" + id + "/extra", ""},
		{"https://youtube.com/shorts/" + id + "/extra", ""},
		{"https://youtube.com.evil.test/watch?v=" + id, ""},
		{"https://evil.test/youtube.com/watch?v=" + id, ""},
		{"https://evil.test@youtube.com/watch?v=" + id, ""},
		{"ftp://youtube.com/watch?v=" + id, ""},
		{"youtube.com/watch?v=" + id, ""},
		{"https://youtube.com/watch?v=abcdefghij!", ""},
		{"not a URL", ""},
	} {
		t.Run(tc.url, func(t *testing.T) {
			got, err := videoID(tc.url)
			if got != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("videoID(%q) = %q, %v; want %q", tc.url, got, err, tc.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	want := caption.VideoDetails{
		Title: "Example", Description: "Description",
		Subtitles: []caption.Subtitle{{Start: "1.2", Dur: "3", Text: "Hello"}},
	}
	var output bytes.Buffer
	err := run([]string{"https://youtu.be/dQw4w9WgXcQ"}, &output, func(options caption.Options) (caption.VideoDetails, error) {
		if options.VideoID != "dQw4w9WgXcQ" {
			t.Fatalf("unexpected video ID: %q", options.VideoID)
		}
		return want, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var got caption.VideoDetails
	if err := json.Unmarshal(output.Bytes(), &got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("output = %q, %v; want %#v", output.String(), err, want)
	}

	for _, args := range [][]string{nil, {"https://youtu.be/dQw4w9WgXcQ", "extra"}, {"https://evil.test/watch?v=dQw4w9WgXcQ"}} {
		output.Reset()
		err := run(args, &output, func(caption.Options) (caption.VideoDetails, error) {
			t.Fatal("lookup called for invalid arguments")
			return caption.VideoDetails{}, nil
		})
		if err == nil || output.Len() != 0 {
			t.Fatalf("run(%q) = %q, %v; want error without output", args, output.String(), err)
		}
	}

	output.Reset()
	failure := errors.New("upstream unavailable")
	err = run([]string{"https://youtu.be/dQw4w9WgXcQ"}, &output, func(caption.Options) (caption.VideoDetails, error) {
		return caption.VideoDetails{}, failure
	})
	if !errors.Is(err, failure) || output.Len() != 0 {
		t.Fatalf("lookup failure = %q, %v", output.String(), err)
	}

	output.Reset()
	err = run([]string{"https://youtu.be/dQw4w9WgXcQ"}, &output, func(caption.Options) (caption.VideoDetails, error) {
		return caption.VideoDetails{Subtitles: []caption.Subtitle{}}, nil
	})
	if err != nil || !strings.Contains(output.String(), `"subtitles":[]`) {
		t.Fatalf("empty captions = %q, %v", output.String(), err)
	}
}
