# youtube-caption-extractor-go

A Go standard library implementation of [devhims/youtube-caption-extractor](https://github.com/devhims/youtube-caption-extractor), compatible with the upstream v1.10.2 public API and normal response behavior (commit `cf0d8b5f423ebb2eeee7e1952a07ff73f6e0c537`), with no third-party dependencies.

## Installation and Usage

```sh
go get github.com/lonegunmanb/youtube-caption-extractor-go
```

The CLI tool can fetch video details in JSON format (title, description, subtitles) from a YouTube video URL:

```sh
go install ./cmd/ytbext
go build -o youtube-caption-extractor ./cmd/ytbext
./youtube-caption-extractor 'https://www.youtube.com/watch?v=7GeFt8suV8E'
```

Supported links include `youtube.com/watch?v=...`, `youtube.com/shorts/...`, `youtube.com/live/...`, `youtube.com/embed/...`, `youtu.be/...`, and `youtube-nocookie.com/embed/...`.
On success, JSON is written to standard output; if parameters are invalid or extraction fails, an error is written to standard error and a non-zero exit code is returned.

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	caption "github.com/lonegunmanb/youtube-caption-extractor-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second}
	details, err := caption.GetVideoDetails(caption.Options{
		VideoID: "7GeFt8suV8E",
		Lang:    "en",
		Fetch:   client.Do,
		Context: ctx,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(details.Title, details.Description)
	for _, s := range details.Subtitles {
		fmt.Printf("%s (%ss): %s\n", s.Start, s.Dur, s.Text)
	}
}
```

## API

| Upstream | Go |
| --- | --- |
| `getSubtitles(options)` | `GetSubtitles(options Options) ([]Subtitle, error)` |
| `getVideoDetails(options)` | `GetVideoDetails(options Options) (VideoDetails, error)` |
| `Options.videoID` | `Options.VideoID` (video ID, not the full URL) |
| `Options.lang` | `Options.Lang` (empty string defaults to `en`) |
| `Options.fetch` | `Options.Fetch` (`func(*http.Request) (*http.Response, error)`, default `http.DefaultClient.Do`) |

`Options.Context` is a Go-specific extension (defaults to `context.Background()`) used to cancel requests or set a deadline for the whole operation.
By default, requests have no extra timeout or retry behavior; you can configure timeout, proxy, and transport through a custom `http.Client`.
Custom `Fetch` implementations must follow the return contract of `http.Client.Do`; this library will close response bodies on successful returns.

- `Subtitle`: `Start`, `Dur`, `Text`, all as strings. Time values are in seconds and are not zero-padded.
- `VideoDetails`: `Title`, `Description`, `Subtitles`.
- JSON field names in returned data match upstream: `start`, `dur`, `text`, `title`, `description`, `subtitles`.

## Behavioral Compatibility

- Tries iOS, Android VR, and MWEB clients in order, preferring the first playable response that contains caption tracks;
  if none contain captions, metadata from the first playable response is retained.
- Language selection order: manual captions in the requested language, auto-generated captions, exact `languageCode`,
  match on the `.<lang>` segment in `vssId`, then the first available track. Language is a preference, not a filter.
- Uses JSON3 captions; concatenates segment text, strips HTML tags before decoding entities, preserves internal newlines,
  trims leading/trailing whitespace, and skips empty text or events with `aAppend === 1`.
- If there are no caption tracks or the selected track has no URL, returns a non-nil empty slice (`[]` in JSON).
- If title or description is missing, returns `No title found` and `No description found` respectively; explicit empty strings are preserved.
- Both functions return caption HTTP/parsing errors; ad-provided caption tracks that return empty content are also treated as errors.
  Extraction failures are not hidden by returning empty subtitles.
- If all clients fail, returns `Video not playable on any client. Attempts:\n...`, including status/error per client.
  Caption error messages include `Caption fetch failed: <status>`, `Caption response was not valid JSON`, and `Caption response contained no subtitles`.
- Set `DEBUG=youtube-caption-extractor` or `DEBUG=*` to enable diagnostics; logging is off by default.

Go uses typed JSON parsing; non-protocol responses such as field type mismatches return errors, and JavaScript-style implicit type coercion is not simulated.
Unlike upstream, an explicitly empty `Lang` is treated the same as omitting language. This library is intended for server-side use.
The library API does not convert URL to video ID, translate subtitles, cache, or auto-retry; the CLI tool extracts video ID from URL.

## Testing

```sh
go test ./...
go test -race -cover ./...
go vet ./...
go build ./...
```

Unit tests cover client fallback, request parameters, language priority, subtitle parsing, entities and HTML,
time format, error propagation, response body closing, and context cancellation. Default acceptance tests run both public functions
through a local HTTP server with no external network dependency; `ExampleGetVideoDetails` is also executable.

Real YouTube acceptance tests (manual and auto captions) must be explicitly enabled:

```sh
YOUTUBE_LIVE=1 go test -run TestLiveAcceptance -v
```

YouTube may return `LOGIN_REQUIRED` or bot verification for GitHub Actions, cloud services, or data-center IPs.
Failure of networked tests does not necessarily mean incompatible implementation; in production, you can configure a trusted proxy via `Fetch`,
and implement caching/retry at the application layer as needed.

## License

MIT. See [LICENSE](LICENSE). The upstream project is also MIT-licensed; this project is an independent Go implementation.
