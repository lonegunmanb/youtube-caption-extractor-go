package caption

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func playerJSON(status, title string, tracks []captionTrack) string {
	var player playerResponse
	player.PlayabilityStatus.Status = status
	player.VideoDetails.Title = &title
	player.Captions.Tracklist.Tracks = tracks
	body, _ := json.Marshal(player)
	return string(body)
}

var englishTracks = []captionTrack{{BaseURL: "https://example.test/timedtext?v=test", VSSID: ".en"}}

const transcriptJSON = `{"events":[{"tStartMs":1120,"dDurationMs":4560,"segs":[{"utf8":"Hello &amp; world"}]}]}`

func TestPickCaptionTrack(t *testing.T) {
	first := captionTrack{VSSID: ".es", BaseURL: "first"}
	partial := captionTrack{VSSID: ".en-US", BaseURL: "partial"}
	language := captionTrack{LanguageCode: "en", BaseURL: "language"}
	auto := captionTrack{VSSID: "a.en", BaseURL: "auto"}
	manual := captionTrack{VSSID: ".en", BaseURL: "manual"}
	for _, tc := range []struct {
		name   string
		tracks []captionTrack
		want   *captionTrack
	}{
		{"manual before auto", []captionTrack{first, auto, manual}, &manual},
		{"auto before language", []captionTrack{first, language, auto}, &auto},
		{"language before partial", []captionTrack{partial, language}, &language},
		{"partial before fallback", []captionTrack{first, partial}, &partial},
		{"first available", []captionTrack{first}, &first},
		{"no tracks", nil, nil},
		{"first duplicate", []captionTrack{manual, {VSSID: ".en", BaseURL: "second"}}, &manual},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickCaptionTrack(tc.tracks, "en"); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParseCaptions(t *testing.T) {
	body := `{"events":[
		{"tStartMs":1120,"dDurationMs":4560,"segs":[{"utf8":"  <b>Hello</b> "},{"utf8":"&amp; 世界\nsecond line  "}]},
		{"tStartMs":1000,"dDurationMs":2000,"segs":[{"utf8":"&lt;b&gt;&#39; &#x1F600; &copy;"}]},
		{"segs":[{},{"utf8":" default times "}]},
		{"tStartMs":-100,"dDurationMs":0,"segs":[{"utf8":"negative"}]},
		{"segs":[{"utf8":"discard"}],"aAppend":1},
		{"segs":[{"utf8":"keep"}],"aAppend":2},
		{"tStartMs":500},
		{"segs":[]},
		{"segs":[{"utf8":" \n<b></b>&nbsp;"}]}
	]}`
	want := []Subtitle{
		{Start: "1.12", Dur: "4.56", Text: "Hello & 世界\nsecond line"},
		{Start: "1", Dur: "2", Text: "<b>' 😀 ©"},
		{Start: "0", Dur: "0", Text: "default times"},
		{Start: "-0.1", Dur: "0", Text: "negative"},
		{Start: "0", Dur: "0", Text: "keep"},
	}
	got, err := parseCaptions([]byte(body))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, %v; want %#v", got, err, want)
	}
}

func TestParseCaptionErrors(t *testing.T) {
	for _, tc := range []struct {
		body, want string
	}{
		{"", "Caption response contained no subtitles"},
		{" \n\t\uFEFF", "Caption response contained no subtitles"},
		{"<html>blocked</html>", "Caption response was not valid JSON"},
		{`{"events":[]} trailing`, "Caption response was not valid JSON"},
		{`{}`, "Caption response contained no subtitles"},
		{`{"events":null}`, "Caption response contained no subtitles"},
		{`{"events":[]}`, "Caption response contained no subtitles"},
		{`{"events":[{"segs":[{"utf8":"x"}],"aAppend":1}]}`, "Caption response contained no subtitles"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			_, err := parseCaptions([]byte(tc.body))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestStripTags(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`<b>x</b><br>y`, "xy"},
		{`<b title=">">x</b>`, "x"},
		{`<b title='<">'>x</b>`, "x"},
		{`a<!-- > hidden -->b`, "ab"},
		{`a<!-- hidden`, "a"},
		{`a<<nested>>b`, "ab"},
		{`a<unclosed`, "a"},
		{`1 < 2 > 0`, "1 < 2 > 0"},
		{"1 <\n2", "1 < 2"},
	} {
		if got := stripTags(tc.input); got != tc.want {
			t.Errorf("stripTags(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSeconds(t *testing.T) {
	for _, tc := range []struct {
		ms   float64
		want string
	}{
		{0, "0"}, {1000, "1"}, {1250, "1.25"}, {1, "0.001"},
		{-100, "-0.1"}, {0.001, "0.000001"}, {0.0001, "1.0000000000000001e-7"},
		{1e24, "1e+21"},
	} {
		if got := seconds(tc.ms); got != tc.want {
			t.Errorf("seconds(%g) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}

func TestPlayerFallback(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bodies []string
		title  string
		count  int
	}{
		{"first success", []string{playerJSON("OK", "ios", englishTracks)}, "ios", 1},
		{"error then success", []string{playerJSON("ERROR", "", nil), playerJSON("OK", "android", englishTracks)}, "android", 2},
		{"no captions then success", []string{playerJSON("OK", "first", nil), playerJSON("OK", "second", englishTracks)}, "second", 2},
		{"first playable metadata", []string{playerJSON("OK", "first", nil), playerJSON("OK", "second", nil), playerJSON("ERROR", "", nil)}, "first", 3},
		{"missing status accepted", []string{playerJSON("", "missing", englishTracks)}, "missing", 1},
		{"bad JSON then success", []string{"not JSON", playerJSON("OK", "second", englishTracks)}, "second", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			options := defaults(Options{Fetch: func(req *http.Request) (*http.Response, error) {
				if calls >= len(tc.bodies) {
					t.Fatal("unexpected additional player request")
				}
				client := clients[calls]
				if req.Header.Get("X-YouTube-Client-Name") != client.header {
					t.Errorf("wrong client order: %s", req.Header.Get("X-YouTube-Client-Name"))
				}
				body := tc.bodies[calls]
				calls++
				return response(200, body), nil
			}})
			player, err := fetchPlayer(options)
			if err != nil {
				t.Fatal(err)
			}
			if *player.VideoDetails.Title != tc.title || calls != tc.count {
				t.Fatalf("title=%q calls=%d", *player.VideoDetails.Title, calls)
			}
		})
	}
}

func TestPlayerFailures(t *testing.T) {
	networkErr := errors.New("network unavailable")
	calls := 0
	_, err := GetSubtitles(Options{Fetch: func(req *http.Request) (*http.Response, error) {
		calls++
		switch calls {
		case 1:
			return response(200, `{"playabilityStatus":{"status":"LOGIN_REQUIRED","reason":"Sign in"}}`), nil
		case 2:
			return response(503, ""), nil
		default:
			return nil, networkErr
		}
	}})
	want := "Video not playable on any client. Attempts:\n" +
		"ios: LOGIN_REQUIRED - Sign in\n" +
		"android_vr: InnerTube /player failed (android_vr): 503 Service Unavailable\n" +
		"mweb: network unavailable"
	if err == nil || err.Error() != want || calls != 3 {
		t.Fatalf("got %v (%d calls), want %s", err, calls, want)
	}
}

func TestPublicAPICaptionErrors(t *testing.T) {
	networkErr := errors.New("caption network error")
	for _, api := range []string{"subtitles", "details"} {
		for _, tc := range []struct {
			name, body, want string
			status           int
			err              error
		}{
			{name: "HTTP failure", status: 503, want: "Caption fetch failed: 503"},
			{name: "invalid JSON", status: 200, body: "not JSON", want: "Caption response was not valid JSON"},
			{name: "empty body", status: 200, want: "Caption response contained no subtitles"},
			{name: "empty events", status: 200, body: `{"events":[]}`, want: "Caption response contained no subtitles"},
			{name: "network error", err: networkErr},
		} {
			t.Run(api+"/"+tc.name, func(t *testing.T) {
				calls := 0
				options := Options{VideoID: "test", Fetch: func(req *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return response(200, playerJSON("OK", "title", englishTracks)), nil
					}
					if calls != 2 {
						t.Fatal("caption failure must not retry another client or track")
					}
					return response(tc.status, tc.body), tc.err
				}}
				var err error
				if api == "subtitles" {
					_, err = GetSubtitles(options)
				} else {
					_, err = GetVideoDetails(options)
				}
				if tc.err != nil {
					if err != tc.err {
						t.Fatalf("network error identity lost: %v", err)
					}
				} else if err == nil || err.Error() != tc.want {
					t.Fatalf("got %v, want %s", err, tc.want)
				}
			})
		}
	}
}

func TestMetadataAndMissingTracks(t *testing.T) {
	for _, tc := range []struct {
		name, body, title, description string
	}{
		{"missing", `{}`, "No title found", "No description found"},
		{"null", `{"videoDetails":{"title":null,"shortDescription":null}}`, "No title found", "No description found"},
		{"empty preserved", `{"videoDetails":{"title":"","shortDescription":""}}`, "", ""},
		{"metadata preserved", `{"videoDetails":{"title":"<b>title</b>","shortDescription":" &amp; "}}`, "<b>title</b>", " &amp; "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			options := Options{Fetch: func(req *http.Request) (*http.Response, error) {
				calls++
				return response(200, tc.body), nil
			}}
			got, err := GetVideoDetails(options)
			if err != nil || got.Title != tc.title || got.Description != tc.description || got.Subtitles == nil || len(got.Subtitles) != 0 || calls != 3 {
				t.Fatalf("got %#v, %v, calls=%d", got, err, calls)
			}
			subtitles, err := GetSubtitles(options)
			if err != nil || subtitles == nil || len(subtitles) != 0 {
				t.Fatalf("got %#v, %v", subtitles, err)
			}
			body, _ := json.Marshal(got)
			if !strings.Contains(string(body), `"subtitles":[]`) {
				t.Fatalf("empty subtitles must serialize as []: %s", body)
			}
		})
	}
}

func TestMissingSelectedTrackURL(t *testing.T) {
	calls := 0
	got, err := GetSubtitles(Options{Fetch: func(req *http.Request) (*http.Response, error) {
		calls++
		return response(200, playerJSON("OK", "title", []captionTrack{
			{VSSID: ".en"}, {VSSID: ".es", BaseURL: "https://example.test/unused"},
		})), nil
	}})
	if err != nil || got == nil || len(got) != 0 || calls != 1 {
		t.Fatalf("got %#v, %v, calls=%d", got, err, calls)
	}
}

func TestCaptionURL(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"https://example.test/?v=x&fmt=srv3&sig=a%2Fb", "https://example.test/?v=x&sig=a%2Fb&fmt=json3"},
		{"https://example.test/?v=x", "https://example.test/?v=x&fmt=json3"},
		{"https://example.test/?fmt=srv3&v=x", "https://example.test/?fmt=srv3&v=x&fmt=json3"},
		{"https://example.test/?v=x&fmt=&fmt=srv3&fmt=old", "https://example.test/?v=x&fmt=&fmt=old&fmt=json3"},
	} {
		t.Run(tc.base, func(t *testing.T) {
			var player playerResponse
			player.Captions.Tracklist.Tracks = []captionTrack{{BaseURL: tc.base}}
			_, err := extractSubtitles(&player, defaults(Options{Fetch: func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != tc.want || req.Method != http.MethodGet || req.Header.Get("User-Agent") != clients[0].userAgent {
					t.Errorf("wrong caption request: %s %s %v", req.Method, req.URL, req.Header)
				}
				return response(200, transcriptJSON), nil
			}}))
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestResponseBodiesClosed(t *testing.T) {
	for _, status := range []int{200, 503} {
		body := &trackedBody{Reader: strings.NewReader(`{}`)}
		_, _ = fetchPlayerWithClient(defaults(Options{Fetch: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body}, nil
		}}), clients[0])
		if !body.closed {
			t.Error("player body not closed")
		}
		body = &trackedBody{Reader: strings.NewReader(transcriptJSON)}
		var player playerResponse
		player.Captions.Tracklist.Tracks = englishTracks
		_, _ = extractSubtitles(&player, defaults(Options{Fetch: func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: body}, nil
		}}))
		if !body.closed {
			t.Error("caption body not closed")
		}
	}
}

func TestBodyReadError(t *testing.T) {
	readErr := errors.New("read failed")
	var player playerResponse
	player.Captions.Tracklist.Tracks = englishTracks
	body := &trackedBody{Reader: errorReader{readErr}}
	_, err := extractSubtitles(&player, defaults(Options{Fetch: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: body}, nil
	}}))
	if err != readErr || !body.closed {
		t.Fatalf("got %v, closed=%v", err, body.closed)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := GetSubtitles(Options{Context: ctx, Fetch: func(*http.Request) (*http.Response, error) {
		t.Fatal("canceled context must not initiate requests")
		return nil, nil
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestDefaults(t *testing.T) {
	got := defaults(Options{})
	if got.Lang != "en" || got.Fetch == nil || got.Context == nil {
		t.Fatalf("missing defaults: %#v", got)
	}
}
