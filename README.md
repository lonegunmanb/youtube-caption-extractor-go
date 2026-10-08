# youtube-caption-extractor-go

[devhims/youtube-caption-extractor](https://github.com/devhims/youtube-caption-extractor)
的 Go 标准库实现，兼容上游 v1.10.2（提交 `cf0d8b5f423ebb2eeee7e1952a07ff73f6e0c537`）
的公开 API 和正常响应行为，无第三方依赖。

## 安装与使用

```sh
go get github.com/lonegunmanb/youtube-caption-extractor-go
```

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

| 上游 | Go |
| --- | --- |
| `getSubtitles(options)` | `GetSubtitles(options Options) ([]Subtitle, error)` |
| `getVideoDetails(options)` | `GetVideoDetails(options Options) (VideoDetails, error)` |
| `Options.videoID` | `Options.VideoID`（视频 ID，不是完整 URL） |
| `Options.lang` | `Options.Lang`（空字符串默认为 `en`） |
| `Options.fetch` | `Options.Fetch`（`func(*http.Request) (*http.Response, error)`，默认 `http.DefaultClient.Do`） |

`Options.Context` 是 Go 扩展，默认 `context.Background()`，用于取消请求或设置整个操作的期限。
默认请求没有额外的超时或重试；可通过自定义 `http.Client` 配置超时、代理及 Transport。
自定义 `Fetch` 必须遵守 `http.Client.Do` 的返回约定；库负责关闭成功返回的响应体。

- `Subtitle`：`Start`、`Dur`、`Text`，均为字符串。时间为秒，不强制补零。
- `VideoDetails`：`Title`、`Description`、`Subtitles`。
- 返回数据的 JSON 字段与上游一致：`start`、`dur`、`text`、`title`、`description`、`subtitles`。

## 行为兼容

- 依次尝试 iOS、Android VR、MWEB 客户端，优先返回带字幕轨道的可播放响应；
  如果均没有字幕，保留第一个可播放响应的元数据。
- 语言选择依次为：指定语言的人工字幕、自动字幕、精确 `languageCode`、
  `vssId` 中的 `.<lang>` 部分匹配、第一条可用轨道。语言是偏好而非过滤条件。
- 使用 JSON3 字幕；拼接分段文字，先去掉 HTML 标签再解码实体，保留内部换行，
  去除首尾空白，跳过空文字和 `aAppend === 1` 的事件。
- 无字幕轨道或选中轨道缺少 URL 时返回非 nil 空切片（JSON 为 `[]`）。
- 缺少标题或描述时分别返回 `No title found`、`No description found`；保留显式空字符串。
- 两个函数均返回字幕 HTTP/解析错误；广告出的字幕轨道返回空内容也属于错误。
  不会通过返回空字幕掩盖提取失败。
- 所有客户端失败时返回 `Video not playable on any client. Attempts:\n...`，
  包含各客户端的状态或错误。字幕错误消息包括 `Caption fetch failed: <status>`、
  `Caption response was not valid JSON` 和 `Caption response contained no subtitles`。
- 设置 `DEBUG=youtube-caption-extractor` 或 `DEBUG=*` 可启用诊断日志；默认不打印日志。

Go 使用类型化 JSON 解析；字段类型错误等非协议响应会返回错误，不模拟 JavaScript 的
隐式类型转换。与上游不同，显式空 `Lang` 等同于省略语言。该库用于服务端，
不执行 URL 转视频 ID、字幕翻译、缓存或自动重试。

## 测试

```sh
go test ./...
go test -race -cover ./...
go vet ./...
go build ./...
```

单元测试覆盖客户端回退、请求参数、语言优先级、字幕解析、实体与 HTML、
时间格式、错误传播、响应体关闭及上下文取消。默认验收测试通过本地 HTTP 服务
执行两个公开函数，不依赖外网；`ExampleGetVideoDetails` 也是可执行示例。

真实 YouTube 验收测试（人工字幕和自动字幕）需显式启用：

```sh
YOUTUBE_LIVE=1 go test -run TestLiveAcceptance -v
```

YouTube 可能对 GitHub Actions、云服务或数据中心 IP 返回 `LOGIN_REQUIRED` 或
机器人验证。联网测试失败不一定意味着实现不兼容；生产环境可通过 `Fetch`
配置可信代理，并在应用层按需实现缓存和重试。

## 许可证

MIT，见 [LICENSE](LICENSE)。上游项目同样使用 MIT 许可证；本项目为独立 Go 实现。
