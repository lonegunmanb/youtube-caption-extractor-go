package caption_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	caption "github.com/lonegunmanb/youtube-caption-extractor-go"
)

func TestAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name, lang, track                  string
		fallback, noTracks, captionFailure bool
	}{
		{name: "manual captions", track: "manual"},
		{name: "auto captions with client fallback", track: "auto", fallback: true},
		{name: "requested language", lang: "es", track: "spanish"},
		{name: "unknown language uses first track", lang: "ja", track: "spanish"},
		{name: "no tracks retains metadata", noTracks: true},
		{name: "caption failure is visible", captionFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var playerCalls, captionCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch req.URL.Path {
				case "/youtubei/v1/player":
					playerCalls.Add(1)
					if req.Method != http.MethodPost || req.URL.Query().Get("prettyPrint") != "false" {
						t.Errorf("unexpected player request: %s %s", req.Method, req.URL)
					}
					if req.Header.Get("Origin") != "https://www.youtube.com" || req.Header.Get("Content-Type") != "application/json" || req.Header.Get("Accept") != "*/*" {
						t.Errorf("unexpected player headers: %v", req.Header)
					}
					var body struct {
						VideoID        string `json:"videoId"`
						ContentCheckOK bool   `json:"contentCheckOk"`
						RacyCheckOK    bool   `json:"racyCheckOk"`
						Context        struct {
							Client map[string]any `json:"client"`
							User   struct {
								LockedSafetyMode bool `json:"lockedSafetyMode"`
							} `json:"user"`
							Request struct {
								UseSSL bool `json:"useSsl"`
							} `json:"request"`
						} `json:"context"`
					}
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.VideoID != "test-video" || !body.ContentCheckOK || !body.RacyCheckOK || body.Context.Client["hl"] != "en" || body.Context.Client["gl"] != "US" || !body.Context.Request.UseSSL || body.Context.User.LockedSafetyMode {
						t.Errorf("unexpected player body: %#v", body)
					}
					profiles := map[string]struct{ name, version, os string }{
						"5":  {"IOS", "20.10.4", "iOS"},
						"28": {"ANDROID_VR", "1.62.20", "Android"},
						"2":  {"MWEB", "2.20251209.01.00", "iOS"},
					}
					profile, exists := profiles[req.Header.Get("X-YouTube-Client-Name")]
					if !exists || body.Context.Client["clientName"] != profile.name || body.Context.Client["clientVersion"] != profile.version || body.Context.Client["osName"] != profile.os || req.Header.Get("X-YouTube-Client-Version") != profile.version || req.Header.Get("User-Agent") == "" {
						t.Errorf("unexpected client profile: %#v %v", body.Context.Client, req.Header)
					}
					if tc.fallback && req.Header.Get("X-YouTube-Client-Name") == "5" {
						fmt.Fprint(w, `{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in"}}`)
						return
					}
					tracks := []map[string]string{}
					if !tc.noTracks {
						for _, track := range []struct{ id, lang, name string }{
							{".es", "es", "spanish"},
							{"a.en", "en", "auto"},
							{".en", "en", "manual"},
						} {
							if tc.fallback && track.name == "manual" {
								continue
							}
							tracks = append(tracks, map[string]string{
								"vssId": track.id, "languageCode": track.lang,
								"baseUrl": "https://www.youtube.com/timedtext?v=test-video&track=" + track.name + "&fmt=srv3",
							})
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"playabilityStatus": map[string]string{"status": "OK"},
						"videoDetails":      map[string]string{"title": "Acceptance video", "shortDescription": "Example description"},
						"captions":          map[string]any{"playerCaptionsTracklistRenderer": map[string]any{"captionTracks": tracks}},
					})
				case "/timedtext":
					captionCalls.Add(1)
					if req.Method != http.MethodGet || req.URL.Query().Get("fmt") != "json3" || req.Header.Get("User-Agent") != "com.google.ios.youtube/20.10.4 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)" {
						t.Errorf("unexpected caption request: %s %s %v", req.Method, req.URL, req.Header)
					}
					if tc.captionFailure {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{
						map[string]any{
							"tStartMs": 1250, "dDurationMs": 2000,
							"segs": []any{map[string]string{"utf8": "<b>" + req.URL.Query().Get("track") + "</b> &amp; 字幕"}},
						},
					}})
				default:
					t.Errorf("unexpected request path: %s", req.URL)
					http.NotFound(w, req)
				}
			}))
			defer server.Close()
			endpoint, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			options := caption.Options{
				VideoID: "test-video", Lang: tc.lang, Context: ctx,
				Fetch: func(req *http.Request) (*http.Response, error) {
					if req.URL.Host != "youtubei.googleapis.com" && req.URL.Host != "www.youtube.com" {
						t.Fatalf("unexpected upstream host: %s", req.URL.Host)
					}
					local := req.Clone(req.Context())
					local.URL.Scheme = endpoint.Scheme
					local.URL.Host = endpoint.Host
					return server.Client().Do(local)
				},
			}
			details, err := caption.GetVideoDetails(options)
			if tc.captionFailure {
				if err == nil || err.Error() != "Caption fetch failed: 503" {
					t.Fatalf("got %v, want caption HTTP error", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if details.Title != "Acceptance video" || details.Description != "Example description" {
				t.Fatalf("unexpected metadata: %#v", details)
			}
			want := []caption.Subtitle{}
			if !tc.noTracks {
				want = append(want, caption.Subtitle{Start: "1.25", Dur: "2", Text: tc.track + " & 字幕"})
			}
			if !reflect.DeepEqual(details.Subtitles, want) {
				t.Fatalf("got %#v, want %#v", details.Subtitles, want)
			}
			subtitles, err := caption.GetSubtitles(options)
			if err != nil || !reflect.DeepEqual(subtitles, details.Subtitles) {
				t.Fatalf("public functions differ: %#v, %v", subtitles, err)
			}
			wantPlayerCalls, wantCaptionCalls := int32(2), int32(2)
			if tc.fallback {
				wantPlayerCalls = 4
			}
			if tc.noTracks {
				wantPlayerCalls, wantCaptionCalls = 6, 0
			}
			if playerCalls.Load() != wantPlayerCalls || captionCalls.Load() != wantCaptionCalls {
				t.Fatalf("requests: player=%d caption=%d", playerCalls.Load(), captionCalls.Load())
			}
		})
	}
}

