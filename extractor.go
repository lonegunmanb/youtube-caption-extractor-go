// Package caption extracts timestamped captions and metadata from public YouTube videos.
package caption

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Subtitle is a decoded caption segment with start and duration in seconds.
type Subtitle struct {
	Start string `json:"start"`
	Dur   string `json:"dur"`
	Text  string `json:"text"`
}

// Options configures extraction. VideoID is a video ID, not a URL.
type Options struct {
	VideoID string
	// Lang defaults to "en". It is a preference, not a strict filter.
	Lang string
	// Fetch defaults to http.DefaultClient.Do. A custom client's Do method
	// can provide proxies, timeouts, caching, or retries.
	Fetch func(*http.Request) (*http.Response, error)
	// Context optionally controls cancellation of all extraction requests.
	Context context.Context
}

// VideoDetails contains metadata and captions. Subtitles is non-nil on success.
type VideoDetails struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Subtitles   []Subtitle `json:"subtitles"`
}

type captionTrack struct {
	BaseURL      string `json:"baseUrl"`
	VSSID        string `json:"vssId"`
	LanguageCode string `json:"languageCode"`
}

type playerResponse struct {
	PlayabilityStatus struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"playabilityStatus"`
	VideoDetails struct {
		Title            *string `json:"title"`
		ShortDescription *string `json:"shortDescription"`
	} `json:"videoDetails"`
	Captions struct {
		Tracklist struct {
			Tracks []captionTrack `json:"captionTracks"`
		} `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
}

type clientProfile struct {
	name, clientName, version, header, userAgent string
	context                                      map[string]any
}

var clients = []clientProfile{
	{
		name: "ios", clientName: "IOS", version: "20.10.4", header: "5",
		userAgent: "com.google.ios.youtube/20.10.4 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)",
		context: map[string]any{
			"deviceMake": "Apple", "deviceModel": "iPhone16,2", "platform": "MOBILE",
			"osName": "iOS", "osVersion": "18.3.2.22D82",
		},
	},
	{
		name: "android_vr", clientName: "ANDROID_VR", version: "1.62.20", header: "28",
		userAgent: "com.google.android.apps.youtube.vr.oculus/1.62.20 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip",
		context: map[string]any{
			"deviceMake": "Oculus", "deviceModel": "Quest 3", "platform": "MOBILE",
			"osName": "Android", "osVersion": "12L", "androidSdkVersion": 32,
		},
	},
	{
		name: "mweb", clientName: "MWEB", version: "2.20251209.01.00", header: "2",
		userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
		context: map[string]any{
			"platform": "MOBILE", "osName": "iOS", "osVersion": "17.5.1",
		},
	},
}

const playerEndpoint = "https://youtubei.googleapis.com/youtubei/v1/player?prettyPrint=false"

// GetSubtitles returns captions, or an empty slice when no track is available.
// Unplayable videos and failures reading an advertised track return an error.
func GetSubtitles(options Options) ([]Subtitle, error) {
	options = defaults(options)
	debug("getSubtitles videoID=%s lang=%s", options.VideoID, options.Lang)
	player, err := fetchPlayer(options)
	if err != nil {
		return nil, err
	}
	return extractSubtitles(player, options)
}

// GetVideoDetails returns metadata and the same captions as GetSubtitles.
// Caption extraction errors are returned rather than hidden as empty captions.
func GetVideoDetails(options Options) (VideoDetails, error) {
	options = defaults(options)
	debug("getVideoDetails videoID=%s lang=%s", options.VideoID, options.Lang)
	player, err := fetchPlayer(options)
	if err != nil {
		return VideoDetails{}, err
	}
	subtitles, err := extractSubtitles(player, options)
	if err != nil {
		return VideoDetails{}, err
	}
	title, description := "No title found", "No description found"
	if player.VideoDetails.Title != nil {
		title = *player.VideoDetails.Title
	}
	if player.VideoDetails.ShortDescription != nil {
		description = *player.VideoDetails.ShortDescription
	}
	return VideoDetails{Title: title, Description: description, Subtitles: subtitles}, nil
}

