package watch

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// MessageLinkSubmissionResult describes a Telegram message link submitted through
// the same pipeline used by reaction-triggered watch downloads.
type MessageLinkSubmissionResult struct {
	Link      string
	PeerID    int64
	MessageID int
	Total     int
	Queued    int
	Skipped   int
}

type messageLinkSubmission struct {
	link  string
	reply chan messageLinkSubmissionResponse
}

type messageLinkSubmissionResponse struct {
	result MessageLinkSubmissionResult
	err    error
}

// ValidateTelegramMessageHTTPLink checks whether raw is an HTTP(S) Telegram
// message link in one of the formats supported by tutil.ParseMessageLink.
func ValidateTelegramMessageHTTPLink(raw string) (string, error) {
	link := strings.TrimSpace(raw)
	if link == "" {
		return "", fmt.Errorf("链接为空")
	}

	u, err := url.Parse(link)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("链接格式无法解析")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", fmt.Errorf("只支持 HTTP/HTTPS 链接")
	}

	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	switch host {
	case "t.me", "telegram.me", "telegram.dog":
	default:
		return "", fmt.Errorf("域名不是 Telegram 消息链接域名")
	}

	parts := splitMessageLinkPath(u.Path)
	if comment := strings.TrimSpace(u.Query().Get("comment")); comment != "" {
		if len(parts) < 1 || !isSupportedPublicDialogSegment(parts[0]) {
			return "", fmt.Errorf("评论链接缺少频道用户名")
		}
		if !isPositiveInt(comment) {
			return "", fmt.Errorf("评论消息 ID 不是数字")
		}
		return link, nil
	}

	switch len(parts) {
	case 2:
		if !isSupportedPublicDialogSegment(parts[0]) {
			return "", fmt.Errorf("链接缺少频道或用户标识")
		}
		if !isPositiveInt(parts[1]) {
			return "", fmt.Errorf("消息 ID 不是数字")
		}
	case 3:
		if parts[0] == "c" {
			if !isPositiveInt(parts[1]) || !isPositiveInt(parts[2]) {
				return "", fmt.Errorf("私有频道链接中的频道 ID 或消息 ID 不是数字")
			}
			return link, nil
		}
		if !isSupportedPublicDialogSegment(parts[0]) {
			return "", fmt.Errorf("链接缺少频道或用户标识")
		}
		if !isPositiveInt(parts[2]) {
			return "", fmt.Errorf("话题链接中的消息 ID 不是数字")
		}
	case 4:
		if parts[0] != "c" {
			return "", fmt.Errorf("链接路径不是 Telegram 消息链接格式")
		}
		if !isPositiveInt(parts[1]) || !isPositiveInt(parts[3]) {
			return "", fmt.Errorf("私有频道话题链接中的频道 ID 或消息 ID 不是数字")
		}
	default:
		return "", fmt.Errorf("链接路径不是 Telegram 消息链接格式")
	}

	return link, nil
}

func splitMessageLinkPath(path string) []string {
	raw := strings.Split(strings.Trim(path, "/"), "/")
	parts := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

func isPositiveInt(value string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	return err == nil && n > 0
}

func isSupportedPublicDialogSegment(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") {
		return false
	}
	switch strings.ToLower(value) {
	case "c", "s", "joinchat", "addstickers", "proxy", "share":
		return false
	default:
		return true
	}
}