func TestLiveAcceptance(t *testing.T) {
	if os.Getenv("YOUTUBE_LIVE") != "1" {
		t.Skip("set YOUTUBE_LIVE=1 to test against YouTube")
	}
	for _, video := range []struct{ name, id string }{
		{"auto", "Q0RFb53ExfY"}, {"manual", "fKxLbERmB4U"},
	} {
		t.Run(video.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			details, err := caption.GetVideoDetails(caption.Options{VideoID: video.id, Context: ctx})
			if err != nil {
				t.Fatal(err)
			}
			if details.Title == "" || details.Title == "No title found" || details.Description == "" || len(details.Subtitles) == 0 {
				t.Fatalf("missing video details: %#v", details)
			}
			for _, subtitle := range details.Subtitles {
				if subtitle.Text == "" {
					t.Fatal("empty caption text")
				}
				for _, value := range []string{subtitle.Start, subtitle.Dur} {
					if _, err := strconv.ParseFloat(value, 64); err != nil {
						t.Fatalf("invalid caption time %q: %v", value, err)
					}
				}
			}
		})
	}
}

func ExampleGetVideoDetails() {
	details, err := caption.GetVideoDetails(caption.Options{
		VideoID: "example",
		Fetch: func(req *http.Request) (*http.Response, error) {
			body := `{"events":[{"tStartMs":1250,"dDurationMs":2000,"segs":[{"utf8":"Hello &amp; world"}]}]}`
			if req.Method == http.MethodPost {
				body = `{"videoDetails":{"title":"Example video","shortDescription":"Description"},"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"https://example.test/timedtext?v=example","vssId":".en"}]}}}`
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(details.Title)
	fmt.Println(details.Subtitles[0].Start, details.Subtitles[0].Text)
	// Output:
	// Example video
	// 1.25 Hello & world
}