func defaults(options Options) Options {
	if options.Lang == "" {
		options.Lang = "en"
	}
	if options.Fetch == nil {
		options.Fetch = http.DefaultClient.Do
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	return options
}

func fetchPlayer(options Options) (*playerResponse, error) {
	var firstPlayable *playerResponse
	var failures []string
	for _, client := range clients {
		if err := options.Context.Err(); err != nil {
			return nil, err
		}
		player, err := fetchPlayerWithClient(options, client)
		if err != nil {
			failures = append(failures, client.name+": "+err.Error())
			debug("%s client error: %s", client.name, err)
			continue
		}
		status := player.PlayabilityStatus.Status
		debug("%s client returned playabilityStatus=%s", client.name, status)
		if status != "" && status != "OK" {
			failure := client.name + ": " + status
			if reason := player.PlayabilityStatus.Reason; reason != "" {
				failure += " - " + reason
			}
			failures = append(failures, failure)
			continue
		}
		if firstPlayable == nil {
			firstPlayable = player
		}
		if len(player.Captions.Tracklist.Tracks) > 0 {
			return player, nil
		}
		failures = append(failures, client.name+": OK but no caption tracks")
	}
	if err := options.Context.Err(); err != nil {
		return nil, err
	}
	if firstPlayable != nil {
		return firstPlayable, nil
	}
	return nil, fmt.Errorf("Video not playable on any client. Attempts:\n%s", strings.Join(failures, "\n"))
}

func fetchPlayerWithClient(options Options, client clientProfile) (*playerResponse, error) {
	clientContext := map[string]any{
		"clientName": client.clientName, "clientVersion": client.version, "hl": "en", "gl": "US",
	}
	for key, value := range client.context {
		clientContext[key] = value
	}
	body, err := json.Marshal(map[string]any{
		"context": map[string]any{
			"client":  clientContext,
			"user":    map[string]any{"lockedSafetyMode": false},
			"request": map[string]any{"useSsl": true},
		},
		"videoId": options.VideoID, "contentCheckOk": true, "racyCheckOk": true,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(options.Context, http.MethodPost, playerEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", client.userAgent)
	req.Header.Set("X-YouTube-Client-Name", client.header)
	req.Header.Set("X-YouTube-Client-Version", client.version)
	req.Header.Set("Origin", "https://www.youtube.com")
	response, err := options.Fetch(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("InnerTube /player failed (%s): %d %s", client.name, response.StatusCode, http.StatusText(response.StatusCode))
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var player playerResponse
	if err := json.Unmarshal(data, &player); err != nil {
		return nil, err
	}
	return &player, nil
}

func pickCaptionTrack(tracks []captionTrack, lang string) *captionTrack {
	for _, matches := range []func(captionTrack) bool{
		func(t captionTrack) bool { return t.VSSID == "."+lang },
		func(t captionTrack) bool { return t.VSSID == "a."+lang },
		func(t captionTrack) bool { return t.LanguageCode == lang },
		func(t captionTrack) bool { return strings.Contains(t.VSSID, "."+lang) },
	} {
		for i := range tracks {
			if matches(tracks[i]) {
				return &tracks[i]
			}
		}
	}
	if len(tracks) == 0 {
		return nil
	}
	return &tracks[0]
}

var captionFormat = regexp.MustCompile(`&fmt=[^&]+`)

func extractSubtitles(player *playerResponse, options Options) ([]Subtitle, error) {
	track := pickCaptionTrack(player.Captions.Tracklist.Tracks, options.Lang)
	if track == nil || track.BaseURL == "" {
		return []Subtitle{}, nil
	}
	// Preserve signed URL parameters; upstream replaces only the first fmt parameter.
	baseURL := track.BaseURL
	if loc := captionFormat.FindStringIndex(baseURL); loc != nil {
		baseURL = baseURL[:loc[0]] + baseURL[loc[1]:]
	}
	req, err := http.NewRequestWithContext(options.Context, http.MethodGet, baseURL+"&fmt=json3", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", clients[0].userAgent)
	response, err := options.Fetch(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Caption fetch failed: %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return parseCaptions(body)
}

func parseCaptions(body []byte) ([]Subtitle, error) {
	if strings.TrimFunc(string(body), jsSpace) == "" {
		return nil, fmt.Errorf("Caption response contained no subtitles")
	}
	var transcript struct {
		Events []struct {
			Start    float64 `json:"tStartMs"`
			Duration float64 `json:"dDurationMs"`
			Append   int     `json:"aAppend"`
			Segments []struct {
				Text string `json:"utf8"`
			} `json:"segs"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &transcript); err != nil {
		return nil, fmt.Errorf("Caption response was not valid JSON")
	}
	subtitles := make([]Subtitle, 0, len(transcript.Events))
	for _, event := range transcript.Events {
		if event.Segments == nil || event.Append == 1 {
			continue
		}
		var raw strings.Builder
		for _, segment := range event.Segments {
			raw.WriteString(segment.Text)
		}
		text := strings.TrimFunc(html.UnescapeString(stripTags(raw.String())), jsSpace)
		if text != "" {
			subtitles = append(subtitles, Subtitle{
				Start: seconds(event.Start), Dur: seconds(event.Duration), Text: text,
			})
		}
	}
	if len(subtitles) == 0 {
		return nil, fmt.Errorf("Caption response contained no subtitles")
	}
	debug("Parsed %d caption events from json3", len(subtitles))
	return subtitles, nil
}

func seconds(ms float64) string {
	value := ms / 1000
	if value == 0 {
		return "0"
	}
	if absolute := math.Abs(value); absolute >= 1e21 || absolute < 1e-6 {
		parts := strings.Split(strconv.FormatFloat(value, 'e', -1, 64), "e")
		sign, exponent := parts[1][:1], strings.TrimLeft(parts[1][1:], "0")
		return parts[0] + "e" + sign + exponent
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00A0', '\u1680',
		'\u2028', '\u2029', '\u202F', '\u205F', '\u3000', '\uFEFF':
		return true
	}
	return r >= '\u2000' && r <= '\u200A'
}

func stripTags(text string) string {
	var result strings.Builder
	var tag strings.Builder
	inTag, inComment := false, false
	depth := 0
	var quote rune
	for _, r := range text {
		if inComment {
			if r == '>' {
				if strings.HasSuffix(tag.String(), "--") {
					inComment = false
					inTag = false
				}
				tag.Reset()
			} else {
				tag.WriteRune(r)
			}
			continue
		}
		if !inTag {
			if r == '<' {
				inTag = true
				tag.WriteRune(r)
			} else {
				result.WriteRune(r)
			}
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			tag.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			tag.WriteRune(r)
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			} else {
				inTag = false
				tag.Reset()
			}
		case '-':
			if tag.String() == "<!-" {
				inComment = true
			}
			tag.WriteRune(r)
		case ' ', '\n':
			if tag.String() == "<" {
				result.WriteString("< ")
				inTag = false
				tag.Reset()
			} else {
				tag.WriteRune(r)
			}
		default:
			tag.WriteRune(r)
		}
	}
	return result.String()
}

func debug(format string, args ...any) {
	value := os.Getenv("DEBUG")
	if value == "*" || strings.Contains(value, "youtube-caption-extractor") {
		log.Printf("youtube-caption-extractor "+format, args...)
	}
}
