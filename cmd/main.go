package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"

	caption "github.com/lonegunmanb/youtube-caption-extractor-go"
)

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

func videoID(input string) (string, error) {
	u, err := url.Parse(input)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return "", errors.New("invalid YouTube video URL")
	}

	var id string
	switch strings.ToLower(u.Hostname()) {
	case "youtube.com", "www.youtube.com", "m.youtube.com", "music.youtube.com":
		switch {
		case u.Path == "/watch":
			id = u.Query().Get("v")
		case strings.HasPrefix(u.Path, "/shorts/"):
			id = strings.TrimPrefix(u.Path, "/shorts/")
		case strings.HasPrefix(u.Path, "/live/"):
			id = strings.TrimPrefix(u.Path, "/live/")
		case strings.HasPrefix(u.Path, "/embed/"):
			id = strings.TrimPrefix(u.Path, "/embed/")
		}
	case "youtu.be", "www.youtu.be":
		id = strings.TrimPrefix(u.Path, "/")
	case "youtube-nocookie.com", "www.youtube-nocookie.com":
		id = strings.TrimPrefix(u.Path, "/embed/")
		if !strings.HasPrefix(u.Path, "/embed/") {
			id = ""
		}
	}
	if !videoIDPattern.MatchString(id) {
		return "", errors.New("invalid YouTube video URL")
	}
	return id, nil
}

func run(args []string, output io.Writer, lookup func(caption.Options) (caption.VideoDetails, error)) error {
	if len(args) != 1 {
		return errors.New("usage: youtube-caption-extractor <youtube-video-url>")
	}
	id, err := videoID(args[0])
	if err != nil {
		return err
	}
	details, err := lookup(caption.Options{VideoID: id})
	if err != nil {
		return fmt.Errorf("get video details: %w", err)
	}
	return json.NewEncoder(output).Encode(details)
}

func main() {
	if err := run(os.Args[1:], os.Stdout, caption.GetVideoDetails); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
